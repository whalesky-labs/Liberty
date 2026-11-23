package application_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
)

type resourceScopeVault struct {
	values map[string]domain.Secret
	calls  [3]int
}

func (vault *resourceScopeVault) Get(ctx context.Context, reference string) (domain.Secret, error) {
	vault.calls[0]++
	return vault.values[reference], ctx.Err()
}

func (vault *resourceScopeVault) Set(ctx context.Context, reference string, value domain.Secret) error {
	vault.calls[1]++
	if err := ctx.Err(); err != nil {
		return err
	}
	vault.values[reference] = value
	return nil
}

func (vault *resourceScopeVault) Delete(ctx context.Context, reference string) error {
	vault.calls[2]++
	if err := ctx.Err(); err != nil {
		return err
	}
	delete(vault.values, reference)
	return nil
}

type resourceScopeState struct {
	models    []application.ModelRecord
	templates []domain.SummaryTemplate
	members   []domain.MeetingMember
	settings  application.SettingsRecord
	intents   []application.CredentialCleanup
	secrets   map[string]domain.Secret
	calls     [3]int
}

func resourceScopeSnapshot(t *testing.T, store *jobdb.Store, vault *resourceScopeVault) resourceScopeState {
	t.Helper()
	ctx := context.Background()
	var state resourceScopeState
	var err error
	if state.models, err = store.Models(ctx); err != nil {
		t.Fatal(err)
	}
	if state.templates, err = store.Templates(ctx); err != nil {
		t.Fatal(err)
	}
	if state.members, err = store.Members(ctx); err != nil {
		t.Fatal(err)
	}
	if state.settings, err = store.Settings(ctx); err != nil {
		t.Fatal(err)
	}
	if state.intents, err = store.PendingCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	state.secrets = make(map[string]domain.Secret, len(vault.values))
	for reference, value := range vault.values {
		state.secrets[reference] = value
	}
	state.calls = vault.calls
	return state
}

func newResourceScopeService(t *testing.T) (*application.Resources, *jobdb.Store, *resourceScopeVault) {
	t.Helper()
	vault := &resourceScopeVault{values: make(map[string]domain.Secret)}
	store, _, err := jobdb.OpenPrepared(context.Background(), filepath.Join(t.TempDir(), "liberty.sqlite3"), vault, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	service := application.NewResources(store, vault)
	t.Cleanup(service.Close)
	return service, store, vault
}

func seedResourceScopeDraft(t *testing.T, service *application.Resources, kind application.ResourceKind, name string) application.ResourceDraft {
	t.Helper()
	draft := application.NewResourceDraft(kind)
	grant, _, err := service.OpenEditor(context.Background(), kind, "")
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Close()
	draft.Model.Name, draft.Model.BaseURL, draft.Model.Model = name, "https://example.test/v1", "synthetic"
	draft.Template.Name, draft.Template.Prompt = name, "Synthetic prompt"
	draft.Member.Name, draft.Member.Department = name, "Synthetic department"
	update := domain.CredentialUpdate{}
	if kind == application.Models {
		update = domain.CredentialUpdate{Action: domain.SetCredential, Value: domain.NewSecret("synthetic-scope-value")}
	}
	saved, _, err := grant.Save(context.Background(), draft, update)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func TestResourceEditorScopesRejectForeignDraftsWithoutDatabaseOrCredentialEffects(t *testing.T) {
	for _, kind := range []application.ResourceKind{application.Models, application.Templates, application.Members} {
		t.Run(string(kind), func(t *testing.T) {
			ctx := context.Background()
			service, store, vault := newResourceScopeService(t)
			original := seedResourceScopeDraft(t, service, kind, "Original synthetic resource")
			other := seedResourceScopeDraft(t, service, kind, "Other synthetic resource")
			grant, draft, err := service.OpenEditor(ctx, kind, original.ID())
			if err != nil {
				t.Fatal(err)
			}
			defer grant.Close()
			baseline := resourceScopeSnapshot(t, store, vault)
			wrongKind := draft
			wrongKind.Kind = application.Models
			if kind == application.Models {
				wrongKind.Kind = application.Templates
			}
			wrongKind.Model.ID, wrongKind.Template.ID, wrongKind.Member.ID = draft.ID(), draft.ID(), draft.ID()
			for _, forged := range []application.ResourceDraft{other, application.NewResourceDraft(kind), wrongKind, application.NewResourceDraft(application.ResourceKind("settings"))} {
				if _, _, err := grant.Save(ctx, forged, domain.CredentialUpdate{Action: domain.ClearCredential}); !errors.Is(err, application.ErrWindowScope) {
					t.Fatal("editor accepted a foreign identity or resource kind", err)
				}
				if !reflect.DeepEqual(baseline, resourceScopeSnapshot(t, store, vault)) {
					t.Fatal("rejected draft changed resources, settings, cleanup intents or credentials")
				}
			}
			if kind != application.Templates {
				if _, err := grant.Duplicate(ctx, draft, " - Copy"); !errors.Is(err, application.ErrWindowScope) {
					t.Fatal("non-template editor acquired template duplication authority", err)
				}
			}
			reset, err := grant.Reset()
			if err != nil || reset.Kind != kind || reset.ID() != "" {
				t.Fatal("explicit reset did not grant an empty draft of the same kind", err)
			}
			if _, _, err := grant.Save(ctx, draft, domain.CredentialUpdate{}); !errors.Is(err, application.ErrWindowScope) {
				t.Fatal("reset editor retained authority over its old entity", err)
			}
			if _, err := grant.Delete(ctx); !errors.Is(err, application.ErrWindowScope) {
				t.Fatal("new resource scope deleted its previous entity", err)
			}
			if !reflect.DeepEqual(baseline, resourceScopeSnapshot(t, store, vault)) {
				t.Fatal("reset or denied operation mutated stored resources or credentials")
			}
			grant.Close()
			if _, _, err := grant.Save(ctx, reset, domain.CredentialUpdate{}); !errors.Is(err, application.ErrWindowScope) {
				t.Fatal("closed editor saved a new resource", err)
			}
			if _, err := grant.Reset(); !errors.Is(err, application.ErrWindowScope) {
				t.Fatal("closed editor acquired a fresh scope", err)
			}
			if _, err := grant.Delete(ctx); !errors.Is(err, application.ErrWindowScope) {
				t.Fatal("closed editor deleted a resource", err)
			}
			if _, err := grant.Duplicate(ctx, draft, " - Copy"); !errors.Is(err, application.ErrWindowScope) {
				t.Fatal("closed editor duplicated a template", err)
			}
			if !reflect.DeepEqual(baseline, resourceScopeSnapshot(t, store, vault)) {
				t.Fatal("revoked scope had database or credential side effects")
			}
		})
	}
}

func TestResourceEditorOpeningRejectsCancellationAndClosedServiceForEveryKind(t *testing.T) {
	for _, kind := range []application.ResourceKind{application.Models, application.Templates, application.Members} {
		t.Run(string(kind), func(t *testing.T) {
			service, store, vault := newResourceScopeService(t)
			draft := seedResourceScopeDraft(t, service, kind, "Synthetic existing resource")
			baseline := resourceScopeSnapshot(t, store, vault)
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			for _, id := range []string{"", draft.ID()} {
				grant, _, err := service.OpenEditor(canceled, kind, id)
				if grant != nil || !errors.Is(err, context.Canceled) {
					t.Error("canceled editor request issued a resource capability", err)
				}
			}
			service.Close()
			for _, id := range []string{"", draft.ID()} {
				grant, _, err := service.OpenEditor(context.Background(), kind, id)
				if grant != nil || !errors.Is(err, application.ErrWindowScope) {
					t.Error("closed resource service issued an editor capability", err)
				}
			}
			if !reflect.DeepEqual(baseline, resourceScopeSnapshot(t, store, vault)) {
				t.Fatal("rejected opening touched the database or credential store")
			}
		})
	}
}

type canceledResourceScopeRead struct {
	application.ResourceRepository
	cancel context.CancelFunc
}

func (repository canceledResourceScopeRead) Models(context.Context) ([]application.ModelRecord, error) {
	repository.cancel()
	return []application.ModelRecord{{Metadata: domain.AIModel{ID: "synthetic-resource", Name: "Synthetic model"}}}, nil
}

func (repository canceledResourceScopeRead) Templates(context.Context) ([]domain.SummaryTemplate, error) {
	repository.cancel()
	return []domain.SummaryTemplate{{ID: "synthetic-resource", Name: "Synthetic template"}}, nil
}

func (repository canceledResourceScopeRead) Members(context.Context) ([]domain.MeetingMember, error) {
	repository.cancel()
	return []domain.MeetingMember{{ID: "synthetic-resource", Name: "Synthetic member"}}, nil
}

func TestResourceEditorOpeningRejectsCancellationDuringRead(t *testing.T) {
	for _, kind := range []application.ResourceKind{application.Models, application.Templates, application.Members} {
		t.Run(string(kind), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			service := application.NewResources(canceledResourceScopeRead{cancel: cancel}, nil)
			defer service.Close()
			grant, _, err := service.OpenEditor(ctx, kind, "synthetic-resource")
			if grant != nil || !errors.Is(err, context.Canceled) {
				t.Error("request canceled during resource lookup issued an editor capability", err)
			}
		})
	}
}

func TestResourceEditorScopeFollowsOnlyItsCreatedEntityAndRevokesAfterServiceClose(t *testing.T) {
	for _, kind := range []application.ResourceKind{application.Models, application.Templates, application.Members} {
		t.Run(string(kind), func(t *testing.T) {
			ctx := context.Background()
			service, store, vault := newResourceScopeService(t)
			grant, draft, err := service.OpenEditor(ctx, kind, "")
			if err != nil {
				t.Fatal(err)
			}
			defer grant.Close()
			draft.Model.Name, draft.Model.BaseURL, draft.Model.Model = "Synthetic model", "https://example.test/v1", "synthetic"
			draft.Template.Name, draft.Template.Prompt = "Synthetic template", "Synthetic prompt"
			draft.Member.Name = "Synthetic member"
			update := domain.CredentialUpdate{}
			if kind == application.Models {
				update = domain.CredentialUpdate{Action: domain.SetCredential, Value: domain.NewSecret("synthetic-scope-value")}
			}
			saved, _, err := grant.Save(ctx, draft, update)
			if err != nil || saved.Kind != kind || saved.ID() == "" {
				t.Fatal("new editor did not adopt its saved entity", err)
			}
			before := resourceScopeSnapshot(t, store, vault)
			if _, _, err := grant.Save(ctx, draft, update); !errors.Is(err, application.ErrWindowScope) {
				t.Fatal("replaying an empty draft created a second entity", err)
			}
			if !reflect.DeepEqual(before, resourceScopeSnapshot(t, store, vault)) {
				t.Fatal("replayed creation changed durable state or touched credentials")
			}
			saved.Model.Name, saved.Template.Name, saved.Member.Name = "Updated model", "Updated template", "Updated member"
			updated, _, err := grant.Save(ctx, saved, domain.CredentialUpdate{Action: domain.KeepCredential})
			if err != nil || updated.ID() != saved.ID() {
				t.Fatal("editor could not update its own newly created entity", err)
			}
			if _, err := grant.Delete(ctx); err != nil {
				t.Fatal("editor could not delete its own entity", err)
			}
			deleted := resourceScopeSnapshot(t, store, vault)
			if _, _, err := grant.Save(ctx, updated, update); !errors.Is(err, application.ErrWindowScope) {
				t.Fatal("deleted entity could be resurrected through its old draft", err)
			}
			if !reflect.DeepEqual(deleted, resourceScopeSnapshot(t, store, vault)) {
				t.Fatal("stale deleted draft changed durable state or credentials")
			}
			service.Close()
			if _, _, err := grant.Save(ctx, draft, update); !errors.Is(err, application.ErrWindowScope) {
				t.Fatal("resource service shutdown allowed an existing editor to create an entity", err)
			}
			if _, err := grant.Reset(); !errors.Is(err, application.ErrWindowScope) {
				t.Error("resource service shutdown allowed an existing editor to renew its scope", err)
			}
			if !reflect.DeepEqual(deleted, resourceScopeSnapshot(t, store, vault)) {
				t.Fatal("closed service accepted a durable write or credential operation")
			}
		})
	}
}
