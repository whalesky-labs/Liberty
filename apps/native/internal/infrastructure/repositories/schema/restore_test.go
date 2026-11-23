package schema

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"liberty.local/liberty/internal/domain"
)

func TestRestoreVerifiedSnapshotAndRetainPreRestoreWALRecovery(t *testing.T) {
	ctx := context.Background()
	db, path := database(t)
	if _, err := Apply(ctx, db, path, nil); err != nil {
		t.Fatal(err)
	}
	exec(t, db, `INSERT INTO meeting_members(id,name,created_at,updated_at) VALUES('pre-native','Before handover','fixture','fixture')`)
	handover, err := ApplyHandover(ctx, db, path, nil, false)
	if err != nil || handover.Backup == "" {
		t.Fatal("missing handover snapshot", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0;"); err != nil {
		t.Fatal(err)
	}
	exec(t, db, `INSERT INTO meeting_members(id,name,created_at,updated_at) VALUES('after-native','After handover','fixture','fixture')`)
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
		t.Fatal("WAL test state missing", err)
	}
	result, err := RestoreLocked(ctx, path, filepath.Base(handover.Backup), nil)
	if err != nil || result.RestoredVersion != CurrentVersion || result.SafetyBackup == "" {
		t.Fatal("restore failed", result, err)
	}
	if scalar(t, db, "PRAGMA integrity_check") != "ok" || scalar(t, db, "SELECT name FROM meeting_members WHERE id='pre-native'") != "Before handover" || scalar(t, db, "SELECT COUNT(*) FROM meeting_members WHERE id='after-native'") != "0" {
		t.Fatal("restore did not return to the selected snapshot")
	}
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := sql.Open("sqlite", URI(path, url.Values{"mode": {"ro"}}))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if scalar(t, restored, "SELECT COUNT(*) FROM meeting_members WHERE id='after-native'") != "0" {
		t.Fatal("restored result was not durable")
	}
	safety, err := sql.Open("sqlite", URI(result.SafetyBackup, url.Values{"mode": {"ro"}}))
	if err != nil {
		t.Fatal(err)
	}
	defer safety.Close()
	if scalar(t, safety, "PRAGMA integrity_check") != "ok" || scalar(t, safety, "SELECT name FROM meeting_members WHERE id='after-native'") != "After handover" {
		t.Fatal("pre-restore safety snapshot did not preserve WAL data")
	}
}

func TestRestoreRefusesInvalidSourcesWithoutChangingCurrentDatabase(t *testing.T) {
	ctx := context.Background()
	db, path := database(t)
	if _, err := Apply(ctx, db, path, nil); err != nil {
		t.Fatal(err)
	}
	exec(t, db, `INSERT INTO meeting_members(id,name,created_at,updated_at) VALUES('current','Keep me','fixture','fixture')`)
	corrupt := filepath.Base(path) + ".pre-native-v10-corrupt.bak"
	for _, name := range []string{"../escape.pre-native-v10-x.bak", "liberty.sqlite3", "foreign.sqlite3.pre-native-v10-x.bak", corrupt} {
		if name == corrupt {
			if err := os.WriteFile(filepath.Join(filepath.Dir(path), name), []byte("not sqlite"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := RestoreLocked(ctx, path, name, nil); !errors.Is(err, ErrRestore) {
			t.Fatal("invalid restore source accepted", name, err)
		}
	}
	if scalar(t, db, "SELECT name FROM meeting_members WHERE id='current'") != "Keep me" {
		t.Fatal("failed restore changed current data")
	}
}

func TestRestoreRefusesCorruptCurrentDatabaseAndPreservesIt(t *testing.T) {
	ctx := context.Background()
	db, path := database(t)
	if _, err := Apply(ctx, db, path, nil); err != nil {
		t.Fatal(err)
	}
	exec(t, db, `INSERT INTO meeting_members(id,name,created_at,updated_at) VALUES('current','Keep me','fixture','fixture')`)
	result, err := ApplyHandover(ctx, db, path, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("synthetic-corrupt-database"), 0600); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreLocked(ctx, path, filepath.Base(result.Backup), nil); !errors.Is(err, ErrRestore) {
		t.Fatal("restore over a corrupted current file unexpectedly succeeded", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(bytes) {
		t.Fatal("failed restore altered corrupted current bytes", err)
	}
}

func TestRestoreRejectsInvalidTargetAndLinkedSource(t *testing.T) {
	ctx := context.Background()
	db, path := database(t)
	if _, err := Apply(ctx, db, path, nil); err != nil {
		t.Fatal(err)
	}
	result, err := ApplyHandover(ctx, db, path, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(path), filepath.Base(path)+".pre-native-v10-link.bak")
	if err := os.Symlink(result.Backup, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlink privilege unavailable")
		}
		t.Fatal(err)
	}
	if _, err := RestoreLocked(ctx, path, filepath.Base(link), nil); !errors.Is(err, ErrRestore) {
		t.Fatal("linked source accepted", err)
	}
	if _, err := RestoreLocked(ctx, path+"-missing", filepath.Base(result.Backup), nil); !errors.Is(err, ErrRestore) {
		t.Fatal("missing database accepted", err)
	}
}

func TestRestoreRequiresAllReferencedSystemCredentials(t *testing.T) {
	ctx := context.Background()
	db, path := database(t)
	if _, err := Apply(ctx, db, path, nil); err != nil {
		t.Fatal(err)
	}
	exec(t, db, `INSERT INTO ai_model_configs(id,name,base_url,model,api_key,api_key_ref,created_at,updated_at) VALUES('synthetic','Synthetic','https://example.test','synthetic','','test-restore-reference','fixture','fixture')`)
	result, err := ApplyHandover(ctx, db, path, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	exec(t, db, "UPDATE ai_model_configs SET name='Current model' WHERE id='synthetic'")
	for _, vault := range []Credentials{nil, &memoryVault{}} {
		if _, err := RestoreLocked(ctx, path, filepath.Base(result.Backup), vault); !errors.Is(err, ErrRestore) {
			t.Fatal("restore ignored a missing system credential", err)
		}
		if scalar(t, db, "SELECT name FROM ai_model_configs WHERE id='synthetic'") != "Current model" {
			t.Fatal("credential refusal changed current profile")
		}
	}
	vault := &memoryVault{values: map[string]domain.Secret{"test-restore-reference": domain.NewSecret("synthetic-value")}}
	if _, err := RestoreLocked(ctx, path, filepath.Base(result.Backup), vault); err != nil {
		t.Fatal("restore refused available credential", err)
	}
	if scalar(t, db, "SELECT name FROM ai_model_configs WHERE id='synthetic'") != "Synthetic" || vault.writes != 0 || vault.deletes != 0 {
		t.Fatal("restore changed credentials or failed to restore data")
	}
}

func TestRestoreEarlySchemaAlsoRequiresExistingCredentialReferences(t *testing.T) {
	ctx := context.Background()
	db, path := database(t)
	v2(t, db)
	connection, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := backup(ctx, connection, path, 2, "pre-migration")
	connection.Close()
	if err != nil {
		t.Fatal(err)
	}
	exec(t, db, "UPDATE ai_model_configs SET name='Current model' WHERE id='model'")
	if _, err := RestoreLocked(ctx, path, filepath.Base(snapshot), &memoryVault{}); !errors.Is(err, ErrRestore) {
		t.Fatal("early-schema restore ignored an existing missing reference", err)
	}
	if scalar(t, db, "SELECT name FROM ai_model_configs WHERE id='model'") != "Current model" {
		t.Fatal("missing-reference preflight changed current data")
	}
	vault := &memoryVault{values: map[string]domain.Secret{"old-model": domain.NewSecret("synthetic-old"), "old-shared": domain.NewSecret("synthetic-shared")}}
	result, err := RestoreLocked(ctx, path, filepath.Base(snapshot), vault)
	if err != nil || result.RestoredVersion != 2 || result.SafetyBackup == "" {
		t.Fatal("valid early-schema references were refused", result, err)
	}
	if scalar(t, db, "SELECT name FROM ai_model_configs WHERE id='model'") != "Synthetic" || vault.writes != 0 || vault.deletes != 0 {
		t.Fatal("early-schema restore changed credentials or lost data")
	}
}
