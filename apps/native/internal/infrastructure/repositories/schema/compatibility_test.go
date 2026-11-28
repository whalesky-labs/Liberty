//go:build compatibility

package schema

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRustMigrationVersionsAndCompleteDataMatch(t *testing.T) {
	directory := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if directory == "" {
		t.Fatal("run pnpm native:compat")
	}
	// The independently hashed Rust golden corpus is frozen at v10. New native
	// migrations must preserve that projection and have their own adjacent tests.
	for v := 0; v <= 10; v++ {
		t.Run(fmt.Sprint(v), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "migration.sqlite3")
			data, err := os.ReadFile(filepath.Join(directory, fmt.Sprintf("migration-v%d.sqlite3", v)))
			if err != nil {
				t.Fatal(err)
			}
			if os.WriteFile(path, data, 0600) != nil {
				t.Fatal("fixture copy")
			}
			db, err := sql.Open("sqlite", URI(path, url.Values{"mode": {"rw"}}))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			beforeUpgrade := snapshot(t, db)
			vault := &memoryVault{}
			r, err := Apply(context.Background(), db, path, vault)
			if err != nil || r.FromVersion != v || r.Version != CurrentVersion {
				t.Fatal(r, err)
			}
			expected, err := sql.Open("sqlite", URI(filepath.Join(directory, fmt.Sprintf("migration-v%d-expected.sqlite3", v)), url.Values{"mode": {"ro"}}))
			if err != nil {
				t.Fatal(err)
			}
			defer expected.Close()
			compareDatabases(t, db, expected)
			before := snapshot(t, db)
			if _, err := Apply(context.Background(), db, path, vault); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, snapshot(t, db)) {
				t.Fatal("repeat startup mutated migrated data")
			}
			if v > 0 && v < CurrentVersion {
				backupData, err := os.ReadFile(r.Backup)
				if err != nil {
					t.Fatal("migration backup unavailable")
				}
				restoredPath := filepath.Join(t.TempDir(), "restored.sqlite3")
				if err := os.WriteFile(restoredPath, backupData, 0600); err != nil {
					t.Fatal("restore failed")
				}
				restored, err := sql.Open("sqlite", URI(restoredPath, url.Values{"mode": {"rw"}}))
				if err != nil {
					t.Fatal(err)
				}
				defer restored.Close()
				if !reflect.DeepEqual(beforeUpgrade, snapshot(t, restored)) || scalar(t, restored, "PRAGMA integrity_check") != "ok" {
					t.Fatal("restored legacy data differs before re-upgrade")
				}
				if _, err := Apply(context.Background(), restored, restoredPath, &memoryVault{}); err != nil {
					t.Fatal(err)
				}
				compareDatabases(t, restored, expected)
			}
		})
	}
}

func compareDatabases(t *testing.T, got, want *sql.DB) {
	t.Helper()
	a, b := snapshot(t, got), snapshot(t, want)
	if scalar(t, got, "SELECT value FROM app_meta WHERE key='schema_version'") != "13" || scalar(t, want, "SELECT value FROM app_meta WHERE key='schema_version'") != "10" {
		t.Fatal("unexpected compatibility projection versions")
	}
	for index, row := range a["app_meta"] {
		if row == `["schema_version","13"]` {
			a["app_meta"][index] = `["schema_version","10"]`
		}
	}
	for _, name := range []string{"live_sessions", "live_segments", "live_session_events", "meeting_drafts"} {
		if rows, ok := a[name]; !ok || len(rows) != 0 {
			t.Fatal("upgrade did not create empty live tables")
		}
		delete(a, name)
		delete(a, "schema:"+name)
	}
	for _, name := range []string{"live_sessions_created", "live_session_events_session"} {
		if _, ok := a["schema:"+name]; !ok {
			t.Fatal("live index missing")
		}
		delete(a, "schema:"+name)
	}
	for table, rows := range b {
		if !reflect.DeepEqual(a[table], rows) {
			t.Errorf("table %s differs from Rust (details omitted to keep test output free of credentials)", table)
		}
	}
	if len(a) != len(b) {
		t.Fatal("table set differs")
	}
	for _, db := range []*sql.DB{got, want} {
		if scalar(t, db, "PRAGMA integrity_check") != "ok" {
			t.Fatal("integrity check")
		}
		rows, err := db.Query("PRAGMA foreign_key_check")
		if err != nil {
			t.Fatal(err)
		}
		if rows.Next() {
			t.Error("foreign key violation")
		}
		rows.Close()
	}
}
