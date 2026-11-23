package schema

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"liberty.local/liberty/internal/domain"
)

type memoryVault struct {
	values                   map[string]domain.Secret
	writes, deletes          int
	failWriteAt              int
	failDelete, corruptWrite bool
	onWrite                  func()
}

func (v *memoryVault) Get(_ context.Context, key string) (domain.Secret, error) {
	return v.values[key], nil
}
func (v *memoryVault) Set(_ context.Context, key string, value domain.Secret) error {
	v.writes++
	if v.values == nil {
		v.values = map[string]domain.Secret{}
	}
	// Emulate an uncertain result: the OS accepted the write before returning
	// an error. The already persisted journal must still be able to remove it.
	v.values[key] = value
	if v.corruptWrite {
		v.values[key] = domain.NewSecret("synthetic-corruption")
	}
	if v.onWrite != nil {
		v.onWrite()
	}
	if v.writes == v.failWriteAt {
		return domain.ErrCredentialStore
	}
	return nil
}
func (v *memoryVault) Delete(_ context.Context, key string) error {
	v.deletes++
	if v.failDelete {
		return domain.ErrCredentialStore
	}
	delete(v.values, key)
	return nil
}
func database(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "synthetic #资料.sqlite3")
	db, err := sql.Open("sqlite", URI(path, url.Values{"mode": {"rwc"}}))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db, path
}
func exec(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.Exec(statement, args...); err != nil {
		t.Fatal(err)
	}
}
func scalar(t *testing.T, db *sql.DB, statement string, args ...any) string {
	t.Helper()
	var value string
	if err := db.QueryRow(statement, args...).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
func v2(t *testing.T, db *sql.DB) {
	t.Helper()
	c, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, v := range []int{1, 2} {
		if err := transaction(context.Background(), c, func() error {
			if e := applyStep(context.Background(), c, v); e != nil {
				return e
			}
			return recordVersion(context.Background(), c, v)
		}); err != nil {
			t.Fatal(err)
		}
	}
	if c.Close() != nil {
		t.Fatal("fixture connection close")
	}
	exec(t, db, `INSERT INTO app_settings(id,theme_mode,liquid_glass_style,accent_color,locale,backend_url,api_token,api_token_ref,default_hotwords,summary_template,python_path,runner_script_path)
 VALUES(1,'auto','transparent','#2f6dff','zh-CN','',' synthetic-token ','old-shared','','','','');
 INSERT INTO ai_model_configs(id,name,base_url,api_key,api_key_ref,model,created_at,updated_at)
 VALUES('model','Synthetic','https://example.test',' synthetic-key ','old-model','test','fixture','fixture'),
 ('other','Synthetic shared','https://example.test','','old-shared','test','fixture','fixture');`)
}

func TestFreshDatabaseAndRepeatKeepCustomData(t *testing.T) {
	db, path := database(t)
	ctx := context.Background()
	r, err := Apply(ctx, db, path, nil)
	if err != nil || r.FromVersion != 0 || r.Version != 10 || r.Backup != "" {
		t.Fatal(r, err)
	}
	if scalar(t, db, "PRAGMA integrity_check") != "ok" || scalar(t, db, "SELECT COUNT(*) FROM ai_summary_templates WHERE builtin=1") != "5" {
		t.Fatal("invalid fresh database")
	}
	exec(t, db, `INSERT INTO meeting_members(id,name,department,created_at,updated_at) VALUES('member','合成人员','研发','created','updated');
 INSERT INTO ai_summary_templates(id,name,description,prompt,created_at,updated_at) VALUES('custom','Custom','Description','Original prompt','created','updated');
 UPDATE ai_summary_templates SET prompt='modified' WHERE id='builtin-standard-summary';`)
	r, err = Apply(ctx, db, path, nil)
	if err != nil || r.FromVersion != 10 || r.Backup != "" {
		t.Fatal(r, err)
	}
	if scalar(t, db, "SELECT name FROM meeting_members WHERE id='member'") != "合成人员" || scalar(t, db, "SELECT prompt FROM ai_summary_templates WHERE id='custom'") != "Original prompt" {
		t.Fatal("existing data changed")
	}
	if scalar(t, db, "SELECT prompt FROM ai_summary_templates WHERE id='builtin-standard-summary'") == "modified" {
		t.Fatal("builtin not restored")
	}
}

func TestRejectInvalidVersionBeforeDDLOrWAL(t *testing.T) {
	for _, value := range []string{"11", "-1", "invalid", "9999999999999999999999"} {
		t.Run(value, func(t *testing.T) {
			db, path := database(t)
			exec(t, db, "CREATE TABLE app_meta(key TEXT PRIMARY KEY,value TEXT NOT NULL)")
			exec(t, db, "INSERT INTO app_meta VALUES('schema_version',?)", value)
			if _, err := Apply(context.Background(), db, path, nil); !errors.Is(err, ErrVersion) {
				t.Fatal("invalid version accepted", err)
			}
			if scalar(t, db, "PRAGMA journal_mode") != "delete" || scalar(t, db, "SELECT COUNT(*) FROM sqlite_master WHERE type='table'") != "1" {
				t.Fatal("rejected database mutated")
			}
			matches, _ := filepath.Glob(path + ".pre-migration-*.bak")
			if len(matches) != 0 {
				t.Fatal("backup created for unsupported version")
			}
		})
	}
}

func TestCredentialStageFailureRecoveryAndSharedReferences(t *testing.T) {
	db, path := database(t)
	v2(t, db)
	ctx := context.Background()
	vault := &memoryVault{failWriteAt: 2, values: map[string]domain.Secret{"old-model": domain.NewSecret("synthetic-old"), "old-shared": domain.NewSecret("synthetic-shared")}}
	if _, err := Apply(ctx, db, path, vault); !errors.Is(err, domain.ErrCredentialStore) {
		t.Fatal(err)
	}
	if scalar(t, db, "SELECT value FROM app_meta WHERE key='schema_version'") != "2" || scalar(t, db, "SELECT api_key FROM ai_model_configs WHERE id='model'") != " synthetic-key " {
		t.Fatal("failed staging cleared plaintext")
	}
	journal := scalar(t, db, "SELECT value FROM app_meta WHERE key=?", journalKey)
	if strings.Contains(journal, "synthetic-key") || strings.Contains(journal, "synthetic-token") {
		t.Fatal("journal contains plaintext")
	}
	vault.failDelete = true
	if _, err := Apply(ctx, db, path, vault); !errors.Is(err, domain.ErrCredentialCleanup) {
		t.Fatal("failed cleanup ignored", err)
	}
	vault.failDelete = false
	vault.failWriteAt = 0
	r, err := Apply(ctx, db, path, vault)
	if err != nil || r.Version != 10 {
		t.Fatal(r, err)
	}
	if scalar(t, db, "SELECT api_key FROM ai_model_configs WHERE id='model'") != "" || scalar(t, db, "SELECT api_token FROM app_settings") != "" {
		t.Fatal("plaintext remains")
	}
	modelRef := scalar(t, db, "SELECT api_key_ref FROM ai_model_configs WHERE id='model'")
	tokenRef := scalar(t, db, "SELECT api_token_ref FROM app_settings")
	if vault.values[modelRef].Reveal() != "synthetic-key" || vault.values[tokenRef].Reveal() != "synthetic-token" {
		t.Fatal("published secret differs")
	}
	if vault.values["old-model"].Empty() || len(vault.values) != 4 || vault.values["old-shared"].Empty() {
		t.Fatal("snapshot or active reference removed or staged reference leaked")
	}
	if scalar(t, db, "SELECT COUNT(*) FROM app_meta WHERE key=?", journalKey) != "1" {
		t.Fatal("snapshot-protected retirement journal was discarded")
	}
	removeCredentialTestBackups(t, path)
	if _, err := Apply(ctx, db, path, vault); err != nil {
		t.Fatal(err)
	}
	if _, ok := vault.values["old-model"]; ok {
		t.Fatal("unreferenced secret remains after recovery points were removed")
	}
	if len(vault.values) != 3 || vault.values["old-shared"].Empty() {
		t.Fatal("active reference removed or staged reference leaked")
	}
	if scalar(t, db, "SELECT COUNT(*) FROM app_meta WHERE key=?", journalKey) != "0" {
		t.Fatal("recovery journal remains")
	}
}

func TestCredentialReadbackAndPublishFailureRetry(t *testing.T) {
	for _, failure := range []string{"readback", "publish"} {
		t.Run(failure, func(t *testing.T) {
			db, path := database(t)
			v2(t, db)
			vault := &memoryVault{corruptWrite: failure == "readback"}
			if failure == "publish" {
				exec(t, db, `CREATE TRIGGER reject_publish BEFORE UPDATE ON ai_model_configs WHEN NEW.api_key='' BEGIN SELECT RAISE(ABORT,'synthetic'); END`)
			}
			if _, err := Apply(context.Background(), db, path, vault); err == nil {
				t.Fatal("injected failure accepted")
			}
			if scalar(t, db, "SELECT api_token FROM app_settings") != " synthetic-token " || scalar(t, db, "SELECT value FROM app_meta WHERE key='schema_version'") != "2" {
				t.Fatal("partial credential publish")
			}
			vault.corruptWrite = false
			if failure == "publish" {
				exec(t, db, "DROP TRIGGER reject_publish")
			}
			if _, err := Apply(context.Background(), db, path, vault); err != nil {
				t.Fatal(err)
			}
			if len(vault.values) != 2 {
				t.Fatal("orphan staged credential")
			}
		})
	}
}

func TestPublishedCredentialCleanupResumesAfterReopen(t *testing.T) {
	db, path := database(t)
	v2(t, db)
	vault := &memoryVault{failDelete: true}
	if _, err := Apply(context.Background(), db, path, vault); err != nil {
		t.Fatal(err)
	}
	if scalar(t, db, "SELECT value FROM app_meta WHERE key='schema_version'") != "10" || scalar(t, db, "SELECT api_token FROM app_settings") != "" {
		t.Fatal("publication not committed")
	}
	var journal credentialJournal
	if json.Unmarshal([]byte(scalar(t, db, "SELECT value FROM app_meta WHERE key=?", journalKey)), &journal) != nil || !journal.Published {
		t.Fatal("committed cleanup phase lost")
	}
	removeCredentialTestBackups(t, path)
	if _, err := Apply(context.Background(), db, path, vault); !errors.Is(err, domain.ErrCredentialCleanup) {
		t.Fatal("credential cleanup failure after snapshot removal was ignored", err)
	}
	vault.failDelete = false
	if _, err := Apply(context.Background(), db, path, vault); err != nil {
		t.Fatal(err)
	}
	if vault.writes != 2 || len(vault.values) != 2 {
		t.Fatal("published credentials restaged or removed")
	}
}

func TestCredentialMigrationAcceptsLegacyNullJournalEntries(t *testing.T) {
	db, path := database(t)
	ctx := context.Background()
	if _, err := Apply(ctx, db, path, nil); err != nil {
		t.Fatal(err)
	}
	for _, entries := range []string{"null", "[]"} {
		exec(t, db, "INSERT INTO app_meta(key,value) VALUES(?,?)", journalKey, `{"published":false,"entries":`+entries+`}`)
		if _, err := Apply(ctx, db, path, nil); err != nil {
			t.Fatal("compatible empty migration journal blocked startup", entries, err)
		}
		if scalar(t, db, "SELECT COUNT(*) FROM app_meta WHERE key=?", journalKey) != "0" {
			t.Fatal("empty migration journal was not retired")
		}
	}
}

func removeCredentialTestBackups(t *testing.T, path string) {
	t.Helper()
	backups, err := filepath.Glob(path + ".pre-*.bak")
	if err != nil || len(backups) == 0 {
		t.Fatal("missing synthetic credential recovery points", err)
	}
	for _, backup := range backups {
		if err := os.Remove(backup); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBackupIncludesCommittedWALAndMigrationRollsBack(t *testing.T) {
	db, path := database(t)
	v2(t, db)
	exec(t, db, "PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0;")
	exec(t, db, "INSERT INTO meeting_members(id,name,created_at,updated_at) VALUES('wal-member','WAL committed','fixture','fixture')")
	info, err := os.Stat(path + "-wal")
	if err != nil || info.Size() == 0 {
		t.Fatal("WAL fixture not established")
	}
	vault := &memoryVault{}
	exec(t, db, `CREATE TRIGGER reject_v4 BEFORE UPDATE ON app_meta WHEN NEW.key='schema_version' AND NEW.value='4' BEGIN SELECT RAISE(ABORT,'synthetic'); END`)
	r, err := Apply(context.Background(), db, path, vault)
	if !errors.Is(err, ErrMigration) || r.Backup == "" {
		t.Fatal(r, err)
	}
	if scalar(t, db, "SELECT value FROM app_meta WHERE key='schema_version'") != "3" {
		t.Fatal("failed migration advanced version")
	}
	copy, err := sql.Open("sqlite", URI(r.Backup, url.Values{"mode": {"ro"}}))
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	if scalar(t, copy, "SELECT name FROM meeting_members WHERE id='wal-member'") != "WAL committed" || scalar(t, copy, "SELECT value FROM app_meta WHERE key='schema_version'") != "2" {
		t.Fatal("backup did not capture original snapshot")
	}
	info, err = os.Stat(r.Backup)
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		t.Fatal("backup permissions too broad")
	}
	exec(t, db, "DROP TRIGGER reject_v4")
	if _, err := Apply(context.Background(), db, path, vault); err != nil {
		t.Fatal(err)
	}
}
