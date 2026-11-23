package schema

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"

	"liberty.local/liberty/internal/domain"
)

const journalKey = "native_credential_migration_v3"

type credentialEntry struct {
	Reference string `json:"reference"`
	Previous  string `json:"previous"`
}
type credentialJournal struct {
	Published bool              `json:"published"`
	Entries   []credentialEntry `json:"entries"`
}
type plannedCredential struct {
	credentialEntry
	modelID  string
	settings bool
	raw      string // exact original value for CAS; never serialized or logged
}

func migrationReference() string {
	var bytes [16]byte
	rand.Read(bytes[:])
	return "liberty:migration:" + hex.EncodeToString(bytes[:])
}

func credentialPlan(ctx context.Context, c *sql.Conn) ([]plannedCredential, error) {
	var entries []plannedCredential
	var token, reference string
	err := c.QueryRowContext(ctx, "SELECT api_token,COALESCE(api_token_ref,'') FROM app_settings WHERE id=1").Scan(&token, &reference)
	if err != nil && err != sql.ErrNoRows {
		return nil, ErrMigration
	}
	if strings.TrimSpace(token) != "" {
		entries = append(entries, plannedCredential{credentialEntry: credentialEntry{migrationReference(), reference}, settings: true, raw: token})
	}
	rows, err := c.QueryContext(ctx, "SELECT id,api_key,COALESCE(api_key_ref,'') FROM ai_model_configs ORDER BY id")
	if err != nil {
		return nil, ErrMigration
	}
	defer rows.Close()
	for rows.Next() {
		var entry plannedCredential
		if rows.Scan(&entry.modelID, &entry.raw, &entry.Previous) != nil {
			return nil, ErrMigration
		}
		if strings.TrimSpace(entry.raw) == "" {
			continue
		}
		entry.Reference = migrationReference()
		entries = append(entries, entry)
	}
	if rows.Err() != nil {
		return nil, ErrMigration
	}
	return entries, nil
}

func saveJournal(ctx context.Context, c *sql.Conn, j credentialJournal) error {
	data, err := json.Marshal(j)
	if err != nil {
		return ErrMigration
	}
	_, err = c.ExecContext(ctx, "INSERT INTO app_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", journalKey, string(data))
	if err != nil {
		return ErrMigration
	}
	return nil
}

func readCredentialJournal(ctx context.Context, connection *sql.Conn) (*credentialJournal, error) {
	var text string
	err := connection.QueryRowContext(ctx, "SELECT value FROM app_meta WHERE key=?", journalKey).Scan(&text)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, ErrMigration
	}
	var journal credentialJournal
	if json.Unmarshal([]byte(text), &journal) != nil {
		return nil, ErrMigration
	}
	return &journal, nil
}

func migrateCredentials(ctx context.Context, c *sql.Conn, vault Credentials) error {
	var plan []plannedCredential
	j := credentialJournal{Entries: []credentialEntry{}}
	// Durable intent precedes every system-store write, including writes whose
	// result is unknown after a crash. This works before v10's cleanup table.
	err := transaction(ctx, c, func() error {
		v, err := version(ctx, c)
		if err != nil || v != 2 {
			return ErrMigration
		}
		plan, err = credentialPlan(ctx, c)
		if err != nil {
			return err
		}
		previous, err := readCredentialJournal(ctx, c)
		if err != nil {
			return err
		}
		if previous != nil {
			j = *previous
		}
		if j.Entries == nil {
			j.Entries = []credentialEntry{}
		}
		for _, p := range plan {
			j.Entries = append(j.Entries, p.credentialEntry)
		}
		return saveJournal(ctx, c, j)
	})
	if err != nil {
		return err
	}
	for _, p := range plan {
		if vault == nil {
			return domain.ErrCredentialStore
		}
		secret := domain.NewSecret(p.raw)
		if vault.Set(ctx, p.Reference, secret) != nil {
			return domain.ErrCredentialStore
		}
		stored, err := vault.Get(ctx, p.Reference)
		if err != nil || stored.Reveal() != secret.Reveal() {
			return domain.ErrCredentialStore
		}
	}
	err = transaction(ctx, c, func() error {
		v, err := version(ctx, c)
		if err != nil || v != 2 {
			return ErrMigration
		}
		for _, p := range plan {
			var result sql.Result
			if p.settings {
				result, err = c.ExecContext(ctx, "UPDATE app_settings SET api_token='',api_token_ref=? WHERE id=1 AND api_token=? AND COALESCE(api_token_ref,'')=?", p.Reference, p.raw, p.Previous)
			} else {
				result, err = c.ExecContext(ctx, "UPDATE ai_model_configs SET api_key='',api_key_ref=? WHERE id=? AND api_key=? AND COALESCE(api_key_ref,'')=?", p.Reference, p.modelID, p.raw, p.Previous)
			}
			if err != nil {
				return ErrMigration
			}
			count, err := result.RowsAffected()
			if err != nil || count != 1 {
				return ErrMigration
			}
		}
		j.Published = true
		if err := saveJournal(ctx, c, j); err != nil {
			return err
		}
		return recordVersion(ctx, c, 3)
	})
	// Unknown COMMIT outcomes leave the journal intact. The next open reads
	// its committed phase before deciding which references may be deleted.
	if err != nil {
		return err
	}
	return recoverCredentials(ctx, c, vault)
}

func recoverCredentials(ctx context.Context, c *sql.Conn, vault Credentials) error {
	j, err := readCredentialJournal(ctx, c)
	if err != nil || j == nil {
		return err
	}
	var database string
	if c.QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name='main'").Scan(&database) != nil || !filepath.IsAbs(database) {
		return ErrMigration
	}
	retained := false
	for _, entry := range j.Entries {
		refs := []string{entry.Reference}
		if j.Published {
			refs = append(refs, entry.Previous)
		}
		for _, ref := range refs {
			if ref == "" {
				continue
			}
			var active int
			if c.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM ai_model_configs WHERE api_key_ref=?) + (SELECT COUNT(*) FROM app_settings WHERE api_token_ref=?)`, ref, ref).Scan(&active) != nil {
				return ErrMigration
			}
			if active > 0 {
				continue
			}
			inBackup, err := BackupCredentialReferenced(ctx, database, ref)
			if err != nil {
				return err
			}
			if inBackup {
				retained = true
				continue
			}
			if vault == nil || vault.Delete(ctx, ref) != nil {
				return domain.ErrCredentialCleanup
			}
		}
	}
	if retained {
		return nil
	}
	_, err = c.ExecContext(ctx, "DELETE FROM app_meta WHERE key=?", journalKey)
	if err != nil {
		return ErrMigration
	}
	return nil
}
