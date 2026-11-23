package legacydb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"liberty.local/liberty/internal/application"
)

// These small fixtures test defensive I/O only. Actual old-schema compatibility
// is verified separately against the database emitted by the Rust repository.
func fixture(t *testing.T, version string) string {
	t.Helper()
	name := "数据库 # & ?.sqlite3"
	if runtime.GOOS == "windows" {
		name = "数据库 # & %.sqlite3"
	}
	path := filepath.Join(t.TempDir(), name)
	u := fixtureURI(path)
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE app_meta(key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE TABLE jobs(id TEXT PRIMARY KEY, title TEXT, created_at TEXT, overall_status TEXT, diarization_status TEXT, duration_minutes INTEGER,
        lang TEXT NOT NULL DEFAULT 'zh', enable_speaker INTEGER NOT NULL DEFAULT 0,
        runner_protocol_version INTEGER, asr_backend TEXT NOT NULL DEFAULT 'unknown',
        warnings_json TEXT NOT NULL DEFAULT '[]', summary_template TEXT NOT NULL DEFAULT '',
        upload_status TEXT NOT NULL DEFAULT 'completed', asr_status TEXT NOT NULL DEFAULT 'completed',
        summary_status TEXT NOT NULL DEFAULT 'idle', processing_started_at_ms INTEGER,
        processing_finished_at_ms INTEGER, processing_duration_seconds INTEGER, failure_reason TEXT,
        active_summary_run_id TEXT, last_exported_at TEXT, hotwords_json TEXT NOT NULL DEFAULT '[]',
        export_formats_json TEXT NOT NULL DEFAULT '[]', process_log TEXT);
		CREATE TABLE ai_summary_runs(id TEXT,job_id TEXT,model_config_id TEXT,template_id TEXT,
        include_speaker INTEGER,include_timestamp INTEGER,extra_instructions TEXT,status TEXT,
        error_message TEXT,prompt_preview TEXT,raw_response TEXT,result_json TEXT,minutes_payload_json TEXT,
        created_at TEXT,updated_at TEXT);
        CREATE TABLE job_deletion_ops(job_id TEXT);
		CREATE TABLE job_source_files(id TEXT, job_id TEXT, name TEXT, kind TEXT, size_label TEXT);
		CREATE TABLE transcript_segments(id TEXT, job_id TEXT, segment_type TEXT, start_ms INTEGER, end_ms INTEGER, speaker TEXT, text TEXT, segment_order INTEGER);
		INSERT INTO jobs (id,title,created_at,overall_status,diarization_status,duration_minutes) VALUES ('visible','合成任务','2026-11-16','completed','unavailable',1),('deleted','deleted','2026-11-15','completed','unavailable',1);
		INSERT INTO job_deletion_ops VALUES ('deleted');
		INSERT INTO job_source_files VALUES ('file','visible','budget.wav','audio','1 MB');
		INSERT INTO transcript_segments VALUES ('t','visible','transcript',0,1000,NULL,'合成逐字稿',0);`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO app_meta VALUES ('schema_version',?)", version); err != nil {
		t.Fatal(err)
	}
	return path
}

func fixtureURI(path string) url.URL {
	// Keep the drive letter in the URI path, not its authority.
	return url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(path), "/")}
}

func TestReadOnlyAndDeletionVisibility(t *testing.T) {
	path := fixture(t, SchemaVersion)
	before, _ := os.ReadFile(path)
	r, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := r.ListJobs(context.Background())
	if err != nil || len(jobs) != 1 || jobs[0].SourceFiles[0].Name != "budget.wav" {
		t.Fatalf("queue: %+v %v", jobs, err)
	}
	result, err := r.ReadResult(context.Background(), "visible")
	if err != nil || len(result.Transcript) != 1 || result.Transcript[0].Text != "合成逐字稿" {
		t.Fatalf("result: %+v %v", result, err)
	}
	if _, err := r.ReadResult(context.Background(), "deleted"); !errors.Is(err, application.ErrJobNotFound) {
		t.Fatalf("deletion surfaced: %v", err)
	}
	if _, err := r.ReadResult(context.Background(), "' OR 1=1 --"); !errors.Is(err, application.ErrJobNotFound) {
		t.Fatal("job id was not bound")
	}
	if _, err := r.db.Exec("DELETE FROM jobs"); err == nil {
		t.Fatal("reader accepted a write")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("database changed")
	}
}

func TestRejectsUnknownSchemaWithoutModifyingIt(t *testing.T) {
	for _, version := range []string{"1", "9", "11", "invalid"} {
		t.Run(version, func(t *testing.T) {
			path := fixture(t, version)
			before, _ := os.ReadFile(path)
			if r, err := Open(context.Background(), path); !errors.Is(err, ErrSchema) {
				if r != nil {
					r.Close()
				}
				t.Fatalf("schema accepted: %v", err)
			}
			after, _ := os.ReadFile(path)
			if sha256.Sum256(before) != sha256.Sum256(after) {
				t.Fatal("rejected database changed")
			}
		})
	}
}

func TestMissingDatabaseIsNotCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.sqlite3")
	if _, err := Open(context.Background(), path); !errors.Is(err, ErrDatabase) {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("created missing database")
	}
}
