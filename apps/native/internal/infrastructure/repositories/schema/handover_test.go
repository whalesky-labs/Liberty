package schema

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandoverBacksUpCurrentWALBeforeAnyPreparation(t *testing.T) {
	db, path := database(t)
	ctx := context.Background()
	if _, err := Apply(ctx, db, path, nil); err != nil {
		t.Fatal(err)
	}
	exec(t, db, "PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0;")
	exec(t, db, `INSERT INTO meeting_members(id,name,created_at,updated_at) VALUES('before','Before handover','fixture','fixture');
UPDATE ai_summary_templates SET prompt='Before preparation' WHERE id='builtin-standard-summary';`)
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
		t.Fatal("committed WAL fixture missing", err)
	}
	result, err := ApplyHandover(ctx, db, path, nil, false)
	if err != nil || result.FromVersion != CurrentVersion || result.Version != CurrentVersion || !strings.Contains(filepath.Base(result.Backup), ".pre-native-v10-") {
		t.Fatal("current-version handover missing backup", result, err)
	}
	exec(t, db, `INSERT INTO meeting_members(id,name,created_at,updated_at) VALUES('after','After handover','fixture','fixture')`)
	if scalar(t, db, "SELECT prompt FROM ai_summary_templates WHERE id='builtin-standard-summary'") == "Before preparation" {
		t.Fatal("preparation did not run after backup")
	}
	bytes, err := os.ReadFile(result.Backup)
	if err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored.sqlite3")
	if err := os.WriteFile(restored, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	copy, err := sql.Open("sqlite", URI(restored, url.Values{"mode": {"ro"}}))
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	if scalar(t, copy, "PRAGMA integrity_check") != "ok" || scalar(t, copy, "SELECT name FROM meeting_members WHERE id='before'") != "Before handover" || scalar(t, copy, "SELECT COUNT(*) FROM meeting_members WHERE id='after'") != "0" || scalar(t, copy, "SELECT prompt FROM ai_summary_templates WHERE id='builtin-standard-summary'") != "Before preparation" {
		t.Fatal("backup did not preserve the exact pre-handover state")
	}
	second, err := ApplyHandover(ctx, db, path, nil, false)
	if err != nil || second.Backup != result.Backup || second.Backup == "" {
		t.Fatal("repeat handover did not preserve first recovery point", err)
	}
	if scalar(t, db, "SELECT name FROM meeting_members WHERE id='after'") != "After handover" {
		t.Fatal("handover discarded new data")
	}
}

func TestHandoverRequiresExplicitMigration(t *testing.T) {
	db, path := database(t)
	v2(t, db)
	result, err := ApplyHandover(context.Background(), db, path, &memoryVault{}, false)
	if !errors.Is(err, ErrVersion) || result.Backup != "" || scalar(t, db, "SELECT value FROM app_meta WHERE key='schema_version'") != "2" || scalar(t, db, "SELECT api_token FROM app_settings") != " synthetic-token " {
		t.Fatal("implicit migration changed profile", err)
	}
	result, err = ApplyHandover(context.Background(), db, path, &memoryVault{}, true)
	if err != nil || result.Version != CurrentVersion || result.Backup == "" {
		t.Fatal("explicit migration failed", err)
	}
	reopened, err := sql.Open("sqlite", URI(path, url.Values{"mode": {"rw"}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyHandover(context.Background(), reopened, path, &memoryVault{}, false); err != nil {
		reopened.Close()
		t.Fatal("migrated recovery point did not survive restart", err)
	}
	if reopened.Close() != nil {
		t.Fatal("reopened profile close failed")
	}
	copy, err := sql.Open("sqlite", URI(result.Backup, url.Values{"mode": {"ro"}}))
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	if scalar(t, copy, "SELECT value FROM app_meta WHERE key='schema_version'") != "2" || scalar(t, copy, "SELECT api_token FROM app_settings") != " synthetic-token " {
		t.Fatal("handover backup was taken after credential migration")
	}
}

func TestHandoverBackupFailurePreventsPreparation(t *testing.T) {
	db, path := database(t)
	if _, err := Apply(context.Background(), db, path, nil); err != nil {
		t.Fatal(err)
	}
	exec(t, db, "UPDATE ai_summary_templates SET prompt='Must remain' WHERE id='builtin-standard-summary'")
	invalid := filepath.Join(filepath.Dir(path), "missing-parent", "liberty.sqlite3")
	if _, err := ApplyHandover(context.Background(), db, invalid, nil, false); !errors.Is(err, ErrBackup) {
		t.Fatal("handover continued after failed backup", err)
	}
	if scalar(t, db, "SELECT prompt FROM ai_summary_templates WHERE id='builtin-standard-summary'") != "Must remain" {
		t.Fatal("failed handover modified profile")
	}
}

func TestHandoverPreservesUnversionedDataAndSkipsEmptyBackup(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "existing tables"}[existing], func(t *testing.T) {
			db, path := database(t)
			if existing {
				exec(t, db, "CREATE TABLE synthetic_legacy_data(value TEXT NOT NULL); INSERT INTO synthetic_legacy_data VALUES('preserved')")
			}
			result, err := ApplyHandover(context.Background(), db, path, nil, true)
			if err != nil || result.FromVersion != 0 || result.Version != CurrentVersion || (result.Backup != "") != existing {
				t.Fatal("unexpected unversioned handover", result, err)
			}
			if existing {
				copy, err := sql.Open("sqlite", URI(result.Backup, url.Values{"mode": {"ro"}}))
				if err != nil {
					t.Fatal(err)
				}
				defer copy.Close()
				if scalar(t, copy, "SELECT value FROM synthetic_legacy_data") != "preserved" || scalar(t, copy, "SELECT COUNT(*) FROM sqlite_master WHERE name='app_meta'") != "0" {
					t.Fatal("unversioned backup did not precede migration")
				}
			}
		})
	}
}

func TestHandoverBindsUnversionedBackupBeforePostMigrationFailure(t *testing.T) {
	db, path := database(t)
	exec(t, db, `CREATE TABLE synthetic_legacy_data(value TEXT NOT NULL);
INSERT INTO synthetic_legacy_data VALUES('preserved');
CREATE TABLE ai_summary_templates (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 description TEXT NOT NULL,
 prompt TEXT NOT NULL,
 include_speaker_by_default INTEGER NOT NULL DEFAULT 1,
 include_timestamp_by_default INTEGER NOT NULL DEFAULT 1,
 builtin INTEGER NOT NULL DEFAULT 0,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE TRIGGER reject_initial_template BEFORE INSERT ON ai_summary_templates
BEGIN SELECT RAISE(ABORT, 'synthetic post-migration failure'); END;`)

	ctx := context.Background()
	first, err := ApplyHandover(ctx, db, path, nil, true)
	if !errors.Is(err, ErrMigration) || first.Version != CurrentVersion || first.Backup == "" {
		t.Fatal("synthetic post-migration failure was not reached", first, err)
	}
	marker := scalar(t, db, "SELECT value FROM app_meta WHERE key=?", handoverBackupKey)
	parts := strings.Split(marker, "|")
	if len(parts) != 3 || parts[0] != filepath.Base(first.Backup) || parts[1] != "0" {
		t.Fatal("original unversioned recovery point was not durably bound before later migration work", marker)
	}

	exec(t, db, "DROP TRIGGER reject_initial_template")
	retry, err := ApplyHandover(ctx, db, path, nil, true)
	if err != nil || retry.Backup != first.Backup {
		t.Fatal("handover retry replaced the original recovery point", retry, err)
	}
	if err := verifyBackup(ctx, retry.Backup, 0); err != nil {
		t.Fatal("bound recovery point no longer contains the original unversioned schema", err)
	}
}

func TestHandoverRejectsMissingOrChangedRecoveryPoint(t *testing.T) {
	for _, tamper := range []string{"missing", "changed"} {
		t.Run(tamper, func(t *testing.T) {
			db, path := database(t)
			ctx := context.Background()
			if _, err := Apply(ctx, db, path, nil); err != nil {
				t.Fatal(err)
			}
			result, err := ApplyHandover(ctx, db, path, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			exec(t, db, "UPDATE ai_summary_templates SET prompt='Must remain' WHERE id='builtin-standard-summary'")
			if tamper == "missing" {
				if err := os.Remove(result.Backup); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(result.Backup, []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := ApplyHandover(ctx, db, path, nil, false); !errors.Is(err, ErrBackup) {
				t.Fatal("client continued without original recovery point", err)
			}
			if scalar(t, db, "SELECT prompt FROM ai_summary_templates WHERE id='builtin-standard-summary'") != "Must remain" {
				t.Fatal("refused handover changed database")
			}
		})
	}
}
