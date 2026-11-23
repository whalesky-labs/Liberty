package client

import (
	"context"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

type resourceSaveCall struct {
	draft      application.ResourceDraft
	credential domain.CredentialUpdate
}

type editorPort struct {
	kind           application.ResourceKind
	saves          chan resourceSaveCall
	resets         int
	duplicateError error
}

func (p *editorPort) Save(_ context.Context, draft application.ResourceDraft, credential domain.CredentialUpdate) (application.ResourceDraft, application.MutationOutcome, error) {
	p.saves <- resourceSaveCall{draft, credential}
	return draft, application.MutationOutcome{}, nil
}
func (p *editorPort) Delete(context.Context) (application.MutationOutcome, error) {
	return application.MutationOutcome{}, nil
}
func (p *editorPort) Reset() (application.ResourceDraft, error) {
	p.resets++
	return application.NewResourceDraft(p.kind), nil
}
func (p *editorPort) Duplicate(_ context.Context, draft application.ResourceDraft, suffix string) (application.ResourceDraft, error) {
	if p.duplicateError != nil {
		return application.ResourceDraft{}, p.duplicateError
	}
	draft.Template.ID, draft.Template.Builtin = "synthetic-copy", false
	draft.Template.Name += suffix
	return draft, nil
}

func TestResourceEditorDuplicateRefreshesOnlyAfterDurableSuccess(t *testing.T) {
	callbacks := make(chan func(), 4)
	port := &editorPort{kind: application.Templates, duplicateError: domain.ErrResourceInvalid}
	draft := application.NewResourceDraft(application.Templates)
	draft.Template = domain.SummaryTemplate{ID: "synthetic-source", Name: "Synthetic source", Prompt: "Synthetic prompt", Builtin: true}
	saved := 0
	controller := NewResourceEditor(port, draft, func(callback func()) { callbacks <- callback }, func() {}, func() { saved++ })
	defer controller.Close()
	controller.Duplicate(" - Copy")
	receive(t, callbacks)()
	if controller.Draft != draft || controller.Dirty() || saved != 0 || controller.Error == nil {
		t.Fatal("failed duplication changed the editor or refreshed as a successful save")
	}
	port.duplicateError = nil
	controller.Duplicate(" - Copy")
	receive(t, callbacks)()
	if controller.Error != nil || controller.Dirty() || controller.Draft.ID() != "synthetic-copy" || saved != 1 {
		t.Fatal("durable duplication did not publish a clean selected copy and refresh the list")
	}
}
func (p *editorPort) Close() {}

func TestResourceEditorKeepsInvalidSortUntilConfirmedReset(t *testing.T) {
	for _, input := range []string{"", "invalid", "1.5", "9223372036854775808", "-9223372036854775809"} {
		t.Run(input, func(t *testing.T) {
			callbacks := make(chan func(), 8)
			port := &editorPort{kind: application.Members, saves: make(chan resourceSaveCall, 1)}
			draft := application.NewResourceDraft(application.Members)
			draft.Member = domain.MeetingMember{ID: "synthetic-member", Name: "Synthetic member", SortOrder: 42}
			controller := NewResourceEditor(port, draft, func(callback func()) { callbacks <- callback }, func() {}, func() {})
			defer controller.Close()
			controller.MemberSortOrder = input
			controller.Save()
			if !controller.Dirty() || controller.InvalidField != "sortOrder" || controller.Error != domain.ErrResourceInvalid || controller.Busy || controller.Draft.Member.SortOrder != 42 || len(port.saves) != 0 {
				t.Fatal("invalid raw sort was discarded, persisted, or considered clean")
			}
			confirmed := make(chan bool, 1)
			controller.Reset(func(context.Context) (bool, error) { confirmed <- true; return false, nil })
			receive(t, confirmed)
			receive(t, callbacks)()
			if !controller.Dirty() || controller.MemberSortOrder != input || port.resets != 0 {
				t.Fatal("canceled reset lost the raw field or bypassed confirmation")
			}
			controller.Reset(func(context.Context) (bool, error) { return true, nil })
			receive(t, callbacks)()
			if controller.Dirty() || controller.MemberSortOrder != "0" || controller.Draft.ID() != "" || port.resets != 1 {
				t.Fatal("confirmed reset did not replace both typed and raw drafts")
			}
		})
	}
}

func TestResourceEditorPreservesInt64SortOnSave(t *testing.T) {
	for _, input := range []string{"9223372036854775807", "-9223372036854775808", " 17 "} {
		t.Run(input, func(t *testing.T) {
			callbacks := make(chan func(), 4)
			port := &editorPort{kind: application.Members, saves: make(chan resourceSaveCall, 1)}
			draft := application.NewResourceDraft(application.Members)
			draft.Member.Name = "Synthetic member"
			controller := NewResourceEditor(port, draft, func(callback func()) { callbacks <- callback }, func() {}, func() {})
			defer controller.Close()
			controller.MemberSortOrder = input
			controller.Save()
			call := receive(t, port.saves)
			receive(t, callbacks)()
			if controller.Busy || controller.Error != nil || controller.InvalidField != "" || controller.Dirty() || controller.Draft.Member.SortOrder != call.draft.Member.SortOrder {
				t.Fatal("valid integer did not round-trip as a clean saved draft")
			}
			if input == " 17 " && controller.MemberSortOrder != "17" || input != " 17 " && controller.MemberSortOrder != input {
				t.Fatal("saved integer lost precision or was not normalized")
			}
		})
	}
}

func TestResourceEditorValidatesRequiredFieldsBeforeCallingPort(t *testing.T) {
	callbacks := make(chan func(), 4)
	port := &editorPort{kind: application.Models, saves: make(chan resourceSaveCall, 1)}
	controller := NewResourceEditor(port, application.NewResourceDraft(application.Models), func(callback func()) { callbacks <- callback }, func() {}, func() {})
	defer controller.Close()
	for _, step := range []struct {
		field string
		fill  func()
	}{
		{"name", func() { controller.Draft.Model.Name = "Synthetic model" }},
		{"baseUrl", func() { controller.Draft.Model.BaseURL = "https://example.test/v1" }},
		{"apiKey", func() { controller.Password = "synthetic-model-credential" }},
		{"model", func() { controller.Draft.Model.Model = "synthetic-model" }},
	} {
		controller.Save()
		if controller.InvalidField != step.field || controller.Error != domain.ErrResourceInvalid || len(port.saves) != 0 || controller.Busy {
			t.Fatalf("validation order changed at %s", step.field)
		}
		step.fill()
	}
	controller.Save()
	call := receive(t, port.saves)
	receive(t, callbacks)()
	if controller.Error != nil || controller.Password != "" || call.credential.Action != domain.SetCredential || call.credential.Value.Empty() {
		t.Fatal("valid model did not retain credential intent")
	}
	controller.Draft.Model.ID = "synthetic-existing-model"
	controller.Password = "  "
	controller.Save()
	call = receive(t, port.saves)
	receive(t, callbacks)()
	if controller.Error != nil || call.credential.Action != domain.KeepCredential {
		t.Fatal("blank replacement did not keep the existing credential")
	}
}
