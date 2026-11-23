package jobdb

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

const summaryTestTime = "2026-11-18T00:00:00Z"
const summaryRaw = `{"choices":[{"finish_reason":"stop","message":{"content":"{\"title\":\"Synthetic\",\"overview\":\"Synthetic overview\",\"topics\":[],\"decisions\":[],\"actionItems\":[],\"risks\":[],\"followUps\":[],\"speakerReports\":[],\"globalSummary\":[]}"}}]}`

func summaryStore(t *testing.T) (*Store, domain.SummarySeed) {
	t.Helper()
	ctx := context.Background()
	s, _, err := OpenPrepared(ctx, filepath.Join(t.TempDir(), "liberty.sqlite3"), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r := domain.Result{Job: domain.JobSummary{ID: "job-1700000000000-1", Title: "Synthetic", Source: "local", SourceFiles: []domain.SourceFile{{ID: "f1", Name: "synthetic.wav", Kind: "audio"}}}, Transcript: []domain.Segment{{ID: "segment1", Text: "Synthetic transcript"}}}
	if err := s.Create(ctx, domain.JobInput{Job: r.Job, SourcePath: filepath.Join(t.TempDir(), "synthetic.wav")}); err != nil {
		t.Fatal(err)
	}
	transcript, _ := domain.SummaryJSON(r.PrimarySegments())
	hash := domain.SummaryHash(transcript)
	snapshot, _ := json.Marshal(domain.SummaryExecutionSnapshot{SchemaVersion: 1, Job: domain.SnapshotSummaryJob(r), ModelBaseURL: "https://example.test", ModelName: "synthetic", TemplateID: "template1", SystemPrompt: "Synthetic system", FallbackTitle: "Synthetic"})
	seed := domain.SummarySeed{Execution: domain.SummaryExecution{Run: domain.SummaryRun{ID: "summary-run-1700000000000-1", JobID: r.Job.ID, TemplateID: "template1", CreatedAt: summaryTestTime}, TranscriptRevision: "sha256:" + hash, TranscriptSHA256: hash, TranscriptJSON: string(transcript), SnapshotJSON: string(snapshot), ChunkCount: 2}}
	for i, prompt := range []string{"Synthetic first", "Synthetic second"} {
		seed.Chunks = append(seed.Chunks, domain.SummaryChunk{Index: i, UserPrompt: prompt, SHA256: domain.SummaryHash([]byte(prompt))})
	}
	return s, seed
}
func seedSummary(t *testing.T, s *Store, seed domain.SummarySeed) domain.SummaryLease {
	t.Helper()
	if _, err := s.CreateSummary(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	lease, err := s.ClaimSummary(context.Background(), seed.Execution.Run.JobID, seed.Execution.Run.ID, summaryTestTime)
	if err != nil || lease == nil {
		t.Fatal("claim", err)
	}
	return *lease
}
func completedChunk(c domain.SummaryChunk) domain.SummaryChunk {
	c.Raw = summaryRaw
	parsed, _ := domain.ParseSummaryStructured(c.Raw)
	encoded, _ := domain.SummaryJSON(parsed)
	c.Structured = string(encoded)
	return c
}
func summaryCompletion(t *testing.T, seed domain.SummarySeed) domain.SummaryCompletion {
	t.Helper()
	snapshot, err := seed.Execution.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	result, err := domain.ParseSummaryResponse(summaryRaw, "Synthetic")
	if err != nil {
		t.Fatal(err)
	}
	minutes, err := domain.ReconstructMinutes(snapshot.Job.Result(), result, nil, "template1", &seed.Execution.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return domain.SummaryCompletion{Raw: summaryRaw, Result: result, Minutes: minutes, CompletedAt: summaryTestTime, Diagnostics: `{"schemaVersion":1}`}
}
func TestSummarySeedAtomicValidationAndNullResults(t *testing.T) {
	s, seed := summaryStore(t)
	bad := seed
	bad.Chunks = append([]domain.SummaryChunk{}, seed.Chunks...)
	bad.Chunks[1] = bad.Chunks[0]
	if _, err := s.CreateSummary(context.Background(), bad); !errors.Is(err, domain.ErrSummaryExecution) {
		t.Fatal(err)
	}
	var count int
	if s.db.QueryRow("SELECT COUNT(*) FROM ai_summary_runs").Scan(&count) != nil || count != 0 {
		t.Fatal("invalid seed partially persisted")
	}
	run, err := s.CreateSummary(context.Background(), seed)
	if err != nil || run.Status != "running" {
		t.Fatal(err)
	}
	var result, minutes *string
	if s.db.QueryRow("SELECT result_json,minutes_payload_json FROM ai_summary_runs WHERE id=?", run.ID).Scan(&result, &minutes) != nil || result != nil || minutes != nil {
		t.Fatal("missing result was JSON null rather than SQL NULL")
	}
	seed.Execution.Run.ID = "summary-run-1700000000000-2"
	same, err := s.CreateSummary(context.Background(), seed)
	if err != nil || same.ID != run.ID {
		t.Fatal("duplicate live run", err)
	}
}
func TestSummaryChunksAndPublicationAreLeaseFenced(t *testing.T) {
	ctx := context.Background()
	s, seed := summaryStore(t)
	first := seedSummary(t, s, seed)
	current, err := s.ClaimSummary(ctx, first.JobID, first.RunID, summaryTestTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadSummaryExecution(ctx, first); !errors.Is(err, domain.ErrSummaryLease) {
		t.Fatal("stale load", err)
	}
	if _, err := s.SummaryChunks(ctx, first); !errors.Is(err, domain.ErrSummaryLease) {
		t.Fatal("stale chunks", err)
	}
	if err := s.SaveSummaryChunk(ctx, first, completedChunk(seed.Chunks[0]), summaryTestTime); !errors.Is(err, domain.ErrSummaryLease) {
		t.Fatal("stale write", err)
	}
	wrong := *current
	wrong.JobID = "job-1700000000000-2"
	if err := s.FailSummary(ctx, wrong, "ai_request_failed", summaryTestTime); !errors.Is(err, domain.ErrSummaryLease) {
		t.Fatal("cross-job fail", err)
	}
	completion := summaryCompletion(t, seed)
	if err := s.CompleteSummary(ctx, *current, completion); err == nil {
		t.Fatal("partial chunks published")
	}
	for _, c := range seed.Chunks {
		if err := s.SaveSummaryChunk(ctx, *current, completedChunk(c), summaryTestTime); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_summary_event BEFORE INSERT ON job_events WHEN NEW.event_type='ai_summary_completed' BEGIN SELECT RAISE(ABORT,'synthetic'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteSummary(ctx, *current, completion); err == nil {
		t.Fatal("publication failure ignored")
	}
	run, err := s.SummaryRun(ctx, first.JobID, first.RunID)
	if err != nil || run.Status != "running" || run.Result != nil {
		t.Fatal("partial completion committed", err)
	}
	if _, err := s.db.Exec("DROP TRIGGER reject_summary_event"); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteSummary(ctx, *current, completion); err != nil {
		t.Fatal(err)
	}
	var active *string
	var status string
	if s.db.QueryRow("SELECT active_summary_run_id,summary_status FROM jobs WHERE id=?", first.JobID).Scan(&active, &status) != nil || active == nil || *active != first.RunID || status != "completed" {
		t.Fatal("publication not observable on job")
	}
	if err := s.CompleteSummary(ctx, *current, completion); !errors.Is(err, domain.ErrSummaryLease) {
		t.Fatal("duplicate terminal publish", err)
	}
	if err := s.DeleteSummary(ctx, first.JobID, first.RunID); err != nil {
		t.Fatal(err)
	}
	if s.db.QueryRow("SELECT active_summary_run_id,summary_status FROM jobs WHERE id=?", first.JobID).Scan(&active, &status) != nil || active != nil || status != "idle" {
		t.Fatal("last deletion retained stale active pointer")
	}
}
func TestSummaryDeletionAndOverflowGuards(t *testing.T) {
	ctx := context.Background()
	s, seed := summaryStore(t)
	lease := seedSummary(t, s, seed)
	if _, err := s.db.Exec("UPDATE ai_summary_runs SET attempt_id=?,lease_token=? WHERE id=?", int64(math.MaxInt64), int64(math.MaxInt64), lease.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimSummary(ctx, lease.JobID, lease.RunID, summaryTestTime); err == nil {
		t.Fatal("overflow accepted")
	}
	var attempt int64
	if s.db.QueryRow("SELECT attempt_id FROM ai_summary_runs WHERE id=?", lease.RunID).Scan(&attempt) != nil || attempt != math.MaxInt64 {
		t.Fatal("overflow changed integer storage")
	}
	if _, err := s.PrepareDeletion(ctx, lease.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimSummary(ctx, lease.JobID, lease.RunID, summaryTestTime); !errors.Is(err, application.ErrJobNotFound) {
		t.Fatal("deleting job claimed", err)
	}
	if _, err := s.LoadSummaryExecution(ctx, lease); !errors.Is(err, domain.ErrSummaryLease) {
		t.Fatal("deleting job loaded", err)
	}
	runs, err := s.RecoverableSummaries(ctx)
	if err != nil || len(runs) != 0 {
		t.Fatal("deleting job recovered", err)
	}
}
func TestSummaryRejectsMalformedStructuredChunkAndResult(t *testing.T) {
	ctx := context.Background()
	s, seed := summaryStore(t)
	lease := seedSummary(t, s, seed)
	chunk := completedChunk(seed.Chunks[0])
	chunk.Structured = chunk.Raw
	if err := s.SaveSummaryChunk(ctx, lease, chunk, summaryTestTime); !errors.Is(err, domain.ErrSummaryExecution) {
		t.Fatal("envelope stored as structured result", err)
	}
	if err := s.FailSummary(ctx, lease, "https://provider.example.test/private-error", summaryTestTime); err != nil {
		t.Fatal(err)
	}
	run, err := s.SummaryRun(ctx, lease.JobID, lease.RunID)
	if err != nil || run.ErrorMessage == nil || *run.ErrorMessage != "ai_execution_failed" {
		t.Fatal("unsanitized failure persisted")
	}
	if _, err := s.db.Exec("UPDATE ai_summary_runs SET status='completed',result_json='null' WHERE id=?", lease.RunID); err != nil {
		t.Fatal(err)
	}
	if err := s.SelectSummary(ctx, lease.JobID, lease.RunID); !errors.Is(err, domain.ErrSummaryMissing) {
		t.Fatal("malformed result selected", err)
	}
}
