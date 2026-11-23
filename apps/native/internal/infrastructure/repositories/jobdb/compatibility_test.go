//go:build compatibility

package jobdb

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/repositories/legacydb"
)

func fixtureStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if dir == "" {
		t.Fatal("run pnpm native:compat")
	}
	// Source is the closed, synthetic Rust database; never copy a live profile.
	bytes, err := os.ReadFile(filepath.Join(dir, "liberty.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "liberty.sqlite3")
	if err = os.WriteFile(path, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}
func syntheticInput() domain.JobInput {
	return domain.JobInput{Job: domain.JobSummary{ID: "job-1700000000000-99", Title: "Synthetic execution", CreatedAt: "2026-11-17T00:00:00.000Z", Lang: "zh", EnableSpeaker: false,
		SourceFiles: []domain.SourceFile{{ID: "synthetic-source-99", Name: "fixture.wav", Kind: "audio", SizeLabel: "1 KB"}}}, SourcePath: filepath.Join(os.TempDir(), "synthetic-fixture.wav")}
}
func completion(l domain.RunLease) domain.Completion {
	return domain.Completion{Lease: l, FinishedAtMS: 4000, Result: domain.RunnerResult{ProtocolVersion: 2, ASRBackend: "funasr", DiarizationStatus: "disabled", Warnings: []domain.RunnerWarning{}, Speakers: []domain.Segment{}, Transcript: []domain.Segment{{ID: "s1", StartMS: 0, EndMS: 60001, Text: "Synthetic transcript"}}}}
}
func TestWriteLifecycleOnRustSchema(t *testing.T) {
	ctx := context.Background()
	s, path := fixtureStore(t)
	input := syntheticInput()
	if other, err := Open(ctx, path); !errors.Is(err, profile.ErrInUse) {
		if other != nil {
			other.Close()
		}
		t.Fatal("concurrent profile writer allowed")
	}
	if err := s.Create(ctx, input); err != nil {
		t.Fatal(err)
	}
	lease, err := s.Begin(ctx, input.Job.ID, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if lease.AttemptID != 1 || lease.Token != 1 || lease.OutputDir != "attempts/attempt-1-1" {
		t.Fatal("initial lease mismatch")
	}
	if _, err := s.Begin(ctx, input.Job.ID, 1001); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("duplicate execution accepted")
	}
	if err := s.Attach(ctx, lease, 123, "synthetic-process-identity", 1500); err != nil {
		t.Fatal(err)
	}
	if err := s.Heartbeat(ctx, lease, 2000); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(ctx, completion(lease)); err != nil {
		t.Fatal(err)
	}
	reader, err := legacydb.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	r, err := reader.ReadResult(ctx, input.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Job.OverallStatus != "completed" || r.Job.DurationMinutes != 2 || r.Transcript[0].Text != "Synthetic transcript" || r.Transcript[0].ID != input.Job.ID+":transcript:s1" {
		t.Fatal("persisted completion differs")
	}
	if err := s.Complete(ctx, completion(lease)); !errors.Is(err, domain.ErrStaleAttempt) {
		t.Fatal("duplicate completion accepted")
	}
	if err := s.Retry(ctx, input.Job.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	next, err := s.Begin(ctx, input.Job.ID, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if next.AttemptID != 2 || next.Token != 2 {
		t.Fatal("retry did not advance lease")
	}
	if err := s.Fail(ctx, lease, 6000, nil, "late-error", ""); !errors.Is(err, domain.ErrStaleAttempt) {
		t.Fatal("old failure overwrote retry")
	}
	if err := s.Requeue(ctx, next); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.Begin(ctx, input.Job.ID, 7000)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.AttemptID != 3 || recovered.Token != 4 {
		t.Fatal("recovery did not fence old result")
	}
	if err := s.Fail(ctx, recovered, 8000, nil, "synthetic_failure", ""); err != nil {
		t.Fatal(err)
	}
	var events int
	if s.db.QueryRow(`SELECT count(*) FROM job_events WHERE job_id=?`, input.Job.ID).Scan(&events) != nil || events != 7 {
		t.Fatalf("event history lost: %d", events)
	}
}
func TestWriteCompletionRollbackAndDeletionRecovery(t *testing.T) {
	ctx := context.Background()
	s, path := fixtureStore(t)
	input := syntheticInput()
	if err := s.Create(ctx, input); err != nil {
		t.Fatal(err)
	}
	l, err := s.Begin(ctx, input.Job.ID, 1000)
	if err != nil {
		t.Fatal(err)
	}
	bad := completion(l)
	bad.Result.Transcript = append(bad.Result.Transcript, bad.Result.Transcript[0]) // duplicate SQL primary key
	if err := s.Complete(ctx, bad); err == nil {
		t.Fatal("invalid completion committed")
	}
	if err := s.Heartbeat(ctx, l, 2000); err != nil {
		t.Fatal("failed transaction consumed lease")
	}
	if err := s.Attach(ctx, l, 123, "synthetic-process-identity", 2100); err != nil {
		t.Fatal(err)
	}
	d, err := s.PrepareDeletion(ctx, input.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := s.PrepareDeletion(ctx, input.Job.ID)
	if err != nil || !reflect.DeepEqual(d, repeated) {
		t.Fatal("deletion not idempotent")
	}
	if err := s.Complete(ctx, completion(l)); !errors.Is(err, domain.ErrStaleAttempt) {
		t.Fatal("deletion raced with completion")
	}
	if err := s.AdvanceDeletion(ctx, d, "database_deleted"); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("out-of-order deletion accepted")
	}
	reader, err := legacydb.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.ReadResult(ctx, input.Job.ID); !errors.Is(err, application.ErrJobNotFound) {
		t.Fatal("pending deletion visible")
	}
	for _, phase := range []string{"fenced", "trashed", "database_deleted"} {
		if err := s.AdvanceDeletion(ctx, d, phase); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	resumed, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	operations, err := resumed.PendingDeletions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, op := range operations {
		if op.ID == d.ID {
			found = op.Phase == "database_deleted" && op.PID != nil && *op.PID == 123
		}
	}
	if !found {
		t.Fatal("deletion compensation identity lost on restart")
	}
	if err := resumed.FinishDeletion(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := resumed.FinishDeletion(ctx, d); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"jobs", "job_runs", "transcript_segments", "job_source_files", "job_events", "ai_summary_runs"} {
		column := "job_id"
		if table == "jobs" {
			column = "id"
		}
		var count int
		if resumed.db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE "+column+"=?", input.Job.ID).Scan(&count) != nil || count != 0 {
			t.Fatal("orphan data in " + table)
		}
	}
}
func TestWriteRejectsUnsupportedSchemaWithoutDDL(t *testing.T) {
	_, path := fixtureStore(t)
	// Independent test db to avoid ownership conflict, preserving the real schema.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	future := filepath.Join(t.TempDir(), "future.sqlite3")
	if os.WriteFile(future, data, 0600) != nil {
		t.Fatal("write fixture")
	}
	db, err := sql.Open("sqlite", future)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE app_meta SET value='99' WHERE key='schema_version'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, _ := os.ReadFile(future)
	if s, err := Open(context.Background(), future); !errors.Is(err, legacydb.ErrSchema) {
		if s != nil {
			s.Close()
		}
		t.Fatal("future schema accepted")
	}
	after, _ := os.ReadFile(future)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("rejected database modified")
	}
}
