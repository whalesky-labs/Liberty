package schema

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"modernc.org/sqlite"
)

var ErrRestore = errors.New("database_restore_failed")

type RestoreResult struct {
	RestoredVersion int
	SafetyBackup    string
}

// RestoreLocked replaces a profile database through SQLite's online backup
// API only after both snapshots validate. The caller must hold the shared
// profile lock and stop legacy clients that do not honor that lock.
func RestoreLocked(ctx context.Context, database, backupName string, vault Credentials) (RestoreResult, error) {
	return restoreLocked(ctx, database, backupName, vault, writeRestoreCredentialJournal)
}

func restoreLocked(ctx context.Context, database, backupName string, vault Credentials, persistCredentials func(context.Context, string, []string) error) (RestoreResult, error) {
	var result RestoreResult
	if !filepath.IsAbs(database) || backupName == "" || filepath.Base(backupName) != backupName || strings.ContainsAny(backupName, `/\:`) || !strings.HasPrefix(backupName, filepath.Base(database)+".pre-") || !strings.HasSuffix(backupName, ".bak") {
		return result, ErrRestore
	}
	directory := filepath.Dir(database)
	backupPath := filepath.Join(directory, backupName)
	databaseInfo, err := os.Lstat(database)
	if err != nil || !databaseInfo.Mode().IsRegular() {
		return result, ErrRestore
	}
	backupInfo, err := os.Lstat(backupPath)
	if err != nil || !backupInfo.Mode().IsRegular() || backupInfo.Size() == 0 || os.SameFile(databaseInfo, backupInfo) {
		return result, ErrRestore
	}
	backupDB, err := sql.Open("sqlite", URI(backupPath, url.Values{"mode": {"ro"}}))
	if err != nil {
		return result, ErrRestore
	}
	backupConnection, err := backupDB.Conn(ctx)
	if err != nil {
		backupDB.Close()
		return result, ErrRestore
	}
	result.RestoredVersion, err = version(ctx, backupConnection)
	if err == nil {
		err = verifyRestoreCredentials(ctx, backupConnection, result.RestoredVersion, vault)
	}
	connectionErr := backupConnection.Close()
	databaseErr := backupDB.Close()
	if err != nil || connectionErr != nil || databaseErr != nil || result.RestoredVersion < 0 || result.RestoredVersion > CurrentVersion {
		return result, ErrRestore
	}
	if err := verifyBackup(ctx, backupPath, result.RestoredVersion); err != nil {
		return result, ErrRestore
	}
	currentDB, err := sql.Open("sqlite", URI(database, url.Values{"mode": {"rw"}, "_pragma": {"busy_timeout(5000)"}}))
	if err != nil {
		return result, ErrRestore
	}
	currentDB.SetMaxOpenConns(1)
	currentConnection, err := currentDB.Conn(ctx)
	if err != nil {
		currentDB.Close()
		return result, ErrRestore
	}
	currentVersion, err := version(ctx, currentConnection)
	if err != nil || currentVersion < 0 || currentVersion > CurrentVersion {
		currentConnection.Close()
		currentDB.Close()
		return result, ErrRestore
	}
	if _, err := readRestoreCredentialJournals(ctx, database); err != nil {
		currentConnection.Close()
		currentDB.Close()
		return result, ErrRestore
	}
	references, err := restoreCredentialReferences(ctx, currentConnection, currentVersion)
	if err != nil {
		currentConnection.Close()
		currentDB.Close()
		return result, ErrRestore
	}
	result.SafetyBackup, err = backup(ctx, currentConnection, database, currentVersion, "pre-restore")
	if err != nil {
		currentConnection.Close()
		currentDB.Close()
		return result, ErrRestore
	}
	if err := persistCredentials(ctx, database, references); err != nil {
		currentConnection.Close()
		currentDB.Close()
		return result, ErrRestore
	}
	err = currentConnection.Raw(func(raw any) error {
		driver, ok := raw.(interface {
			NewRestore(string) (*sqlite.Backup, error)
		})
		if !ok {
			return ErrRestore
		}
		restore, err := driver.NewRestore(URI(backupPath, url.Values{"mode": {"ro"}}))
		if err != nil {
			return ErrRestore
		}
		for {
			if ctx.Err() != nil {
				restore.Finish()
				return ErrRestore
			}
			more, err := restore.Step(256)
			if err != nil {
				restore.Finish()
				return ErrRestore
			}
			if !more {
				break
			}
		}
		if restore.Finish() != nil {
			return ErrRestore
		}
		return nil
	})
	var imported []restoreCredentialRecord
	if err == nil {
		imported, err = importRestoreCredentialJournals(ctx, currentConnection, database)
	}
	if err == nil && len(imported) == 0 {
		err = checkpointRestoreWAL(ctx, currentConnection)
	}
	connectionCloseErr := currentConnection.Close()
	databaseCloseErr := currentDB.Close()
	if err != nil || connectionCloseErr != nil || databaseCloseErr != nil {
		return result, ErrRestore
	}
	if err := verifyBackup(ctx, database, result.RestoredVersion); err != nil {
		return result, ErrRestore
	}
	if syncRestoredDatabase(database) != nil {
		return result, ErrRestore
	}
	if directory, err := os.Open(directory); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	if err := removeRestoreCredentialJournals(ctx, imported); err != nil {
		return result, ErrRestore
	}
	return result, nil
}

func verifyRestoreCredentials(ctx context.Context, connection *sql.Conn, storedVersion int, vault Credentials) error {
	references, err := snapshotCredentialReferences(ctx, connection, storedVersion)
	if err != nil {
		return ErrRestore
	}
	for _, reference := range references {
		if vault == nil {
			return ErrRestore
		}
		secret, err := vault.Get(ctx, reference)
		if err != nil || secret.Empty() {
			return ErrRestore
		}
	}
	return nil
}
