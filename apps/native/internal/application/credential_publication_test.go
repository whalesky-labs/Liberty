package application_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
)

type publicationVault struct {
	values     map[string]domain.Secret
	fault      string
	staged     string
	failDelete bool
	cancel     context.CancelFunc
	beforeSet  func(context.Context, string) error
}

func (vault *publicationVault) Get(ctx context.Context, reference string) (domain.Secret, error) {
	if err := ctx.Err(); err != nil {
		return domain.Secret{}, err
	}
	if reference == vault.staged && vault.fault == "read-failure" {
		return domain.Secret{}, domain.ErrCredentialStore
	}
	return vault.values[reference], nil
}

func (vault *publicationVault) Set(ctx context.Context, reference string, value domain.Secret) error {
	if err := vault.beforeSet(ctx, reference); err != nil {
		return err
	}
	vault.staged = reference
	switch vault.fault {
	case "missing":
		return nil
	case "changed":
		value = domain.NewSecret("synthetic-changed-value")
	}
	vault.values[reference] = value
	if vault.fault == "canceled" {
		vault.cancel()
	}
	return nil
}

func (vault *publicationVault) Delete(ctx context.Context, reference string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if vault.failDelete {
		return domain.ErrCredentialStore
	}
	delete(vault.values, reference)
	return nil
}

func TestCredentialPublicationRejectsUnverifiedWritesAndRecoversAfterReopen(t *testing.T) {
	for _, kind := range []string{"model", "settings"} {
		for _, fault := range []string{"missing", "changed", "read-failure", "canceled"} {
			for _, deferred := range []bool{false, true} {
				name := kind + "/" + fault + "/immediate-cleanup"
				if deferred {
					name = kind + "/" + fault + "/deferred-cleanup"
				}
				t.Run(name, func(t *testing.T) {
					ctx := context.Background()
					vault := &publicationVault{values: make(map[string]domain.Secret)}
					database := filepath.Join(t.TempDir(), "liberty.sqlite3")
					store, _, err := jobdb.OpenPrepared(ctx, database, vault, true)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if store != nil {
							_ = store.Close()
						}
					})
					vault.beforeSet = func(ctx context.Context, reference string) error {
						pending, err := store.PendingCredentials(ctx)
						if err != nil {
							return err
						}
						for _, intent := range pending {
							if intent.Reference == reference && intent.Operation == "rollback" {
								return nil
							}
						}
						return errors.New("synthetic-write-without-durable-rollback")
					}
					notifications := 0
					service := application.NewResources(store, vault, func(domain.Settings) { notifications++ })
					initial := domain.NewSecret("synthetic-original-value")
					update := domain.CredentialUpdate{Action: domain.SetCredential, Value: initial}
					var beforeModel application.ModelRecord
					var beforeSettings application.SettingsRecord
					if kind == "model" {
						value := domain.AIModel{ID: "synthetic-publication-model", Name: "Original model", BaseURL: "https://example.test/v1", Model: "synthetic", Enabled: true, IsDefault: true}
						if _, _, err = service.SaveModel(ctx, value, update); err == nil {
							beforeModel, err = store.Model(ctx, value.ID)
						}
					} else {
						var settings domain.Settings
						settings, err = service.Settings(ctx)
						if err == nil {
							_, _, err = service.SaveSettings(ctx, settings, update)
						}
						if err == nil {
							beforeSettings, err = store.Settings(ctx)
						}
					}
					if err != nil {
						t.Fatal("initial credential save failed", err)
					}
					priorNotifications := notifications
					failedContext, cancel := context.WithCancel(ctx)
					defer cancel()
					vault.fault, vault.failDelete, vault.cancel = fault, deferred, cancel
					update.Value = domain.NewSecret("synthetic-replacement-value")
					if kind == "model" {
						draft := beforeModel.Metadata
						draft.Name = "Unverified replacement"
						_, _, err = service.SaveModel(failedContext, draft, update)
					} else {
						draft := beforeSettings.Snapshot
						draft.ThemeMode = "dark"
						_, _, err = service.SaveSettings(failedContext, draft, update)
					}
					wantError := domain.ErrCredentialStore
					if fault == "canceled" {
						wantError = context.Canceled
					}
					if !errors.Is(err, wantError) {
						t.Fatal("unverified credential save did not report the expected failure", err)
					}
					if notifications != priorNotifications {
						t.Fatal("unverified settings were published to subscribers")
					}
					service.Close()
					if err := store.Close(); err != nil {
						t.Fatal(err)
					}
					store, err = jobdb.Open(ctx, database)
					if err != nil {
						t.Fatal(err)
					}
					previous := beforeSettings.Reference
					if kind == "model" {
						after, err := store.Model(ctx, beforeModel.Metadata.ID)
						if err != nil || after != beforeModel {
							t.Fatal("failed credential rotation changed the saved model", err)
						}
						previous = beforeModel.Reference
					} else {
						after, err := store.Settings(ctx)
						if err != nil || after != beforeSettings {
							t.Fatal("failed credential rotation changed saved settings or revision", err)
						}
					}
					if vault.values[previous] != initial {
						t.Fatal("failed credential rotation retired the active secret")
					}
					wantPending := 0
					if deferred {
						wantPending = 1
					}
					pending, err := store.PendingCredentials(ctx)
					if err != nil || len(pending) != wantPending {
						t.Fatal("failed credential cleanup did not retain its durable intent", err)
					}
					if deferred && (pending[0].Reference != vault.staged || pending[0].Operation != "rollback") {
						t.Fatal("retry intent targets the active or an unrelated secret")
					}
					vault.fault, vault.failDelete = "", false
					recovered := application.NewResources(store, vault)
					defer recovered.Close()
					for range 2 {
						if err := recovered.RecoverCredentials(ctx); err != nil {
							t.Fatal("credential cleanup could not retry after reopen", err)
						}
					}
					pending, err = store.PendingCredentials(ctx)
					if err != nil || len(pending) != 0 || len(vault.values) != 1 || vault.values[previous] != initial {
						t.Fatal("credential recovery removed a live secret or left an untracked staged secret", err)
					}
				})
			}
		}
	}
}
