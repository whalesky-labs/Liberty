package client

import (
	"context"
	"testing"

	"liberty.local/liberty/internal/domain"
)

type dashboardCall struct {
	period string
	ctx    context.Context
	reply  chan struct{}
}
type dashboardService struct{ calls chan dashboardCall }

func (s dashboardService) Overview(ctx context.Context, period string) (domain.DashboardOverview, error) {
	call := dashboardCall{period: period, ctx: ctx, reply: make(chan struct{})}
	s.calls <- call
	<-call.reply
	return domain.DashboardOverview{Range: period}, nil
}

func TestDashboardRejectsLateRangeAndClosedView(t *testing.T) {
	s := dashboardService{calls: make(chan dashboardCall, 3)}
	callbacks := make(chan func(), 3)
	c := NewDashboard(s, func(f func()) { callbacks <- f }, func() {})
	c.Refresh("7d")
	old := receive(t, s.calls)
	c.Refresh("today")
	current := receive(t, s.calls)
	if old.ctx.Err() == nil {
		t.Fatal("old range not canceled")
	}
	close(current.reply)
	receive(t, callbacks)()
	close(old.reply)
	receive(t, callbacks)()
	if c.Overview == nil || c.Overview.Range != "today" {
		t.Fatal("stale range replaced dashboard")
	}
	c.Refresh("all")
	closing := receive(t, s.calls)
	c.Close()
	if closing.ctx.Err() == nil {
		t.Fatal("close did not cancel")
	}
	close(closing.reply)
	receive(t, callbacks)()
	if c.Overview.Range != "today" {
		t.Fatal("closed dashboard updated")
	}
}
