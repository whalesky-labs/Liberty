package client

import (
	"context"
	"errors"
	"testing"

	"liberty.local/liberty/internal/domain"
)

type runtimeRecoveryResult struct {
	panel domain.RuntimePanel
	err   error
}

type runtimeRecoveryPanelCall struct {
	ctx   context.Context
	reply chan runtimeRecoveryResult
}

type runtimeRecoveryActionCall struct {
	name  string
	reply chan error
}

type runtimeRecoveryService struct {
	panels  chan runtimeRecoveryPanelCall
	actions chan runtimeRecoveryActionCall
}

func (service *runtimeRecoveryService) Panel(ctx context.Context) (domain.RuntimePanel, error) {
	call := runtimeRecoveryPanelCall{ctx: ctx, reply: make(chan runtimeRecoveryResult, 1)}
	service.panels <- call
	reply := <-call.reply
	return reply.panel, reply.err
}

func (service *runtimeRecoveryService) action(name string) error {
	call := runtimeRecoveryActionCall{name: name, reply: make(chan error, 1)}
	service.actions <- call
	return <-call.reply
}

func (service *runtimeRecoveryService) Install(_ context.Context, component string) error {
	return service.action("install:" + component)
}
func (service *runtimeRecoveryService) InstallAll(context.Context) error {
	return service.action("install-all")
}
func (service *runtimeRecoveryService) Detect(_ context.Context, component string) error {
	return service.action("detect:" + component)
}
func (service *runtimeRecoveryService) SwitchSource(_ context.Context, component, source string) error {
	return service.action("switch:" + component + ":" + source)
}
func (service *runtimeRecoveryService) SelectDownloadSource(_ context.Context, source string) error {
	return service.action("source:" + source)
}

func TestRuntimeReadFailureClearsStalePanelAndPollingRecovers(t *testing.T) {
	service := &runtimeRecoveryService{panels: make(chan runtimeRecoveryPanelCall, 4), actions: make(chan runtimeRecoveryActionCall, 2)}
	callbacks := make(chan func(), 4)
	c := NewRuntime(service, func(callback func()) { callbacks <- callback }, func() {})
	defer c.Close()
	ready := domain.RuntimePanel{DownloadSource: "official"}
	c.Panel = &ready

	c.poll()
	failed := receive(t, service.panels)
	failed.reply <- runtimeRecoveryResult{err: errors.New("runtime read failed")}
	receive(t, callbacks)()
	if c.Panel != nil || c.Error == nil || c.ReadError == nil {
		t.Fatal("runtime read failure retained stale state")
	}

	c.poll()
	recovered := receive(t, service.panels)
	recovered.reply <- runtimeRecoveryResult{panel: ready}
	receive(t, callbacks)()
	if c.Panel == nil || c.Error != nil || c.ReadError != nil {
		t.Fatal("successful polling did not clear the read error")
	}
}

func TestRuntimeCommandFailureSurvivesBackgroundPollUntilExplicitRefresh(t *testing.T) {
	service := &runtimeRecoveryService{panels: make(chan runtimeRecoveryPanelCall, 4), actions: make(chan runtimeRecoveryActionCall, 2)}
	callbacks := make(chan func(), 4)
	c := NewRuntime(service, func(callback func()) { callbacks <- callback }, func() {})
	defer c.Close()
	panel := domain.RuntimePanel{DownloadSource: "official"}
	c.Panel = &panel

	c.Install("python")
	action := receive(t, service.actions)
	actionError := errors.New("install failed")
	action.reply <- actionError
	postAction := receive(t, service.panels)
	postAction.reply <- runtimeRecoveryResult{panel: panel}
	receive(t, callbacks)()
	if c.Error != actionError || c.Panel == nil {
		t.Fatal("command failure was not preserved with a fresh panel")
	}

	c.poll()
	background := receive(t, service.panels)
	background.reply <- runtimeRecoveryResult{panel: panel}
	receive(t, callbacks)()
	if c.Error != actionError {
		t.Fatal("background polling cleared an actionable command failure")
	}

	c.Refresh()
	explicit := receive(t, service.panels)
	explicit.reply <- runtimeRecoveryResult{panel: panel}
	receive(t, callbacks)()
	if c.Error != nil || c.ReadError != nil {
		t.Fatal("explicit read retry did not clear the command failure")
	}
}
