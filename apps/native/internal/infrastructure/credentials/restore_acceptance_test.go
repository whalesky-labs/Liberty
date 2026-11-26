//go:build credentials_acceptance && (darwin || windows)

package credentials

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
	"liberty.local/liberty/internal/infrastructure/repositories/schema"
)

type restoreAcceptanceVault struct {
	system     *System
	references map[string]struct{}
}

func (vault *restoreAcceptanceVault) Get(ctx context.Context, reference string) (domain.Secret, error) {
	return vault.system.Get(ctx, reference)
}

func (vault *restoreAcceptanceVault) Set(ctx context.Context, reference string, secret domain.Secret) error {
	vault.references[reference] = struct{}{}
	return vault.system.Set(ctx, reference, secret)
}

func (vault *restoreAcceptanceVault) Delete(ctx context.Context, reference string) error {
	return vault.system.Delete(ctx, reference)
}

func TestIsolatedSystemCredentialsSurviveRotationRestoreAndReclamation(test *testing.T) {
	ctx := context.Background()
	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		test.Fatal("cannot create isolated acceptance identity")
	}
	vault := &restoreAcceptanceVault{system: &System{service: "LibertyNativeRecoveryAcceptance-" + hex.EncodeToString(identity[:])}, references: map[string]struct{}{}}
	test.Cleanup(func() {
		for reference := range vault.references {
			if vault.system.Delete(context.Background(), reference) != nil {
				test.Error("isolated recovery credential cleanup failed")
			}
		}
	})
	database := filepath.Join(test.TempDir(), "synthetic #资料.sqlite3")
	store, prepared, err := jobdb.OpenPrepared(ctx, database, vault, true)
	if err != nil {
		test.Fatal("isolated database preparation failed", err)
	}
	test.Cleanup(func() { store.Close() })
	resources := application.NewResources(store, vault)
	test.Cleanup(resources.Close)
	modelSecret := domain.NewSecret("synthetic-model-before-测试")
	tokenSecret := domain.NewSecret("synthetic-token-before-测试")
	model, outcome, err := resources.SaveModel(ctx, domain.AIModel{ID: "synthetic-recovery-model", Name: "Before restore", BaseURL: "https://example.invalid/v1", Model: "synthetic", Enabled: true, IsDefault: true}, domain.CredentialUpdate{Action: domain.SetCredential, Value: modelSecret})
	if err != nil || outcome.CleanupPending {
		test.Fatal("initial model credential publication failed", err)
	}
	settings, err := resources.Settings(ctx)
	if err != nil {
		test.Fatal("initial settings read failed", err)
	}
	_, outcome, err = resources.SaveSettings(ctx, settings, domain.CredentialUpdate{Action: domain.SetCredential, Value: tokenSecret})
	if err != nil || outcome.CleanupPending {
		test.Fatal("initial remote token publication failed", err)
	}
	previous := acceptanceReferences(test, store)
	resources.Close()
	if err := store.Close(); err != nil {
		test.Fatal("initial store close failed", err)
	}

	handover, takeover, err := jobdb.OpenHandover(ctx, database, vault, false)
	if err != nil || takeover.Backup == "" {
		test.Fatal("isolated handover did not preserve a recovery point", err)
	}
	test.Cleanup(func() { handover.Close() })
	rotating := application.NewResources(handover, vault)
	test.Cleanup(rotating.Close)
	model.Name = "After rotation"
	_, outcome, err = rotating.SaveModel(ctx, model, domain.CredentialUpdate{Action: domain.SetCredential, Value: domain.NewSecret("synthetic-model-after-测试")})
	if err != nil || outcome.CleanupPending {
		test.Fatal("model rotation failed or misreported snapshot retention", err)
	}
	settings, err = rotating.Settings(ctx)
	if err != nil {
		test.Fatal("settings read before token rotation failed", err)
	}
	_, outcome, err = rotating.SaveSettings(ctx, settings, domain.CredentialUpdate{Action: domain.SetCredential, Value: domain.NewSecret("synthetic-token-after-测试")})
	if err != nil || outcome.CleanupPending {
		test.Fatal("token rotation failed or misreported snapshot retention", err)
	}
	current := acceptanceReferences(test, handover)
	if current == previous || current[0] == previous[0] || current[1] == previous[1] {
		test.Fatal("credential rotation reused a published reference")
	}
	for _, reference := range append(previous[:], current[:]...) {
		if secret, err := vault.Get(ctx, reference); err != nil || secret.Empty() {
			test.Fatal("rotation lost a live or recovery-point credential", err)
		}
	}
	rotating.Close()
	if err := handover.Close(); err != nil {
		test.Fatal("rotated store close failed", err)
	}

	owner, err := profile.Acquire(filepath.Dir(database))
	if err != nil {
		test.Fatal("isolated restore ownership failed", err)
	}
	test.Cleanup(func() { owner.Close() })
	canonicalDatabase := filepath.Join(owner.Directory(), filepath.Base(database))
	restored, restoreErr := schema.RestoreLocked(ctx, canonicalDatabase, filepath.Base(takeover.Backup), vault)
	closeErr := owner.Close()
	if restoreErr != nil || closeErr != nil || restored.RestoredVersion != prepared.Version || restored.SafetyBackup == "" {
		test.Fatalf("isolated system-credential restore failed: restored version %d, expected %d, restore error %v, close error %v", restored.RestoredVersion, prepared.Version, restoreErr, closeErr)
	}
	reopened, err := jobdb.Open(ctx, database)
	if err != nil {
		test.Fatal("restored profile did not reopen", err)
	}
	test.Cleanup(func() { reopened.Close() })
	recovering := application.NewResources(reopened, vault)
	test.Cleanup(recovering.Close)
	if err := recovering.RecoverCredentials(ctx); err != nil {
		test.Fatal("pre-restore safety retention failed", err)
	}
	if acceptanceReferences(test, reopened) != previous {
		test.Fatal("restore did not reinstate both original credential references")
	}
	for index, expected := range []domain.Secret{modelSecret, tokenSecret} {
		secret, err := vault.Get(ctx, previous[index])
		if err != nil || secret != expected {
			test.Fatal("restored credential does not resolve to the original value", err)
		}
	}
	pending, err := reopened.PendingCredentials(ctx)
	if err != nil || len(pending) != 2 {
		test.Fatal("restore lost retirement intents for the newer credentials", err)
	}
	for _, intent := range pending {
		if intent.Reference != current[0] && intent.Reference != current[1] {
			test.Fatal("restore retired an unrelated or live credential")
		}
		if secret, err := vault.Get(ctx, intent.Reference); err != nil || secret.Empty() {
			test.Fatal("pre-restore safety snapshot lost a required credential", err)
		}
	}
	if err := os.Remove(restored.SafetyBackup); err != nil {
		test.Fatal("synthetic safety snapshot removal failed", err)
	}
	if err := recovering.RecoverCredentials(ctx); err != nil {
		test.Fatal("retirement after safety-snapshot removal failed", err)
	}
	pending, err = reopened.PendingCredentials(ctx)
	if err != nil || len(pending) != 0 {
		test.Fatal("completed retirement intents remain", err)
	}
	for _, reference := range current {
		if secret, err := vault.Get(ctx, reference); err != nil || !secret.Empty() {
			test.Fatal("unreferenced post-recovery credential was not reclaimed", err)
		}
	}
	models, err := recovering.Models(ctx)
	if err != nil || len(models) != 1 || models[0].Name != "Before restore" || !models[0].CredentialPresent {
		test.Fatal("credential reclamation changed the restored model", err)
	}
	settings, err = recovering.Settings(ctx)
	if err != nil || !settings.TokenConfigured {
		test.Fatal("credential reclamation removed the restored remote token", err)
	}
	if err := recovering.RecoverCredentials(ctx); err != nil {
		test.Fatal("completed credential retirement was not idempotent", err)
	}
	test.Log("isolated system store verified model/token rotation, SQLite restore, safety-snapshot retention and post-snapshot reclamation")
}

func acceptanceReferences(test *testing.T, store *jobdb.Store) [2]string {
	test.Helper()
	model, modelErr := store.Model(context.Background(), "synthetic-recovery-model")
	settings, settingsErr := store.Settings(context.Background())
	if modelErr != nil || settingsErr != nil || model.Reference == "" || settings.Reference == "" {
		test.Fatal("missing synthetic credential references", modelErr, settingsErr)
	}
	return [2]string{model.Reference, settings.Reference}
}
