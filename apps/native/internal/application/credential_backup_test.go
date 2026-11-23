package application

import (
	"context"
	"errors"
	"testing"

	"liberty.local/liberty/internal/domain"
)

type credentialBackupRepository struct {
	ResourceRepository
	live, retained            bool
	backupError               error
	backupReads, done, failed int
}

func (repository *credentialBackupRepository) PendingCredentials(context.Context) ([]CredentialCleanup, error) {
	return []CredentialCleanup{{ID: "synthetic-intent", Owner: "synthetic-owner", Reference: "synthetic-reference", Operation: "retire"}}, nil
}

func (repository *credentialBackupRepository) CredentialReferenced(context.Context, string) (bool, error) {
	return repository.live, nil
}

func (repository *credentialBackupRepository) BackupCredentialReferenced(context.Context, string) (bool, error) {
	repository.backupReads++
	return repository.retained, repository.backupError
}

func (repository *credentialBackupRepository) FinishCredential(context.Context, string) error {
	repository.done++
	return nil
}

func (repository *credentialBackupRepository) FailCredential(context.Context, string) error {
	repository.failed++
	return nil
}

type credentialBackupVault struct {
	Credentials
	deletes int
	err     error
}

func (vault *credentialBackupVault) Delete(context.Context, string) error {
	vault.deletes++
	return vault.err
}

func TestCredentialRecoverySeparatesLiveBackupAndFailedRetention(t *testing.T) {
	backupError := errors.New("synthetic-backup-unavailable")
	for _, test := range []struct {
		name                               string
		live, retained                     bool
		backupError, vaultError, wantError error
		backupReads, deleted, done, failed int
	}{
		{name: "live", live: true, backupError: backupError, done: 1},
		{name: "retained", retained: true, backupReads: 1},
		{name: "orphan", backupReads: 1, deleted: 1, done: 1},
		{name: "unreadable-backup", backupError: backupError, wantError: backupError, backupReads: 1},
		{name: "delete-failure", vaultError: domain.ErrCredentialStore, wantError: domain.ErrCredentialCleanup, backupReads: 1, deleted: 1, failed: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &credentialBackupRepository{live: test.live, retained: test.retained, backupError: test.backupError}
			vault := &credentialBackupVault{err: test.vaultError}
			service := NewResources(repository, vault)
			if err := service.RecoverCredentials(context.Background()); !errors.Is(err, test.wantError) {
				t.Fatal("credential recovery misreported retention", err)
			}
			if repository.backupReads != test.backupReads || vault.deletes != test.deleted || repository.done != test.done || repository.failed != test.failed {
				t.Fatal("credential recovery discarded a required secret or intent", repository.backupReads, vault.deletes, repository.done, repository.failed)
			}
		})
	}
}
