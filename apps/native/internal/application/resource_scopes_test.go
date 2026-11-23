package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

type templateRepository struct {
	ResourceRepository
	values     []domain.SummaryTemplate
	writeError error
}

func (r *templateRepository) Templates(ctx context.Context) ([]domain.SummaryTemplate, error) {
	return append([]domain.SummaryTemplate(nil), r.values...), ctx.Err()
}
func (r *templateRepository) SaveTemplate(ctx context.Context, value domain.SummaryTemplate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.writeError != nil {
		return r.writeError
	}
	for index := range r.values {
		if r.values[index].ID == value.ID {
			r.values[index] = value
			return nil
		}
	}
	r.values = append(r.values, value)
	return nil
}

func TestTemplateDuplicationPersistsStoredSourceAndTransfersOnlyCopyScope(t *testing.T) {
	ctx := context.Background()
	source := domain.SummaryTemplate{ID: "synthetic-builtin", Name: "Synthetic original", Description: "Stored description", Prompt: "Stored synthetic prompt", Builtin: true, IncludeSpeaker: true, CreatedAt: "2026-01-01T00:00:00+00:00", UpdatedAt: "2026-01-01T00:00:00+00:00"}
	repository := &templateRepository{values: []domain.SummaryTemplate{source}}
	service := NewResources(repository, nil)
	defer service.Close()
	service.now = func() time.Time { return time.Date(2026, 11, 19, 4, 30, 0, 0, time.UTC) }
	grant, draft, err := service.OpenEditor(ctx, Templates, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Close()
	draft.Template.Name, draft.Template.Prompt = "Unsaved draft", ""
	copy, err := grant.Duplicate(ctx, draft, " - Copy")
	if err != nil {
		t.Fatal(err)
	}
	if len(repository.values) != 2 || repository.values[0] != source || copy.ID() == "" || copy.ID() == source.ID || copy.Template.Builtin || copy.Template.Name != source.Name+" - Copy" || copy.Template.Prompt != source.Prompt || copy.Template.IncludeSpeaker != source.IncludeSpeaker || copy.Template.CreatedAt != domain.PersistedUTC(service.now()) || copy.Template.UpdatedAt != copy.Template.CreatedAt {
		t.Fatal("copy was not persisted from the stored source or changed the original")
	}
	if _, _, err := grant.Save(ctx, draft, domain.CredentialUpdate{}); !errors.Is(err, ErrWindowScope) {
		t.Fatal("copy grant retained write access to the original", err)
	}
	copy.Template.Name = "Edited copy"
	if _, _, err := grant.Save(ctx, copy, domain.CredentialUpdate{}); err != nil || len(repository.values) != 2 || repository.values[1].Name != copy.Template.Name || repository.values[0] != source {
		t.Fatal("copy grant could not edit only its new template", err)
	}
	grant.Close()
	if _, err := grant.Duplicate(ctx, copy, " - Copy"); !errors.Is(err, ErrWindowScope) || len(repository.values) != 2 {
		t.Fatal("revoked grant duplicated a template", err)
	}
}

func TestTemplateDuplicationFailureAndCancellationKeepOriginalScope(t *testing.T) {
	ctx := context.Background()
	source := domain.SummaryTemplate{ID: "synthetic-source", Name: "Synthetic source", Prompt: "Synthetic prompt", Builtin: true}
	repository := &templateRepository{values: []domain.SummaryTemplate{source}, writeError: errors.New("synthetic-write-failure")}
	service := NewResources(repository, nil)
	defer service.Close()
	grant, draft, err := service.OpenEditor(ctx, Templates, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Close()
	if _, err := grant.Duplicate(ctx, draft, " - Copy"); !errors.Is(err, repository.writeError) || grant.id != source.ID || len(repository.values) != 1 {
		t.Fatal("failed copy changed the editor scope or source", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := grant.Duplicate(canceled, draft, " - Copy"); !errors.Is(err, context.Canceled) || grant.id != source.ID || len(repository.values) != 1 {
		t.Fatal("canceled copy changed the editor scope or source", err)
	}
	forged := draft
	forged.Template.ID = "another-template"
	if _, err := grant.Duplicate(ctx, forged, " - Copy"); !errors.Is(err, ErrWindowScope) {
		t.Fatal("copy bypassed the editor's original resource scope", err)
	}
	repository.writeError = nil
	if _, err := grant.Duplicate(ctx, draft, " - Copy"); err != nil || len(repository.values) != 2 || repository.values[0] != source {
		t.Fatal("copy could not retry safely after a failed write", err)
	}
}
