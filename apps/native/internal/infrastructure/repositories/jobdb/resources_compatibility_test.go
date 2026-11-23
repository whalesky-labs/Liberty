//go:build compatibility

package jobdb

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

type syntheticCredentials struct {
	mu                  sync.Mutex
	values              map[string]domain.Secret
	failDelete, failSet bool
	onSet               func()
}

func (c *syntheticCredentials) Get(_ context.Context, key string) (domain.Secret, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.values[key], nil
}
func (c *syntheticCredentials) Set(_ context.Context, key string, value domain.Secret) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failSet {
		return domain.ErrCredentialStore
	}
	if c.values == nil {
		c.values = make(map[string]domain.Secret)
	}
	c.values[key] = value
	if c.onSet != nil {
		c.onSet()
	}
	return nil
}
func (c *syntheticCredentials) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failDelete {
		return domain.ErrCredentialStore
	}
	delete(c.values, key)
	return nil
}
func modelDraft() domain.AIModel {
	return domain.AIModel{Name: "Synthetic model", BaseURL: "https://example.test/v1", Model: "test", Enabled: true, IsDefault: true}
}
func syntheticUpdate() domain.CredentialUpdate {
	return domain.CredentialUpdate{Action: domain.SetCredential, Value: domain.NewSecret("synthetic-resource-test")}
}
func TestWriteResourcesCredentialsRollbackRecovery(t *testing.T) {
	ctx := context.Background()
	store, _ := fixtureStore(t)
	vault := &syntheticCredentials{}
	service := application.NewResources(store, vault)
	first, outcome, err := service.SaveModel(ctx, modelDraft(), syntheticUpdate())
	if err != nil || outcome.CleanupPending || !first.CredentialPresent {
		t.Fatal("initial save failed", err)
	}
	record, err := store.Model(ctx, first.ID)
	if err != nil || record.LegacyPresent || record.Reference == "" {
		t.Fatal("metadata contains plaintext or no reference")
	}
	previous := record.Reference
	vault.failDelete = true
	first.Name = "Updated model"
	_, outcome, err = service.SaveModel(ctx, first, syntheticUpdate())
	if err != nil || !outcome.CleanupPending {
		t.Fatal("committed save misreported", err)
	}
	record, _ = store.Model(ctx, first.ID)
	if record.Reference == previous || record.Metadata.Name != "Updated model" {
		t.Fatal("saved model rolled back")
	}
	intents, err := store.PendingCredentials(ctx)
	if err != nil || len(intents) != 1 {
		t.Fatal("cleanup intent not durable")
	}
	var attempt int
	if store.db.QueryRow(`SELECT attempt FROM credential_cleanup_intents WHERE id=?`, intents[0].ID).Scan(&attempt) != nil || attempt != 1 {
		t.Fatal("cleanup failure not recorded")
	}
	vault.failDelete = false
	if application.NewResources(store, vault).RecoverCredentials(ctx) != nil {
		t.Fatal("cleanup retry failed")
	}
	if _, exists := vault.values[previous]; exists {
		t.Fatal("retired secret remains")
	}
	// Database failure must not consume the current secret or default flag.
	if _, err := store.db.Exec(`CREATE TRIGGER reject_resource_save BEFORE INSERT ON ai_model_configs WHEN NEW.name='rejected' BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	failed := modelDraft()
	failed.Name = "rejected"
	if _, _, err := service.SaveModel(ctx, failed, syntheticUpdate()); err == nil {
		t.Fatal("injected failure accepted")
	}
	if len(vault.values) != 1 {
		t.Fatal("orphan staged secret remains")
	}
	records, _ := store.Models(ctx)
	if len(records) != 1 || !records[0].Metadata.IsDefault {
		t.Fatal("failed save changed default")
	}
	vault.failDelete = true
	if result, err := service.DeleteModel(ctx, first.ID); err != nil || !result.CleanupPending {
		t.Fatal("committed deletion misreported")
	}
	if _, err := store.Model(ctx, first.ID); !errors.Is(err, domain.ErrResourceMissing) {
		t.Fatal("model not deleted")
	}
	vault.failDelete = false
	if service.RecoverCredentials(ctx) != nil || len(vault.values) != 0 {
		t.Fatal("delete cleanup did not recover")
	}
}
func TestWriteSettingsRacePreservesRuntimeAndCredential(t *testing.T) {
	ctx := context.Background()
	store, _ := fixtureStore(t)
	vault := &syntheticCredentials{}
	service := application.NewResources(store, vault)
	settings, err := service.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings, _, err = service.SaveSettings(ctx, settings, syntheticUpdate())
	if err != nil {
		t.Fatal(err)
	}
	before, _ := store.Settings(ctx)
	vault.onSet = func() {
		if err := store.RuntimeSource(ctx, "python", "system"); err != nil {
			t.Error(err)
		}
	}
	settings.ThemeMode = "dark"
	_, _, err = service.SaveSettings(ctx, settings, syntheticUpdate())
	var conflict *domain.SettingsConflict
	if !errors.As(err, &conflict) || conflict.Current.Revision != settings.Revision+1 || !conflict.Current.TokenConfigured {
		t.Fatal("settings conflict missing", err)
	}
	after, _ := store.Settings(ctx)
	if after.Reference != before.Reference || after.Snapshot.ThemeMode == "dark" || len(vault.values) != 1 {
		t.Fatal("racing save published or leaked credential")
	}
	vault.onSet = nil
	settings = conflict.Current
	settings.ThemeMode = "dark"
	saved, _, err := service.SaveSettings(ctx, settings, domain.CredentialUpdate{Action: domain.KeepCredential})
	if err != nil || saved.ThemeMode != "dark" {
		t.Fatal("current revision save failed", err)
	}
	runtime, err := store.RuntimeSettings(ctx)
	if err != nil || runtime.PythonSource != "system" {
		t.Fatal("public save replaced runtime source")
	}
	// Both Go and legacy recovery must protect an actively referenced token.
	intent := application.CredentialCleanup{ID: "synthetic-active-intent", Owner: "settings", Reference: after.Reference, Operation: "rollback"}
	if store.RememberCredential(ctx, intent) != nil || service.RecoverCredentials(ctx) != nil {
		t.Fatal("active cleanup failed")
	}
	if len(vault.values) != 1 {
		t.Fatal("active settings token deleted")
	}
	if _, _, err = service.SaveSettings(ctx, saved, domain.CredentialUpdate{Action: domain.ClearCredential}); err != nil || len(vault.values) != 0 {
		t.Fatal("explicit clear failed")
	}
}
func TestWriteResourceImportAndReadonlyBuiltins(t *testing.T) {
	ctx := context.Background()
	store, _ := fixtureStore(t)
	service := application.NewResources(store, &syntheticCredentials{})
	first, err := service.SaveMember(ctx, domain.MeetingMember{Name: " Alice ", Department: " Engineering ", SortOrder: 2, IsRecorder: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.SaveMember(ctx, domain.MeetingMember{Name: "Bob", SortOrder: 1, IsRecorder: true})
	if err != nil {
		t.Fatal(err)
	}
	values, _ := service.Members(ctx)
	if len(values) != 2 || values[0].ID != second.ID || values[1].IsRecorder || values[1].Department != "Engineering" {
		t.Fatal("member order/recorder differs")
	}
	result, err := service.ImportMembers(ctx, []domain.MeetingMember{{Name: "Alice", Department: "Updated", IsRecorder: true}, {Name: "Carol"}})
	if err != nil || result.Created != 1 || result.Updated != 1 {
		t.Fatal("import merge differs", err)
	}
	values, _ = service.Members(ctx)
	found := false
	for _, value := range values {
		if value.ID == first.ID {
			found = value.CreatedAt == first.CreatedAt && value.IsRecorder && value.Department == "Updated"
		}
	}
	if !found {
		t.Fatal("import lost existing identity")
	}
	if _, err := service.ImportMembers(ctx, []domain.MeetingMember{{Name: "Alice"}, {Name: " Alice "}}); err == nil {
		t.Fatal("duplicate import accepted")
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_member BEFORE INSERT ON meeting_members WHEN NEW.name='Rejected' BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ImportMembers(ctx, []domain.MeetingMember{{Name: "Alice", Department: "Should rollback"}, {Name: "Rejected"}}); err == nil {
		t.Fatal("partial import accepted")
	}
	values, _ = service.Members(ctx)
	for _, v := range values {
		if strings.Contains(v.Department, "rollback") {
			t.Fatal("partial import committed")
		}
	}
	if _, err := store.db.Exec(`INSERT INTO ai_summary_templates(id,name,description,prompt,include_speaker_by_default,include_timestamp_by_default,builtin,created_at,updated_at) VALUES('synthetic-builtin','Builtin','','Synthetic prompt',1,1,1,'2026-11-17T00:00:00Z','2026-11-17T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	templates, _ := service.Templates(ctx)
	builtin := ""
	for _, v := range templates {
		if v.Builtin {
			builtin = v.ID
			v.Builtin = false
			v.Name = "overwrite"
			if _, err := service.SaveTemplate(ctx, v); !errors.Is(err, domain.ErrBuiltinTemplate) {
				t.Fatal("builtin overwritten")
			}
			break
		}
	}
	if builtin == "" {
		t.Fatal("Rust fixture missing builtin template")
	}
	if !errors.Is(service.DeleteTemplate(ctx, builtin), domain.ErrBuiltinTemplate) {
		t.Fatal("builtin deleted")
	}
	custom, err := service.SaveTemplate(ctx, domain.SummaryTemplate{Name: "Custom", Prompt: "Test prompt", IncludeSpeaker: true, IncludeTimestamp: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.DeleteTemplate(ctx, custom.ID); err != nil {
		t.Fatal(err)
	}
}
func TestWriteStagedCredentialCrashCanRecover(t *testing.T) {
	ctx := context.Background()
	store, _ := fixtureStore(t)
	vault := &syntheticCredentials{}
	intent := application.CredentialCleanup{ID: "synthetic-crash-intent", Owner: "synthetic-model", Reference: "synthetic-crash-reference", Operation: "rollback"}
	if store.RememberCredential(ctx, intent) != nil || vault.Set(ctx, intent.Reference, domain.NewSecret("synthetic-orphan")) != nil {
		t.Fatal("stage failed")
	}
	if application.NewResources(store, vault).RecoverCredentials(ctx) != nil || len(vault.values) != 0 {
		t.Fatal("staged secret not recovered")
	}
}

func TestWriteEditorScopeCannotMutateOtherResources(t *testing.T) {
	ctx := context.Background()
	store, _ := fixtureStore(t)
	service := application.NewResources(store, &syntheticCredentials{})
	one, err := service.SaveMember(ctx, domain.MeetingMember{Name: "One"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := service.SaveMember(ctx, domain.MeetingMember{Name: "Two"})
	if err != nil {
		t.Fatal(err)
	}
	grant, draft, err := service.OpenEditor(ctx, application.Members, one.ID)
	if err != nil {
		t.Fatal(err)
	}
	forged := draft
	forged.Member = two
	if _, _, err := grant.Save(ctx, forged, domain.CredentialUpdate{}); !errors.Is(err, application.ErrWindowScope) {
		t.Fatal("editor changed another resource")
	}
	forged = draft
	forged.Kind = application.Models
	if _, _, err := grant.Save(ctx, forged, domain.CredentialUpdate{}); !errors.Is(err, application.ErrWindowScope) {
		t.Fatal("editor changed another domain")
	}
	grant.Close()
	if _, _, err := grant.Save(ctx, draft, domain.CredentialUpdate{}); !errors.Is(err, application.ErrWindowScope) {
		t.Fatal("revoked editor still writes")
	}
	service.Close()
	if _, err := service.SaveMember(ctx, one); !errors.Is(err, application.ErrWindowScope) {
		t.Fatal("closed profile still writes")
	}
}
