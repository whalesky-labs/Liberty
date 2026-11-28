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
)

func TestBackupCredentialsRetainModelAndTokenFromEveryRestorablePurpose(t *testing.T) {
	for _, purpose := range []string{"pre-native", "pre-migration", "pre-restore", "pre-custom"} {
		t.Run(purpose, func(t *testing.T) {
			ctx := context.Background()
			db, path := database(t)
			if _, err := Apply(ctx, db, path, nil); err != nil {
				t.Fatal(err)
			}
			exec(t, db, `INSERT INTO ai_model_configs(id,name,base_url,api_key,api_key_ref,model,created_at,updated_at) VALUES('synthetic','Synthetic','https://example.test','','synthetic-model','synthetic','fixture','fixture');`)
			exec(t, db, `INSERT INTO app_settings(id,theme_mode,liquid_glass_style,accent_color,locale,backend_url,api_token,api_token_ref,default_hotwords,summary_template,python_path,runner_script_path) VALUES(1,'auto','transparent','#2f6dff','zh-CN','','','synthetic-token','','','','')`)
			connection, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := backup(ctx, connection, path, CurrentVersion, purpose)
			connection.Close()
			if err != nil {
				t.Fatal(err)
			}
			exec(t, db, `DELETE FROM ai_model_configs; DELETE FROM app_settings;`)
			for _, reference := range []string{"synthetic-model", "synthetic-token", "synthetic-unused"} {
				found, err := BackupCredentialReferenced(ctx, path, reference)
				if err != nil || found != (reference != "synthetic-unused") {
					t.Fatal("backup reference protection differs", reference, found, err)
				}
			}
			if err := os.Remove(snapshot); err != nil {
				t.Fatal(err)
			}
			if found, err := BackupCredentialReferenced(ctx, path, "synthetic-model"); err != nil || found {
				t.Fatal("removed snapshot still retains reference", found, err)
			}
		})
	}
}

func TestBackupCredentialsInspectExistingReferencesInEarlySchemas(t *testing.T) {
	db, path := database(t)
	v2(t, db)
	ctx := context.Background()
	connection, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = backup(ctx, connection, path, 2, "pre-migration")
	connection.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, reference := range []string{"old-model", "old-shared"} {
		if found, err := BackupCredentialReferenced(ctx, path, reference); err != nil || !found {
			t.Fatal("early schema reference was ignored", found, err)
		}
	}
}

func TestBackupCredentialsFailClosedOnInvalidSnapshot(t *testing.T) {
	for _, kind := range []string{"corrupt", "empty", "future", "missing-column", "directory", "link", "same-file"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			db, path := database(t)
			if _, err := Apply(ctx, db, path, nil); err != nil {
				t.Fatal(err)
			}
			snapshot := path + ".pre-restore-v10-invalid.bak"
			switch kind {
			case "corrupt", "empty":
				var data []byte
				if kind == "corrupt" {
					data = []byte("synthetic-corrupt-snapshot")
				}
				if err := os.WriteFile(snapshot, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(snapshot, 0700); err != nil {
					t.Fatal(err)
				}
			case "link":
				if err := os.Symlink(path, snapshot); err != nil {
					if runtime.GOOS == "windows" {
						t.Skip("symlink privilege unavailable")
					}
					t.Fatal(err)
				}
			case "same-file":
				if err := os.Link(path, snapshot); err != nil {
					t.Fatal(err)
				}
			default:
				connection, err := db.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				generated, err := backup(ctx, connection, path, CurrentVersion, "pre-restore")
				connection.Close()
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(generated, snapshot); err != nil {
					t.Fatal(err)
				}
				snapshotDB, err := sql.Open("sqlite", URI(snapshot, url.Values{"mode": {"rw"}}))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { snapshotDB.Close() })
				if kind == "future" {
					exec(t, snapshotDB, "UPDATE app_meta SET value='14' WHERE key='schema_version'")
				} else {
					exec(t, snapshotDB, "DROP TABLE ai_model_configs")
				}
				if err := snapshotDB.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if found, err := BackupCredentialReferenced(ctx, path, "synthetic-unused"); !errors.Is(err, ErrBackup) || found {
				t.Fatal("unreadable snapshot allowed credential deletion", found, err)
			}
		})
	}
}

func TestBackupCredentialsIgnoreForeignNamesAndHonorCancellation(t *testing.T) {
	db, path := database(t)
	ctx := context.Background()
	if _, err := Apply(ctx, db, path, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"foreign.sqlite3.pre-native-v10-x.bak", filepath.Base(path) + ".pre-native-v10-x.bak.tmp"} {
		if err := os.WriteFile(filepath.Join(filepath.Dir(path), name), []byte("synthetic-invalid"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if found, err := BackupCredentialReferenced(ctx, path, "synthetic-unused"); err != nil || found {
		t.Fatal("foreign artifact blocked unrelated cleanup", found, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if found, err := BackupCredentialReferenced(canceled, path, "synthetic-unused"); err == nil || found {
		t.Fatal("canceled scan allowed credential deletion", found, err)
	}
}
