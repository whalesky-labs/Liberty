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
	for v := 0; v <= CurrentVersion; v++ {
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
			if err != nil || r.FromVersion != v || r.Version != 10 {
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
