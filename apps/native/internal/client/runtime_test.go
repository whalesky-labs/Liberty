package client

import (
	"context"
	"testing"

	"liberty.local/liberty/internal/domain"
)

type runtimeCall struct {
	ctx   context.Context
	reply chan domain.RuntimePanel
}
type runtimeService struct {
	calls   chan runtimeCall
	actions chan string
}

func (s runtimeService) Panel(ctx context.Context) (domain.RuntimePanel, error) {
	call := runtimeCall{ctx, make(chan domain.RuntimePanel, 1)}
	s.calls <- call
	return <-call.reply, nil
}
func (s runtimeService) Install(_ context.Context, c string) error {
	s.actions <- "install:" + c
	return nil
}
func (s runtimeService) InstallAll(context.Context) error                   { return nil }
func (s runtimeService) Detect(context.Context, string) error               { return nil }
func (s runtimeService) SwitchSource(context.Context, string, string) error { return nil }
func (s runtimeService) SelectDownloadSource(_ context.Context, source string) error {
	s.actions <- source
	return nil
}
func TestRuntimeControllerCommandFencesPollAndClose(t *testing.T) {
	s := runtimeService{calls: make(chan runtimeCall, 4), actions: make(chan string, 2)}
	callbacks := make(chan func(), 4)
	c := NewRuntime(s, func(f func()) { callbacks <- f }, func() {})
	c.Refresh()
	old := receive(t, s.calls)
	c.SelectDownloadSource("nju")
	if receive(t, s.actions) != "nju" {
		t.Fatal("wrong source command")
	}
	current := receive(t, s.calls)
	if old.ctx.Err() == nil {
		t.Fatal("obsolete query not cancelled")
	}
	c.Install("python")
	select {
	case <-s.actions:
		t.Fatal("overlapping command accepted")
	default:
	}
	current.reply <- domain.RuntimePanel{DownloadSource: "nju"}
	receive(t, callbacks)()
	old.reply <- domain.RuntimePanel{DownloadSource: "official"}
	receive(t, callbacks)()
	if c.Panel == nil || c.Panel.DownloadSource != "nju" || c.Busy {
		t.Fatal("stale poll overwrote selected source")
	}
	c.Refresh()
	closing := receive(t, s.calls)
	c.Close()
	if closing.ctx.Err() == nil {
		t.Fatal("close did not cancel poll")
	}
	closing.reply <- domain.RuntimePanel{DownloadSource: "official"}
	receive(t, callbacks)()
	if c.Panel.DownloadSource != "nju" {
		t.Fatal("closed view updated")
	}
}
