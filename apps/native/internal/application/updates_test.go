package application

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

type releaseSourceFunc func(context.Context) ([]domain.Release, error)

func (f releaseSourceFunc) Releases(ctx context.Context) ([]domain.Release, error) { return f(ctx) }

func TestUpdatesChecksWithDeadlineAndPreservesNotes(t *testing.T) {
	service := NewUpdates("2.0.0-alpha.3", releaseSourceFunc(func(ctx context.Context) ([]domain.Release, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 20*time.Second {
			t.Error("check must have a bounded lifetime")
		}
		return []domain.Release{{Tag: "v2.0.0-alpha.4", Notes: "## 中文\n\n- 修复更新\n\n## English\n\n- Fixed updates", Prerelease: true, PublishedAt: time.Now()}}, nil
	}))
	result, err := service.Check(context.Background(), domain.PreviewUpdates)
	if err != nil || !result.Available || result.Latest == nil || result.Latest.Notes == "" {
		t.Fatal(result, err)
	}
}

func TestUpdatesFailuresCannotReportCurrent(t *testing.T) {
	for _, failure := range []error{errors.New("synthetic transport details"), domain.ErrUpdateRateLimit, context.DeadlineExceeded} {
		service := NewUpdates("2.0.0", releaseSourceFunc(func(context.Context) ([]domain.Release, error) { return nil, failure }))
		result, err := service.Check(context.Background(), domain.StableUpdates)
		if err == nil || result.Latest != nil || result.Available {
			t.Fatal(result, err)
		}
		want := domain.ErrUpdateCheck
		if failure == domain.ErrUpdateRateLimit || failure == context.DeadlineExceeded {
			want = failure
		}
		if !errors.Is(err, want) {
			t.Fatal("error not sanitized", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service := NewUpdates("2.0.0", releaseSourceFunc(func(ctx context.Context) ([]domain.Release, error) { return nil, ctx.Err() }))
	if _, err := service.Check(ctx, domain.StableUpdates); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	service = NewUpdates("unknown", releaseSourceFunc(func(context.Context) ([]domain.Release, error) {
		t.Fatal("invalid version reached network")
		return nil, nil
	}))
	if _, err := service.Check(context.Background(), domain.StableUpdates); err != domain.ErrUpdateVersion {
		t.Fatal(err)
	}
}

func TestUpdatesCacheSharesSnapshotAcrossChannelsAndExpires(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	calls := 0
	failure := error(nil)
	s := NewUpdates("2.0.0-alpha.3", releaseSourceFunc(func(context.Context) ([]domain.Release, error) {
		calls++
		return []domain.Release{{Tag: "v2.0.0-alpha.4", PublishedAt: now, Prerelease: true, Notes: "Release notes"}}, failure
	}))
	s.now = func() time.Time { return now }
	first, err := s.Check(context.Background(), domain.PreviewUpdates)
	if err != nil || !first.Available || first.Cached || first.CheckedAt != now {
		t.Fatal(first, err)
	}
	first.Latest.Notes = "modified caller copy"
	now = now.Add(time.Minute)
	for _, channel := range []domain.UpdateChannel{domain.StableUpdates, domain.PreviewUpdates} {
		result, err := s.Check(context.Background(), channel)
		if err != nil || !result.Cached || calls != 1 || result.CheckedAt != first.CheckedAt {
			t.Fatal(result, calls, err)
		}
		if channel == domain.PreviewUpdates && result.Latest.Notes != "Release notes" {
			t.Fatal("caller mutated cached snapshot")
		}
		if channel == domain.StableUpdates && result.Latest != nil {
			t.Fatal("cache ignored channel")
		}
	}
	now = now.Add(5 * time.Minute)
	failure = domain.ErrUpdateCheck
	result, err := s.Check(context.Background(), domain.PreviewUpdates)
	if err == nil || result.Cached || result.Latest != nil || calls != 2 {
		t.Fatal("expired cache masked failed check", result, err, calls)
	}
}

func TestUpdatesRateLimitHonorsDeadlineAndDoesNotCacheFailure(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(10 * time.Minute)
	calls := 0
	s := NewUpdates("2.0.0", releaseSourceFunc(func(context.Context) ([]domain.Release, error) {
		calls++
		if calls == 1 {
			return nil, &domain.UpdateRateLimitError{RetryAt: deadline}
		}
		return []domain.Release{}, nil
	}))
	s.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		result, err := s.Check(context.Background(), domain.StableUpdates)
		var limit *domain.UpdateRateLimitError
		if !errors.As(err, &limit) || limit.RetryAt != deadline || result.Latest != nil || calls != 1 {
			t.Fatal(result, err, calls)
		}
	}
	now = deadline
	result, err := s.Check(context.Background(), domain.StableUpdates)
	if err != nil || calls != 2 || result.Cached {
		t.Fatal(result, err, calls)
	}
}

func TestUpdatesConcurrentChecksShareOneRequest(t *testing.T) {
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	s := NewUpdates("2.0.0", releaseSourceFunc(func(ctx context.Context) ([]domain.Release, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return []domain.Release{}, nil
	}))
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			if _, err := s.Check(context.Background(), domain.StableUpdates); err != nil {
				t.Error(err)
			}
		})
	}
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Check(ctx, domain.StableUpdates); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("checks were not coalesced", calls.Load())
	}
}
