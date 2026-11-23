package client

import (
	"context"
	"errors"
	"liberty.local/liberty/internal/domain"
	"testing"
)

type remoteReply struct {
	ctx   context.Context
	reply chan domain.RemoteCapabilities
}
type controlledRemote struct{ calls chan remoteReply }

func (s controlledRemote) Capabilities(ctx context.Context) (domain.RemoteCapabilities, error) {
	call := remoteReply{ctx, make(chan domain.RemoteCapabilities, 1)}
	s.calls <- call
	return <-call.reply, nil
}
func TestRemoteResetAndCloseRejectLateHandshake(t *testing.T) {
	callbacks := make(chan func(), 4)
	service := controlledRemote{make(chan remoteReply, 4)}
	c := NewRemote(service, func(f func()) { callbacks <- f }, func() {})
	c.Check()
	first := receive(t, service.calls)
	c.Reset()
	if first.ctx.Err() == nil || c.Checking {
		t.Fatal("reset did not cancel request")
	}
	c.Check()
	second := receive(t, service.calls)
	first.reply <- domain.RemoteCapabilities{ServiceVersion: "stale"}
	receive(t, callbacks)()
	if c.Capabilities != nil || !c.Checking {
		t.Fatal("stale handshake replaced checking state")
	}
	second.reply <- domain.RemoteCapabilities{ServiceVersion: "current"}
	receive(t, callbacks)()
	if c.Capabilities == nil || c.Capabilities.ServiceVersion != "current" {
		t.Fatal("handshake not delivered")
	}
	c.Check()
	third := receive(t, service.calls)
	c.Close()
	third.reply <- domain.RemoteCapabilities{ServiceVersion: "closed"}
	receive(t, callbacks)()
	if c.Capabilities != nil || third.ctx.Err() == nil {
		t.Fatal("closed controller accepted callback")
	}
}
func TestSettingsSaveThenWaitsForPublicationAndSuppressesFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		service := settingsService{loaded: domain.DefaultSettings(), calls: make(chan settingsCall, 4)}
		callbacks := make(chan func(), 4)
		c := NewSettings(service, func(f func()) { callbacks <- f }, func() {}, func(domain.UIPreferences) {})
		c.Load()
		receive(t, callbacks)()
		c.Password = "synthetic-token"
		c.Draft.BackendURL = "http://127.0.0.1:8000"
		c.Save()
		first := receive(t, service.calls)
		checked := false
		c.SaveThen(func() { checked = true })
		if checked {
			t.Fatal("checked before credential publication")
		}
		if fail {
			first.reply <- settingsReply{err: errors.New("synthetic save failure")}
		} else {
			saved := first.value
			saved.TokenConfigured = true
			first.reply <- settingsReply{value: saved}
		}
		receive(t, callbacks)()
		if !fail && c.Busy {
			second := receive(t, service.calls)
			second.reply <- settingsReply{value: second.value}
			receive(t, callbacks)()
		}
		if checked == fail || !fail && c.Password != "" {
			t.Fatal("save/check ordering", fail, checked)
		}
		c.Close()
	}
}

type unavailableCapabilities struct{}

func (unavailableCapabilities) Capabilities(context.Context) (domain.RemoteCapabilities, error) {
	return domain.RemoteCapabilities{}, domain.ErrRemote
}
func TestBackgroundCapabilitiesRetainSnapshotUntilFailureAndThenClear(t *testing.T) {
	callbacks := make(chan func(), 2)
	c := NewRemote(unavailableCapabilities{}, func(f func()) { callbacks <- f }, func() {})
	defer c.Close()
	c.Capabilities = &domain.RemoteCapabilities{Operations: []string{"jobs.read"}}
	c.Refresh()
	if c.Capabilities == nil || !c.Checking {
		t.Fatal("background refresh removed usable snapshot prematurely")
	}
	receive(t, callbacks)()
	if c.Capabilities != nil || c.Error == nil || c.Checking {
		t.Fatal("failed refresh left operations enabled")
	}
}
