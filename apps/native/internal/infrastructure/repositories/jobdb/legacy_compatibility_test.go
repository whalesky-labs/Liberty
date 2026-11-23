//go:build compatibility

package jobdb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/legacyjobs"
	"liberty.local/liberty/internal/infrastructure/repositories/legacydb"
)

func TestWriteLegacyImportMatchesRustAndDoesNotReapply(t *testing.T) {
	ctx := context.Background()
	fixture := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if fixture == "" {
		t.Fatal("run pnpm native:compat")
	}
	data, err := os.ReadFile(filepath.Join(fixture, "legacy-expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var expected []domain.Result
	if json.Unmarshal(data, &expected) != nil || len(expected) != 6 {
		t.Fatal("invalid legacy fixtures")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "liberty.sqlite3")
	s, _, err := OpenPrepared(ctx, path, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	source, err := legacyjobs.Open(filepath.Join(fixture, "legacy-input"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	result, err := s.ImportLegacy(ctx, source)
	if err != nil || result.Imported != 6 || result.Skipped != 1 {
		t.Fatal(result, err)
	}
	r, err := legacydb.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, want := range expected {
		got, err := r.ReadResult(ctx, want.Job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			a, _ := json.Marshal(got)
			b, _ := json.Marshal(want)
			t.Fatalf("synthetic legacy mismatch: %s\n%s", a, b)
		}
		var python, runner, path string
		if s.db.QueryRow("SELECT python_path,runner_script_path FROM jobs WHERE id=?", want.Job.ID).Scan(&python, &runner) != nil || python != "synthetic-python" || runner != "synthetic-runner" {
			t.Fatal("private runtime metadata lost")
		}
		if s.db.QueryRow("SELECT path FROM job_source_files WHERE job_id=?", want.Job.ID).Scan(&path) != nil || path != "synthetic-media.wav" {
			t.Fatal("source path lost")
		}
	}
	if _, err = s.db.Exec("UPDATE jobs SET title='edited after import'"); err != nil {
		t.Fatal(err)
	}
	result, err = s.ImportLegacy(ctx, source)
	if err != nil || !result.AlreadyImported || result.Imported != 0 {
		t.Fatal("import repeated", err)
	}
	if got, err := r.ReadResult(ctx, expected[0].Job.ID); err != nil || got.Job.Title != "edited after import" {
		t.Fatal("repeat import overwrote edit")
	}
}
