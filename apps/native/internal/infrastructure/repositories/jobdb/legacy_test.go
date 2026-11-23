package jobdb

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/legacyjobs"
	"liberty.local/liberty/internal/infrastructure/repositories/legacydb"
)

type legacyBatch struct {
	jobs    []application.LegacyJob
	err     error
	skipped int
}

func (b legacyBatch) Each(_ context.Context, fn func(application.LegacyJob) error) (int, error) {
	for _, j := range b.jobs {
		if err := fn(j); err != nil {
			return 0, err
		}
	}
	return b.skipped, b.err
}
func TestLegacyImportBatchRollbackAndRetry(t *testing.T) {
	ctx := context.Background()
	s, _, err := OpenPrepared(ctx, filepath.Join(t.TempDir(), "liberty.sqlite3"), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	job := application.LegacyJob{Result: domain.Result{Job: domain.JobSummary{ID: "job-1700000000000-1", Title: "synthetic", ASRBackend: "unknown", DiarizationStatus: "legacy_unverified", OverallStatus: "completed"}, Transcript: []domain.Segment{{ID: "s", Text: "synthetic"}}, Summary: domain.Summary{Overview: "legacy summary"}}}
	for _, mode := range []string{"read", "write"} {
		batch := legacyBatch{jobs: []application.LegacyJob{job}}
		if mode == "read" {
			batch.err = errors.New("synthetic read failure")
		} else {
			if _, err := s.db.Exec(`CREATE TRIGGER reject_import BEFORE INSERT ON app_meta WHEN NEW.key='legacy_jobs_imported' BEGIN SELECT RAISE(ABORT,'synthetic'); END`); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.ImportLegacy(ctx, batch); err == nil {
			t.Fatal("injected failure accepted")
		}
		for _, table := range []string{"jobs", "transcript_segments", "ai_summary_runs"} {
			var count int
			if s.db.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&count) != nil || count != 0 {
				t.Fatal("partial import persisted", table)
			}
		}
		var count int
		if s.db.QueryRow("SELECT COUNT(*) FROM app_meta WHERE key='legacy_jobs_imported'").Scan(&count) != nil || count != 0 {
			t.Fatal("failed import marked complete")
		}
		if mode == "write" {
			if _, err := s.db.Exec("DROP TRIGGER reject_import"); err != nil {
				t.Fatal(err)
			}
		}
	}
	result, err := s.ImportLegacy(ctx, legacyBatch{jobs: []application.LegacyJob{job}})
	if err != nil || result.Imported != 1 {
		t.Fatal("retry failed", err)
	}
	result, err = s.ImportLegacy(ctx, nil)
	if err != nil || !result.AlreadyImported {
		t.Fatal("repeat import accessed source", err)
	}
}

func TestLegacyImportPreservesExistingSQLiteResultAfterReopen(t *testing.T) {
	store, _, input := lifecycleFixture(t)
	ctx := context.Background()
	lease, err := store.Begin(ctx, input.Job.ID, 1000)
	if err != nil || store.Complete(ctx, domain.Completion{Lease: lease, FinishedAtMS: 2000, Result: lifecycleResult(t, false)}) != nil {
		t.Fatal("synthetic durable completed result seeding failed")
	}
	snapshot := map[string]any{"id": input.Job.ID, "title": "Stale synthetic JSON", "durationMinutes": 0, "createdAt": input.Job.CreatedAt, "lang": "zh-CN", "enableSpeaker": false, "summaryTemplate": "", "uploadStatus": "uploaded", "asrStatus": "queued", "summaryStatus": "idle", "overallStatus": "queued", "sourceFiles": []map[string]any{{"id": "stale-source", "name": "synthetic.wav", "path": input.SourcePath, "sizeLabel": "1 B", "kind": "audio"}}, "transcriptSegments": []any{}, "speakerSegments": []any{}, "summary": domain.EmptySummary()}
	data, err := json.Marshal(snapshot)
	if err != nil || os.WriteFile(filepath.Join(filepath.Dir(store.path), "jobs", input.Job.ID, "job.json"), data, 0600) != nil {
		t.Fatal("synthetic stale legacy snapshot seeding failed")
	}
	source, err := legacyjobs.Open(filepath.Dir(store.path))
	if err != nil {
		t.Fatal("synthetic legacy source could not open")
	}
	defer source.Close()
	if _, err := store.ImportLegacy(ctx, source); err != nil {
		t.Fatal("synthetic same-ID legacy import failed")
	}
	database := store.path
	if store.Close() != nil {
		t.Fatal("synthetic collision close failed")
	}
	reopened, err := Open(ctx, database)
	if err != nil {
		t.Fatal("synthetic collision reopen failed")
	}
	defer reopened.Close()
	reader, err := legacydb.Open(ctx, database)
	if err != nil {
		t.Fatal("synthetic collision projection failed")
	}
	defer reader.Close()
	result, err := reader.ReadResult(ctx, input.Job.ID)
	if err != nil || result.Job.Title != input.Job.Title || result.Job.OverallStatus != "completed" || len(result.Transcript) != 1 || result.Transcript[0].Text != "Synthetic durable transcript" {
		t.Fatal("first legacy import replaced the authoritative SQLite result")
	}
	var runStatus string
	if reopened.db.QueryRow("SELECT status FROM job_runs WHERE job_id=?", input.Job.ID).Scan(&runStatus) != nil || runStatus != "completed" {
		t.Fatal("legacy import changed the existing execution snapshot")
	}
	lifecycleAssertCompletedAfterReopen(t, reopened, input.Job.ID)
}

func legacyRows(t *testing.T, store *Store, query string, arguments ...any) string {
	t.Helper()
	rows, err := store.db.Query(query, arguments...)
	if err != nil {
		t.Fatal("synthetic legacy preservation query failed")
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal("synthetic legacy preservation columns unavailable")
	}
	values := [][]any{}
	for rows.Next() {
		value := make([]any, len(columns))
		target := make([]any, len(columns))
		for index := range value {
			target[index] = &value[index]
		}
		if rows.Scan(target...) != nil {
			t.Fatal("synthetic legacy preservation row unavailable")
		}
		values = append(values, value)
	}
	data, encodeErr := json.Marshal(values)
	if rows.Err() != nil || encodeErr != nil {
		t.Fatal("synthetic legacy preservation comparison failed")
	}
	return string(data)
}

func TestLegacyImportCountsPreservedRowsAndRetainsRelatedRecordsAcrossRollback(t *testing.T) {
	store, seed := summaryStore(t)
	ctx := context.Background()
	jobID := seed.Execution.Run.JobID
	lease, err := store.Begin(ctx, jobID, 1000)
	if err != nil || store.Complete(ctx, domain.Completion{Lease: lease, FinishedAtMS: 2000, Result: lifecycleResult(t, false)}) != nil {
		t.Fatal("synthetic existing task could not complete")
	}
	seedSummary(t, store, seed)
	deletedID := "job-1700000000000-802"
	input, err := store.Input(ctx, jobID)
	if err != nil {
		t.Fatal("synthetic existing task input unavailable")
	}
	input.Job.ID, input.Job.SourceFiles[0].ID = deletedID, "deleted-source"
	if store.Create(ctx, input) != nil {
		t.Fatal("synthetic deletion task seeding failed")
	}
	operation, err := store.PrepareDeletion(ctx, deletedID)
	if err != nil {
		t.Fatal("synthetic deletion intent seeding failed")
	}
	for _, phase := range []string{"fenced", "trashed", "database_deleted"} {
		if store.AdvanceDeletion(ctx, operation, phase) != nil {
			t.Fatal("synthetic durable deletion transition failed")
		}
	}
	queries := map[string]string{
		"jobs":           "SELECT * FROM jobs WHERE id=? ORDER BY rowid",
		"sources":        "SELECT * FROM job_source_files WHERE job_id=? ORDER BY rowid",
		"segments":       "SELECT * FROM transcript_segments WHERE job_id=? ORDER BY rowid",
		"runs":           "SELECT * FROM job_runs WHERE job_id=? ORDER BY rowid",
		"events":         "SELECT * FROM job_events WHERE job_id=? ORDER BY rowid",
		"summaries":      "SELECT * FROM ai_summary_runs WHERE job_id=? ORDER BY rowid",
		"summary-chunks": "SELECT * FROM ai_summary_run_chunks WHERE run_id IN (SELECT id FROM ai_summary_runs WHERE job_id=?) ORDER BY rowid",
	}
	before := make(map[string]string, len(queries))
	for key, query := range queries {
		before[key] = legacyRows(t, store, query, jobID)
	}
	deletionBefore := legacyRows(t, store, "SELECT * FROM job_deletion_ops ORDER BY rowid")
	existing := application.LegacyJob{Result: domain.Result{Job: domain.JobSummary{ID: jobID, Title: "Stale synthetic replacement", OverallStatus: "queued"}}}
	deleted := existing
	deleted.Result.Job.ID = deletedID
	added := existing
	added.Result.Job.ID = "job-1700000000000-803"
	batch := legacyBatch{jobs: []application.LegacyJob{existing, existing, deleted, added, added}, skipped: 2}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_preserving_import BEFORE INSERT ON app_meta WHEN NEW.key='legacy_jobs_imported' BEGIN SELECT RAISE(ABORT,'synthetic'); END`); err != nil {
		t.Fatal("synthetic import rollback injection failed")
	}
	result, err := store.ImportLegacy(ctx, batch)
	if err == nil || result != (application.LegacyImportResult{}) || legacyRows(t, store, "SELECT id FROM jobs WHERE id=?", added.Result.Job.ID) != "[]" {
		t.Fatal("failed preserving import leaked counts or a partial new task")
	}
	if _, err := store.db.Exec("DROP TRIGGER reject_preserving_import"); err != nil {
		t.Fatal("synthetic import rollback injection cleanup failed")
	}
	result, err = store.ImportLegacy(ctx, batch)
	if err != nil || result.Imported != 1 || result.Skipped != 6 || result.AlreadyImported {
		t.Fatal("preserving import did not count additions, collisions, deletion intents, and source skips separately")
	}
	for key, query := range queries {
		if legacyRows(t, store, query, jobID) != before[key] {
			t.Fatal("legacy collision changed existing SQLite records in " + key)
		}
	}
	if legacyRows(t, store, "SELECT * FROM job_deletion_ops ORDER BY rowid") != deletionBefore || legacyRows(t, store, "SELECT id FROM jobs WHERE id=?", deletedID) != "[]" {
		t.Fatal("legacy collision resurrected or modified a persisted deletion")
	}
	result, err = store.ImportLegacy(ctx, nil)
	if err != nil || result != (application.LegacyImportResult{AlreadyImported: true}) {
		t.Fatal("completed import read the source or reported historical counts")
	}
}

func TestLegacyImportSummaryIdentityCollisionCannotOverwriteAnotherJob(t *testing.T) {
	store, _, input := lifecycleFixture(t)
	addedID := "job-1700000000000-804"
	summaryID := "imported-summary-" + addedID
	if _, err := store.db.Exec(`INSERT INTO ai_summary_runs(id,job_id,include_speaker,include_timestamp,status,result_json,created_at,updated_at) VALUES(?,?,0,0,'completed','{"overview":"Synthetic retained summary"}','2026-11-20T00:00:00Z','2026-11-20T00:00:00Z')`, summaryID, input.Job.ID); err != nil {
		t.Fatal("synthetic existing summary identity seeding failed")
	}
	before := legacyRows(t, store, "SELECT * FROM ai_summary_runs WHERE id=?", summaryID)
	added := application.LegacyJob{Result: domain.Result{Job: domain.JobSummary{ID: addedID, Title: "Synthetic imported task", OverallStatus: "completed"}, Transcript: []domain.Segment{{ID: "synthetic-segment", Text: "Synthetic imported transcript"}}, Summary: domain.Summary{Overview: "Synthetic imported summary"}}}
	result, err := store.ImportLegacy(context.Background(), legacyBatch{jobs: []application.LegacyJob{added}})
	if err == nil || result != (application.LegacyImportResult{}) || legacyRows(t, store, "SELECT * FROM ai_summary_runs WHERE id=?", summaryID) != before || legacyRows(t, store, "SELECT id FROM jobs WHERE id=?", addedID) != "[]" || legacyRows(t, store, "SELECT value FROM app_meta WHERE key='legacy_jobs_imported'") != "[]" {
		t.Fatal("legacy summary collision overwrote existing data or committed a partial import")
	}
}
