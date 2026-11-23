package legacyjobs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/legacyjobs"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
	"liberty.local/liberty/internal/infrastructure/repositories/legacydb"
	"liberty.local/liberty/internal/infrastructure/repositories/schema"
)

func TestLegacyFileDecodingPersistsExactDataAcrossReopen(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	database := filepath.Join(directory, "synthetic.sqlite3")
	sourcePath := filepath.Join(directory, "synthetic.wav")
	for index := 1; index <= 4; index++ {
		id := fmt.Sprintf("job-1700000000000-%d", index)
		jobDirectory := filepath.Join(directory, "jobs", id)
		if os.MkdirAll(jobDirectory, 0700) != nil {
			t.Fatal("synthetic job directory unavailable")
		}
		job := map[string]any{
			"id": id, "title": "Exact synthetic title", "durationMinutes": 0, "createdAt": "2026-11-20T00:00:00Z", "lang": "zh", "enableSpeaker": false, "summaryTemplate": "", "uploadStatus": "uploaded", "asrStatus": "completed", "summaryStatus": "idle", "overallStatus": "completed",
			"sourceFiles": []any{map[string]any{"id": fmt.Sprintf("source-%d", index), "name": "synthetic.wav", "path": sourcePath, "sizeLabel": "1 B", "kind": "audio"}},
		}
		if index == 1 {
			job["transcriptSegments"] = json.RawMessage(`[{"id":"s1","startMs":0,"endMs":1000,"text":"Exact transcript","TEXT":"alias transcript","SPEAKER":"alias speaker"}]`)
			job["summary"] = json.RawMessage(`{"overview":"Exact summary","OVERVIEW":"alias summary"}`)
		}
		data, err := json.Marshal(job)
		if err != nil {
			t.Fatal("synthetic job encoding failed")
		}
		if index == 1 {
			data = []byte(strings.TrimSuffix(string(data), "}") + `,"TITLE":"alias title","PYTHONPATH":"alias executable","RUNNERSCRIPTPATH":"alias script"}`)
		}
		if index == 2 {
			data = []byte(strings.TrimSuffix(string(data), "}") + `,"title":"duplicate title"}`)
		}
		if os.WriteFile(filepath.Join(jobDirectory, "job.json"), data, 0600) != nil {
			t.Fatal("synthetic job fixture failed")
		}
		var result string
		if index == 3 {
			result = `{"durationMinutes":3,"transcriptSegments":[{"id":"s1","startMs":0,"endMs":1000,"text":"first","text":"second"}]}`
		}
		if index == 4 {
			result = `{"durationMinutes":4,"transcriptSegments":[{"id":"s1","startMs":0,"endMs":1000,"text":"Exact result fallback","TEXT":"alias result"}],"failureReason":null,"FAILUREREASON":"alias failure"}`
		}
		if result != "" && os.WriteFile(filepath.Join(jobDirectory, "result.json"), []byte(result), 0600) != nil {
			t.Fatal("synthetic result fixture failed")
		}
	}
	source, err := legacyjobs.Open(directory)
	if err != nil {
		t.Fatal("legacy source unavailable")
	}
	defer source.Close()
	store, _, err := jobdb.OpenPrepared(ctx, database, nil, true)
	if err != nil {
		t.Fatal("synthetic database unavailable")
	}
	defer store.Close()
	imported, err := store.ImportLegacy(ctx, source)
	if err != nil || imported.Imported != 3 || imported.Skipped != 1 || imported.AlreadyImported {
		t.Fatal("import did not skip the duplicate-field snapshot")
	}
	if store.Close() != nil {
		t.Fatal("initial database close failed")
	}
	var baseline []domain.Result
	for pass := 0; pass < 3; pass++ {
		store, err = jobdb.Open(ctx, database)
		if err != nil {
			t.Fatal("imported database reopen failed")
		}
		repeated, err := store.ImportLegacy(ctx, nil)
		if err != nil || !repeated.AlreadyImported || repeated.Imported != 0 {
			store.Close()
			t.Fatal("completed import was replayed")
		}
		reader, err := legacydb.Open(ctx, database)
		if err != nil {
			store.Close()
			t.Fatal("imported projection unavailable")
		}
		jobs, err := reader.ListJobs(ctx)
		if err != nil || len(jobs) != 3 {
			reader.Close()
			store.Close()
			t.Fatal("reopen changed imported job count")
		}
		var actual []domain.Result
		for _, index := range []int{1, 3, 4} {
			id := fmt.Sprintf("job-1700000000000-%d", index)
			result, err := reader.ReadResult(ctx, id)
			if err != nil {
				reader.Close()
				store.Close()
				t.Fatal("imported result unavailable")
			}
			actual = append(actual, result)
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "alias") || result.Job.Title != "Exact synthetic title" {
				reader.Close()
				store.Close()
				t.Fatal("field alias persisted into user-visible data")
			}
			input, err := store.Input(ctx, id)
			if err != nil || input.SourcePath != sourcePath || input.PythonPath != "" || input.RunnerPath != "" {
				reader.Close()
				store.Close()
				t.Fatal("source path or executable field changed during import")
			}
			if index == 1 && (len(result.Transcript) != 1 || result.Transcript[0].Text != "Exact transcript" || result.Summary.Overview != "Exact summary") {
				reader.Close()
				store.Close()
				t.Fatal("saved transcript or imported summary changed")
			}
			if index == 3 && (len(result.Transcript) != 0 || result.Job.DurationMinutes != 0) {
				reader.Close()
				store.Close()
				t.Fatal("invalid result partially merged")
			}
			if index == 4 && (len(result.Transcript) != 1 || result.Transcript[0].Text != "Exact result fallback" || result.Job.DurationMinutes != 4 || result.Job.FailureReason != nil) {
				reader.Close()
				store.Close()
				t.Fatal("valid fallback values changed")
			}
		}
		reader.Close()
		if store.Close() != nil {
			t.Fatal("reopened writer close failed")
		}
		if pass == 0 {
			baseline = actual
		} else if !reflect.DeepEqual(actual, baseline) {
			t.Fatal("durable projections changed across reopen")
		}
	}
	// Audit the stored values directly as well as through application readers.
	db, err := sql.Open("sqlite", schema.URI(database, url.Values{"mode": {"ro"}}))
	if err != nil {
		t.Fatal("read-only audit unavailable")
	}
	defer db.Close()
	var count int
	if db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE python_path IS NOT NULL OR runner_script_path IS NOT NULL OR title != 'Exact synthetic title'`).Scan(&count) != nil || count != 0 {
		t.Fatal("unexpected runtime field or title was stored")
	}
	var marker string
	if db.QueryRow(`SELECT value FROM app_meta WHERE key='legacy_jobs_imported'`).Scan(&marker) != nil || marker != "1" {
		t.Fatal("successful import marker missing")
	}
}
