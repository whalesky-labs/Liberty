package client

import (
	"context"
	"errors"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

type settingsRetryLoad struct {
	reply chan settingsReply
}

type settingsRetryService struct {
	loads chan settingsRetryLoad
	saves chan settingsCall
}

func (service *settingsRetryService) Settings(context.Context) (domain.Settings, error) {
	call := settingsRetryLoad{reply: make(chan settingsReply, 1)}
	service.loads <- call
	reply := <-call.reply
	return reply.value, reply.err
}

func (service *settingsRetryService) SaveSettings(_ context.Context, value domain.Settings, credential domain.CredentialUpdate) (domain.Settings, application.MutationOutcome, error) {
	call := settingsCall{value: value, credential: credential, reply: make(chan settingsReply, 1)}
	service.saves <- call
	reply := <-call.reply
	return reply.value, application.MutationOutcome{}, reply.err
}

func TestSettingsRetryReloadsAfterInitialAndRefreshReadFailures(t *testing.T) {
	service := &settingsRetryService{loads: make(chan settingsRetryLoad, 4), saves: make(chan settingsCall, 2)}
	callbacks := make(chan func(), 4)
	c := NewSettings(service, func(callback func()) { callbacks <- callback }, func() {}, func(domain.UIPreferences) {})
	defer c.Close()

	c.Load()
	first := receive(t, service.loads)
	first.reply <- settingsReply{err: errors.New("initial read failed")}
	receive(t, callbacks)()
	if c.Loaded || c.Error == nil || !c.CanRetry() {
		t.Fatal("initial read failure did not expose a retryable load state")
	}

	c.Retry()
	second := receive(t, service.loads)
	second.reply <- settingsReply{value: domain.DefaultSettings()}
	receive(t, callbacks)()
	if !c.Loaded || c.Error != nil || c.Dirty() {
		t.Fatal("retry did not complete the initial load")
	}

	c.Load()
	refresh := receive(t, service.loads)
	refresh.reply <- settingsReply{err: errors.New("refresh failed")}
	receive(t, callbacks)()
	c.Retry()
	retried := receive(t, service.loads)
	if len(service.saves) != 0 {
		t.Fatal("read failure retry incorrectly issued a settings save")
	}
	retried.reply <- settingsReply{value: domain.DefaultSettings()}
	receive(t, callbacks)()
	if c.Error != nil || !c.Loaded {
		t.Fatal("refresh read retry did not recover")
	}
}

func TestSettingsRetryResubmitsFailedSaveAndKeepsDraft(t *testing.T) {
	service := &settingsRetryService{loads: make(chan settingsRetryLoad, 2), saves: make(chan settingsCall, 3)}
	callbacks := make(chan func(), 4)
	c := NewSettings(service, func(callback func()) { callbacks <- callback }, func() {}, func(domain.UIPreferences) {})
	defer c.Close()

	c.Load()
	load := receive(t, service.loads)
	load.reply <- settingsReply{value: domain.DefaultSettings()}
	receive(t, callbacks)()
	c.Draft.DefaultHotwords = "keep this draft"
	c.Save()
	failed := receive(t, service.saves)
	failed.reply <- settingsReply{err: errors.New("save failed")}
	receive(t, callbacks)()
	if !c.CanRetry() || c.Draft.DefaultHotwords != "keep this draft" {
		t.Fatal("failed save lost the retryable draft")
	}

	c.Retry()
	retried := receive(t, service.saves)
	if retried.value.DefaultHotwords != "keep this draft" {
		t.Fatal("save retry did not resubmit the draft")
	}
	saved := retried.value
	saved.Revision++
	retried.reply <- settingsReply{value: saved}
	receive(t, callbacks)()
	if c.Error != nil || c.Dirty() || c.CanRetry() {
		t.Fatal("successful save retry left an error or dirty state")
	}
}

func TestSettingsRetryRefreshesWhenFailedSaveDraftWasReverted(t *testing.T) {
	service := &settingsRetryService{loads: make(chan settingsRetryLoad, 3), saves: make(chan settingsCall, 2)}
	callbacks := make(chan func(), 4)
	c := NewSettings(service, func(callback func()) { callbacks <- callback }, func() {}, func(domain.UIPreferences) {})
	defer c.Close()

	c.Load()
	initial := receive(t, service.loads)
	initial.reply <- settingsReply{value: domain.DefaultSettings()}
	receive(t, callbacks)()
	c.Draft.DefaultHotwords = "temporary edit"
	c.Save()
	save := receive(t, service.saves)
	c.Draft.DefaultHotwords = c.original.DefaultHotwords
	c.Save()
	save.reply <- settingsReply{err: errors.New("save failed after edit was reverted")}
	receive(t, callbacks)()
	if c.Dirty() || !c.CanRetry() {
		t.Fatal("reverted save failure did not retain a retryable clean state")
	}

	c.Retry()
	refresh := receive(t, service.loads)
	if len(service.saves) != 0 {
		t.Fatal("retry issued an empty save for a clean draft")
	}
	refresh.reply <- settingsReply{value: domain.DefaultSettings()}
	receive(t, callbacks)()
	if c.Error != nil || !c.Loaded {
		t.Fatal("retry did not recover the clean settings snapshot")
	}
}
