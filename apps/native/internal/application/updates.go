package application

import (
	"context"
	"errors"
	"time"

	"liberty.local/liberty/internal/domain"
)

type ReleaseSource interface {
	Releases(context.Context) ([]domain.Release, error)
}

type Updates struct {
	version            string
	source             ReleaseSource
	gate               chan struct{}
	now                func() time.Time
	releases           []domain.Release
	checkedAt, retryAt time.Time
}

const updateCacheLifetime = 5 * time.Minute

func NewUpdates(version string, source ReleaseSource) *Updates {
	return &Updates{version: version, source: source, gate: make(chan struct{}, 1), now: time.Now}
}

func (s *Updates) Check(ctx context.Context, channel domain.UpdateChannel) (domain.UpdateResult, error) {
	result, err := domain.SelectUpdate(s.version, channel, nil)
	if err != nil {
		return result, err
	}
	if s.source == nil {
		return result, domain.ErrUpdateCheck
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// Serialize requests across windows/channels and allow queued callers to
	// cancel. Cache only complete successful reads, with their original date.
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return result, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	now := s.now()
	if !s.checkedAt.IsZero() && now.Before(s.checkedAt.Add(updateCacheLifetime)) {
		result, err = domain.SelectUpdate(s.version, channel, s.releases)
		result.CheckedAt, result.Cached = s.checkedAt, true
		return result, err
	}
	if now.Before(s.retryAt) {
		return result, &domain.UpdateRateLimitError{RetryAt: s.retryAt}
	}
	releases, err := s.source.Releases(ctx)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return result, context.DeadlineExceeded
		}
		if errors.Is(err, domain.ErrUpdateRateLimit) {
			var limited *domain.UpdateRateLimitError
			s.retryAt = s.now().Add(time.Minute)
			if errors.As(err, &limited) && limited.RetryAt.After(s.now()) {
				s.retryAt = limited.RetryAt
			}
			return result, &domain.UpdateRateLimitError{RetryAt: s.retryAt}
		}
		return result, domain.ErrUpdateCheck
	}
	s.releases = append([]domain.Release(nil), releases...)
	s.checkedAt, s.retryAt = s.now(), time.Time{}
	result, err = domain.SelectUpdate(s.version, channel, s.releases)
	result.CheckedAt = s.checkedAt
	return result, err
}
