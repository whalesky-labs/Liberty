package client

import (
	"context"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

type updateReply struct {
	result domain.UpdateResult
	err    error
}
type updateCall struct {
	ctx     context.Context
	channel domain.UpdateChannel
	reply   chan updateReply
}
type updateService struct{ calls chan updateCall }

func (s updateService) Check(ctx context.Context, channel domain.UpdateChannel) (domain.UpdateResult, error) {
	call := updateCall{ctx: ctx, channel: channel, reply: make(chan updateReply, 1)}
	s.calls <- call
	reply := <-call.reply
	return reply.result, reply.err
}

func TestUpdateControllerCancelsAndIgnoresStaleResults(t *testing.T) {
	service := updateService{calls: make(chan updateCall, 4)}
	callbacks := make(chan func(), 4)
	c := NewUpdates(service, "2.0.0-alpha.3", func(f func()) { callbacks <- f }, func() {})
	defer c.Close()
	if c.Channel != domain.PreviewUpdates {
		t.Fatal(c.Channel)
	}
	c.Check()
	first := receive(t, service.calls)
	if !c.Open || !c.Busy || c.Result != nil || first.channel != domain.PreviewUpdates {
		t.Fatal("missing checking state")
	}
	c.Check()
	if len(service.calls) != 0 {
		t.Fatal("duplicate check started")
	}
	c.Dismiss()
	if first.ctx.Err() == nil || c.Open || c.Busy {
		t.Fatal("dismiss did not cancel the operation")
	}
	c.Channel = domain.StableUpdates
	c.Check()
	second := receive(t, service.calls)
	first.reply <- updateReply{err: domain.ErrUpdateCheck}
	receive(t, callbacks)()
	if !c.Busy || c.Error != nil || !c.Open {
		t.Fatal("stale response changed current state")
	}
	second.reply <- updateReply{result: domain.UpdateResult{CurrentVersion: "2.0.0-alpha.3", Channel: domain.StableUpdates}}
	receive(t, callbacks)()
	if c.Busy || c.Result == nil || c.Error != nil || second.channel != domain.StableUpdates {
		t.Fatal("new response was lost")
	}
	c.Check()
	third := receive(t, service.calls)
	c.Close()
	third.reply <- updateReply{result: domain.UpdateResult{Available: true}}
	receive(t, callbacks)()
	if c.Open || c.Result != nil || third.ctx.Err() == nil {
		t.Fatal("closed controller accepted late completion")
	}
	c.Check()
	if c.Open {
		t.Fatal("closed controller restarted")
	}
}

func TestUpdateControllerFailureAndRetry(t *testing.T) {
	service := updateService{calls: make(chan updateCall, 3)}
	callbacks := make(chan func(), 3)
	c := NewUpdates(service, "2.0.0", func(f func()) { callbacks <- f }, nil)
	defer c.Close()
	c.Check()
	first := receive(t, service.calls)
	first.reply <- updateReply{err: domain.ErrUpdateCheck}
	receive(t, callbacks)()
	if c.Busy || c.Error != domain.ErrUpdateCheck || c.Result != nil || !c.Open {
		t.Fatal("failed check is not retryable")
	}
	c.Check()
	if !c.Busy || c.Error != nil || c.Result != nil {
		t.Fatal("retry retained the previous state")
	}
	second := receive(t, service.calls)
	second.reply <- updateReply{result: domain.UpdateResult{Available: true, Latest: &domain.Release{Tag: "v2.1.0", Notes: "Release notes"}}}
	receive(t, callbacks)()
	if c.Busy || c.Error != nil || c.Result == nil || !c.Result.Available || c.Result.Latest.Notes != "Release notes" {
		t.Fatal("retry did not publish release notes")
	}
}

func TestUpdateControllerRateLimitBlocksRetryAndReopenUntilDeadline(t *testing.T) {
	service := updateService{calls: make(chan updateCall, 4)}
	callbacks := make(chan func(), 8)
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	c := NewUpdates(service, "2.0.0", func(f func()) { callbacks <- f }, nil)
	c.now = func() time.Time { return now }
	defer c.Close()
	c.Check()
	call := receive(t, service.calls)
	call.reply <- updateReply{err: &domain.UpdateRateLimitError{RetryAt: now.Add(time.Minute)}}
	receive(t, callbacks)()
	if c.CanRetry() || c.RetryAt.IsZero() || c.retryTimer == nil {
		t.Fatal("missing rate-limit cooldown")
	}
	c.Check()
	if c.Busy || c.CanRetry() {
		t.Fatal("premature retry started")
	}
	c.Dismiss()
	if c.retryTimer != nil {
		t.Fatal("dismiss retained timer")
	}
	c.Check()
	if !c.Open || c.Busy || c.CanRetry() || len(service.calls) != 0 {
		t.Fatal("reopen bypassed cooldown")
	}
	now = now.Add(time.Minute)
	if !c.CanRetry() {
		t.Fatal("retry did not unlock")
	}
	c.Check()
	call = receive(t, service.calls)
	call.reply <- updateReply{result: domain.UpdateResult{CurrentVersion: "2.0.0"}}
	receive(t, callbacks)()
	if c.Busy || c.Error != nil || !c.RetryAt.IsZero() || c.retryTimer != nil {
		t.Fatal("retry did not recover")
	}
}

func TestUpdateControllerRateLimitExpiryOnlyRepaints(t *testing.T) {
	service := updateService{calls: make(chan updateCall, 2)}
	callbacks := make(chan func(), 4)
	changes := 0
	c := NewUpdates(service, "2.0.0", func(f func()) { callbacks <- f }, func() { changes++ })
	defer c.Close()
	c.Open = true
	c.Error = domain.ErrUpdateRateLimit
	c.RetryAt = time.Now().Add(-time.Second)
	c.wakeAfterRateLimit()
	receive(t, callbacks)()
	if !c.CanRetry() || changes != 1 || len(service.calls) != 0 {
		t.Fatal("expiry should repaint without fetching")
	}
}
