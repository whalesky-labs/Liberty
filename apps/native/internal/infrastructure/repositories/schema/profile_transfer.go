package schema

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"os"

	"liberty.local/liberty/internal/domain"
	"modernc.org/sqlite"
)

var ErrProfileTransfer = errors.New("profile_snapshot_import_failed")
var ErrProfileTransferBusy = errors.New("profile_snapshot_has_active_operations")

// Process ownership and deletion compensation cannot be transferred by copying
// their SQLite rows: the source executable may still own those processes and
// files. Take a quiescent committed snapshot rather than killing or resuming
// legacy work from the new profile.
func profileSnapshotQuiescent(ctx context.Context, connection *sql.Conn) error {
	for _, check := range []struct {
		table, query string
		violation    error
	}{
		{"jobs", "SELECT COUNT(*) FROM jobs WHERE overall_status IN ('queued','transcribing','speaker_processing')", ErrProfileTransferBusy},
		{"job_runs", "SELECT COUNT(*) FROM job_runs WHERE status IN ('queued','running')", ErrProfileTransferBusy},
		{"job_deletion_ops", "SELECT COUNT(*) FROM job_deletion_ops", ErrProfileTransferBusy},
		{"ai_summary_runs", "SELECT COUNT(*) FROM ai_summary_runs WHERE status='running'", ErrProfileTransferBusy},
		// Keep these five active kinds aligned with domain.RuntimeOperation.Active.
		// Failed operations are terminal; unfamiliar values remain invalid.
		{"runtime_component_state", "SELECT COUNT(*) FROM runtime_component_state WHERE operation_kind IN ('detecting','waiting_for_python','downloading','installing','validating')", ErrProfileTransferBusy},
		{"runtime_component_state", "SELECT COUNT(*) FROM runtime_component_state WHERE operation_kind NOT IN ('idle','failed','detecting','waiting_for_python','downloading','installing','validating')", ErrProfileTransfer},
		{"runtime_state", "SELECT COUNT(*) FROM runtime_state WHERE status IN ('detecting','waiting_for_python','downloading','installing','validating')", ErrProfileTransferBusy},
	} {
		var exists, active int
		if connection.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", check.table).Scan(&exists) != nil {
			return ErrProfileTransfer
		}
		if exists == 0 {
			continue
		}
		if connection.QueryRowContext(ctx, check.query).Scan(&active) != nil {
			return ErrProfileTransfer
		}
		if active != 0 {
			return check.violation
		}
	}
	return nil
}

// CopyProfileSnapshot reads a live source through SQLite, including committed
// WAL pages. It never acquires a fictional ownership lock over legacy clients
// and never changes the source schema, business rows or credential references.
// The caller owns a new, private destination file and its profile lock.
func CopyProfileSnapshot(ctx context.Context, source, destination string) (int, error) {
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() {
		return 0, ErrProfileTransfer
	}
	db, err := sql.Open("sqlite", URI(source, url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "trusted_schema(0)", "busy_timeout(5000)"}}))
	if err != nil {
		return 0, ErrProfileTransfer
	}
	defer db.Close()
	connection, err := db.Conn(ctx)
	if err != nil {
		return 0, ErrProfileTransfer
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, "BEGIN"); err != nil {
		return 0, ErrProfileTransfer
	}
	defer connection.ExecContext(context.Background(), "ROLLBACK")
	storedVersion, err := version(ctx, connection)
	if err != nil {
		return 0, ErrProfileTransfer
	}
	if err := profileSnapshotQuiescent(ctx, connection); err != nil {
		return 0, err
	}
	err = connection.Raw(func(raw any) error {
		driver, ok := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return ErrProfileTransfer
		}
		copy, err := driver.NewBackup(URI(destination, url.Values{"mode": {"rw"}}))
		if err != nil {
			return ErrProfileTransfer
		}
		for {
			if ctx.Err() != nil {
				copy.Finish()
				return ErrProfileTransfer
			}
			more, err := copy.Step(256)
			if err != nil {
				copy.Finish()
				return ErrProfileTransfer
			}
			if !more {
				break
			}
		}
		return copy.Finish()
	})
	if err != nil || verifyBackup(ctx, destination, storedVersion) != nil {
		return 0, ErrProfileTransfer
	}
	return storedVersion, nil
}

type transferVault struct {
	Credentials
	beforeWrite func(string) error
	aliases     map[string]string
}

func (v transferVault) Set(ctx context.Context, reference string, secret domain.Secret) error {
	// The v3 executor chooses legacy migration account names. Translate its
	// writes to import-owned accounts without changing that executor or ever
	// owning/deleting a source account with the same legacy prefix.
	if alias, exists := v.aliases[reference]; exists {
		return v.Credentials.Set(ctx, alias, secret)
	}
	alias, err := v.clone(ctx, secret)
	if err == nil {
		v.aliases[reference] = alias
	}
	return err
}

func (v transferVault) clone(ctx context.Context, secret domain.Secret) (string, error) {
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil || v.Credentials == nil {
		return "", ErrProfileTransfer
	}
	reference := "liberty:native-import:" + hex.EncodeToString(random[:])
	previous, err := v.Credentials.Get(ctx, reference)
	if err != nil || !previous.Empty() || v.beforeWrite(reference) != nil {
		return "", ErrProfileTransfer
	}
	if v.Credentials.Set(ctx, reference, secret) != nil {
		return "", ErrProfileTransfer
	}
	stored, err := v.Credentials.Get(ctx, reference)
	if err != nil || stored.Reveal() != secret.Reveal() {
		return "", ErrProfileTransfer
	}
	return reference, nil
}

func (v transferVault) Get(ctx context.Context, reference string) (domain.Secret, error) {
	if v.Credentials == nil {
		return domain.Secret{}, ErrProfileTransfer
	}
	if alias, exists := v.aliases[reference]; exists {
		reference = alias
	}
	return v.Credentials.Get(ctx, reference)
}

// Source cleanup intents belong to the source profile. Even the existing v3
// migration executor must never delete their references during an import.
func (v transferVault) Delete(context.Context, string) error { return nil }

// PrepareTransferredProfile upgrades only the private snapshot using the normal
// executor, then copies active secrets to fresh account names. System service
// identity stays compatible with the host's vault, while account names are
// disjoint from every source reference. beforeWrite durably records ownership
// BEFORE each possibly ambiguous OS-store write, including migration writes.
// It returns only newly owned live references for publication/compensation.
func PrepareTransferredProfile(ctx context.Context, database string, vault Credentials, beforeWrite func(string) error) ([]string, error) {
	if beforeWrite == nil {
		return nil, ErrProfileTransfer
	}
	db, err := sql.Open("sqlite", URI(database, url.Values{"mode": {"rw"}, "_pragma": {"busy_timeout(5000)", "synchronous(FULL)", "trusted_schema(0)", "secure_delete(1)"}}))
	if err != nil {
		return nil, ErrProfileTransfer
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	connection, err := db.Conn(ctx)
	if err != nil {
		return nil, ErrProfileTransfer
	}
	v, err := version(ctx, connection)
	if err == nil && v > 0 {
		_, err = connection.ExecContext(ctx, "DELETE FROM app_meta WHERE key IN (?,?)", handoverBackupKey, journalKey)
	}
	if err == nil && v >= 10 {
		_, err = connection.ExecContext(ctx, "DELETE FROM credential_cleanup_intents")
	}
	connection.Close()
	if err != nil {
		return nil, ErrProfileTransfer
	}
	protected := transferVault{Credentials: vault, beforeWrite: beforeWrite, aliases: make(map[string]string)}
	if _, err := Apply(ctx, db, database, protected); err != nil {
		return nil, ErrProfileTransfer
	}
	connection, err = db.Conn(ctx)
	if err != nil {
		return nil, ErrProfileTransfer
	}
	defer connection.Close()
	references, err := snapshotCredentialReferences(ctx, connection, CurrentVersion)
	if err != nil {
		return nil, ErrProfileTransfer
	}
	cloned := make(map[string]string)
	var live []string
	for _, original := range references {
		if _, exists := cloned[original]; exists {
			continue
		}
		secret, err := protected.Get(ctx, original)
		if err != nil || secret.Empty() {
			return nil, ErrProfileTransfer
		}
		reference, err := protected.clone(ctx, secret)
		if err != nil {
			return nil, ErrProfileTransfer
		}
		cloned[original] = reference
		live = append(live, reference)
	}
	err = transaction(ctx, connection, func() error {
		for original, reference := range cloned {
			if _, err := connection.ExecContext(ctx, "UPDATE ai_model_configs SET api_key_ref=? WHERE api_key_ref=?", reference, original); err != nil {
				return ErrProfileTransfer
			}
			if _, err := connection.ExecContext(ctx, "UPDATE app_settings SET api_token_ref=? WHERE api_token_ref=?", reference, original); err != nil {
				return ErrProfileTransfer
			}
		}
		_, err := connection.ExecContext(ctx, "DELETE FROM app_meta WHERE key IN (?,?)", handoverBackupKey, journalKey)
		return err
	})
	if err != nil || checkpointRestoreWAL(ctx, connection) != nil {
		return nil, ErrProfileTransfer
	}
	return live, nil
}

// VerifyTransferredProfile is also used by idempotent retries: a successful
// process exit alone is insufficient evidence of a usable, rebound snapshot.
func VerifyTransferredProfile(ctx context.Context, database string, vault Credentials) error {
	if verifyBackup(ctx, database, CurrentVersion) != nil {
		return ErrProfileTransfer
	}
	db, err := sql.Open("sqlite", URI(database, url.Values{"mode": {"ro"}}))
	if err != nil {
		return ErrProfileTransfer
	}
	defer db.Close()
	connection, err := db.Conn(ctx)
	if err != nil {
		return ErrProfileTransfer
	}
	defer connection.Close()
	references, err := snapshotCredentialReferences(ctx, connection, CurrentVersion)
	if err != nil {
		return ErrProfileTransfer
	}
	for _, reference := range references {
		if vault == nil {
			return ErrProfileTransfer
		}
		secret, err := vault.Get(ctx, reference)
		if err != nil || secret.Empty() {
			return ErrProfileTransfer
		}
	}
	return nil
}
