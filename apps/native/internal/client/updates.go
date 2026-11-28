package client

import (
	"context"
	"errors"
	"time"

	"liberty.local/liberty/internal/domain"
)

type UpdateService interface {
	Check(context.Context, domain.UpdateChannel) (domain.UpdateResult, error)
}

// UpdateController is owned by the main UI thread. Worker results are delivered
// through dispatch, with cancelled/closed checks invalidated by generation.
type UpdateController struct {
	Open, Busy bool
	Result     *domain.UpdateResult
	Error      error
	Channel    domain.UpdateChannel
	RetryAt    time.Time
	service    UpdateService
	dispatch   func(func())
	changed    func()
	cancel     context.CancelFunc
	revision   uint64
	closed     bool
	retryTimer *time.Timer
	now        func() time.Time
}

func NewUpdates(service UpdateService, version string, dispatch func(func()), changed func()) *UpdateController {
	return &UpdateController{service: service, Channel: domain.DefaultUpdateChannel(version), dispatch: dispatch, changed: changed, now: time.Now}
}

func (c *UpdateController) CanRetry() bool {
	return c != nil && !c.closed && !c.Busy && (c.RetryAt.IsZero() || !c.now().Before(c.RetryAt))
}

func (c *UpdateController) Check() {
	if c == nil || c.closed || c.Busy {
		return
	}
	if !c.CanRetry() {
		c.revision++
		c.Open, c.Result, c.Error = true, nil, &domain.UpdateRateLimitError{RetryAt: c.RetryAt}
		c.wakeAfterRateLimit()
		c.notify()
		return
	}
	c.stopRetryTimer()
	c.RetryAt = time.Time{}
	c.Open, c.Busy, c.Result, c.Error = true, true, nil, nil
	c.revision++
	revision, channel := c.revision, c.Channel
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.notify()
	go func() {
		defer cancel()
		var result domain.UpdateResult
		err := domain.ErrUpdateCheck
		if c.service != nil {
			result, err = c.service.Check(ctx, channel)
		}
		c.dispatch(func() {
			if c.closed || revision != c.revision {
				return
			}
			c.Busy, c.Error, c.cancel = false, err, nil
			if err == nil {
				c.Result = &result
			} else if errors.Is(err, domain.ErrUpdateRateLimit) {
				c.RetryAt = c.now().Add(time.Minute)
				var limited *domain.UpdateRateLimitError
				if errors.As(err, &limited) && limited.RetryAt.After(c.now()) {
					c.RetryAt = limited.RetryAt
				}
				c.wakeAfterRateLimit()
			}
			c.notify()
		})
	}()
}

func (c *UpdateController) Dismiss() {
	if c == nil || c.closed {
		return
	}
	c.revision++
	c.stopRetryTimer()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	c.Open, c.Busy, c.Result, c.Error = false, false, nil, nil
	c.notify()
}

// Re-enable the button when GitHub permits retrying. This timer never sends a
// request; the user still starts the next check explicitly.
func (c *UpdateController) wakeAfterRateLimit() {
	c.stopRetryTimer()
	revision := c.revision
	c.retryTimer = time.AfterFunc(max(0, c.RetryAt.Sub(c.now())), func() {
		c.dispatch(func() {
			if c.closed || !c.Open || c.revision != revision {
				return
			}
			c.retryTimer = nil
			c.notify()
		})
	})
}

func (c *UpdateController) stopRetryTimer() {
	if c.retryTimer != nil {
		c.retryTimer.Stop()
		c.retryTimer = nil
	}
}

func (c *UpdateController) Close() {
	if c == nil || c.closed {
		return
	}
	c.Dismiss()
	c.closed = true
}

func (c *UpdateController) notify() {
	if c.changed != nil {
		c.changed()
	}
}
