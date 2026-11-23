package client

import (
	"context"
	"errors"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

type resultActionFixture struct {
	calls   chan string
	exports chan bool
}

func (f resultActionFixture) RenameSpeaker(_ context.Context, from, to string) error {
	f.calls <- from + ":" + to
	return nil
}
func (f resultActionFixture) Export(_ context.Context, k domain.TextExportKind, title, locale string) (bool, error) {
	f.calls <- string(k) + ":" + title + ":" + locale
	return <-f.exports, nil
}
func TestResultActionsConfirmationCloseAndSingleOperation(t *testing.T) {
	fixture := resultActionFixture{calls: make(chan string, 8), exports: make(chan bool, 2)}
	callbacks := make(chan func(), 8)
	confirmation := make(chan bool, 8)
	renamed := 0
	c := NewResultActions(fixture, fixture, func(context.Context, string, string, string) (bool, error) { return <-confirmation, nil }, func(fn func()) { callbacks <- fn }, func() {}, func(string, string) { renamed++ })
	c.Rename("one", "two", "en-US")
	confirmation <- false
	receive(t, callbacks)()
	if renamed != 0 || c.Busy {
		t.Fatal("cancelled rename reported success")
	}
	select {
	case <-fixture.calls:
		t.Fatal("cancelled rename executed")
	default:
	}
	c.Rename("one", " two ", "en-US")
	c.Export(domain.ExportBundle, "ignored", "en-US")
	confirmation <- true
	if receive(t, fixture.calls) != "one:two" {
		t.Fatal("rename parameters differ")
	}
	receive(t, callbacks)()
	if renamed != 1 || c.Busy || c.Error != nil {
		t.Fatal("rename did not complete")
	}
	c.Export(domain.ExportNotes, "title", "zh-CN")
	if receive(t, fixture.calls) != "notes:title:zh-CN" {
		t.Fatal("export parameters")
	}
	fixture.exports <- false
	receive(t, callbacks)()
	if c.Exported || c.Exporting || c.Busy {
		t.Fatal("cancelled export reported success")
	}
	c.Rename("one", "two", "en-US")
	c.Close()
	confirmation <- true
	receive(t, callbacks)()
	select {
	case <-fixture.calls:
		t.Fatal("closed view mutated data")
	default:
	}
	if renamed != 1 {
		t.Fatal("closed view received callback")
	}
}

func TestResultExportBusyFeedbackAndCloseRejectsLateSuccess(t *testing.T) {
	fixture := resultActionFixture{calls: make(chan string, 4), exports: make(chan bool, 1)}
	callbacks := make(chan func(), 2)
	changed := 0
	controller := NewResultActions(fixture, fixture, nil, func(fn func()) { callbacks <- fn }, func() { changed++ }, nil)
	controller.Export(domain.ExportBundle, "Synthetic meeting", "en-US")
	receive(t, fixture.calls)
	if !controller.Busy || !controller.Exporting || controller.Exported {
		t.Fatal("export did not expose in-progress state")
	}
	controller.Export(domain.ExportNotes, "duplicate", "en-US")
	controller.Close()
	before := changed
	fixture.exports <- true
	receive(t, callbacks)()
	if controller.Exported || changed != before || len(fixture.calls) != 0 {
		t.Fatal("closed export published success or started a second export")
	}
}

type scopedSpeakerActions struct{ calls chan string }

func (p scopedSpeakerActions) Result(_ context.Context, id string) (domain.Result, error) {
	speaker := "speaker"
	return domain.Result{Job: domain.JobSummary{ID: id, Source: "local", DiarizationStatus: "completed"}, Speakers: []domain.Segment{{ID: "segment", Speaker: &speaker, Text: "Synthetic transcript"}}}, nil
}
func (p scopedSpeakerActions) RenameSpeaker(_ context.Context, id, _, _ string) error {
	p.calls <- id
	return nil
}

func TestResultReplacementDuringConfirmationCannotRenamePreviousOrNewJob(t *testing.T) {
	ctx := context.Background()
	port := scopedSpeakerActions{calls: make(chan string, 2)}
	scopes := application.NewWindowScopes(port)
	scopes.EnableEditing(port)
	previous, err := scopes.OpenFromMain(ctx, application.Workbench, "local", "job-1700000000000-1")
	if err != nil {
		t.Fatal(err)
	}
	confirmation := make(chan bool, 1)
	started := make(chan struct{}, 1)
	callbacks := make(chan func(), 1)
	renamed := false
	controller := NewResultActions(previous, nil, func(context.Context, string, string, string) (bool, error) {
		started <- struct{}{}
		return <-confirmation, nil
	}, func(fn func()) { callbacks <- fn }, func() {}, func(string, string) { renamed = true })
	defer controller.Close()
	controller.Rename("speaker", "replacement", "en-US")
	receive(t, started)
	current, err := scopes.OpenFromMain(ctx, application.Workbench, "local", "job-1700000000000-2")
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	confirmation <- true
	receive(t, callbacks)()
	if !errors.Is(controller.Error, application.ErrWindowScope) || controller.Busy || renamed || len(port.calls) != 0 {
		t.Fatal("replaced confirmation escaped its original grant", controller.Error)
	}
	previous.Close()
	if err := current.RenameSpeaker(ctx, "speaker", "current label"); err != nil || receive(t, port.calls) != "job-1700000000000-2" {
		t.Fatal("closing the previous grant revoked the current job", err)
	}
}
