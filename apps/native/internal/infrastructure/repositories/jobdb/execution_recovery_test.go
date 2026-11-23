package jobdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/repositories/schema"
	"liberty.local/liberty/internal/infrastructure/runner"
)

type lifecycleRunner struct {
	result domain.RunnerResult
	first  *domain.RunnerResult
	calls  atomic.Int32
}

func (r *lifecycleRunner) Run(context.Context, domain.JobInput, domain.RunLease, application.ExecutionCallbacks) (domain.RunnerResult, error) {
	if r.calls.Add(1) == 1 && r.first != nil {
		return *r.first, nil
	}
	return r.result, nil
}

type lifecycleExecutor struct {
	inner *application.Executor
	done  chan error
}

func (e *lifecycleExecutor) Execute(ctx context.Context, id string) error {
	err := e.inner.Execute(ctx, id)
	e.done <- err
	return err
}

func lifecycleFixture(t *testing.T) (*Store, *runner.Files, domain.JobInput) {
	t.Helper()
	directory := t.TempDir()
	store, _, err := OpenPrepared(context.Background(), filepath.Join(directory, "synthetic.sqlite3"), nil, true)
	if err != nil {
		t.Fatal("synthetic store initialization failed")
	}
	t.Cleanup(func() { _ = store.Close() })
	files, err := runner.OpenFiles(directory)
	if err != nil {
		t.Fatal("synthetic job root failed")
	}
	t.Cleanup(func() { _ = files.Close() })
	input := domain.JobInput{Job: domain.JobSummary{ID: "job-1700000000000-800", Title: "Synthetic durable job", CreatedAt: "2026-11-20T00:00:00Z", Lang: "zh-CN", SourceFiles: []domain.SourceFile{{ID: "synthetic-source", Name: "synthetic.wav", Kind: "audio", SizeLabel: "1 B"}}}, SourcePath: filepath.Join(directory, "synthetic.wav")}
	if os.WriteFile(input.SourcePath, []byte("synthetic-media"), 0600) != nil || files.CreateJob(input.Job.ID) != nil || store.Create(context.Background(), input) != nil {
		t.Fatal("synthetic task seeding failed")
	}
	return store, files, input
}

func lifecycleResult(t *testing.T, duplicate bool) domain.RunnerResult {
	t.Helper()
	segments := []map[string]any{{"id": "synthetic-segment", "startMs": 0, "endMs": 1000, "text": "Synthetic durable transcript"}}
	if duplicate {
		segments = append(segments, segments[0])
	}
	data, err := json.Marshal(map[string]any{"protocolVersion": 2, "asrBackend": "funasr", "diarizationRequested": false, "diarizationStatus": "disabled", "warnings": []any{}, "durationMinutes": 1, "transcriptSegments": segments, "speakerSegments": []any{}})
	if err != nil {
		t.Fatal("synthetic result construction failed")
	}
	result, err := runner.ParseResult(data, false, "funasr")
	if err != nil {
		t.Fatal("synthetic runner result was rejected before completion")
	}
	return result
}

func lifecycleAwait(t *testing.T, executor *lifecycleExecutor) error {
	t.Helper()
	select {
	case err := <-executor.done:
		return err
	case <-time.After(8 * time.Second):
		t.Fatal("synthetic execution did not finish within its deadline")
		return nil
	}
}

func lifecycleAssertCompletedAfterReopen(t *testing.T, store *Store, id string) {
	t.Helper()
	database := store.path
	if store.Close() != nil {
		t.Fatal("synthetic task close failed")
	}
	reopened, err := Open(context.Background(), database)
	if err != nil {
		t.Fatal("synthetic task reopen failed")
	}
	defer reopened.Close()
	var jobStatus, runStatus, text string
	var completed, segments int
	if reopened.db.QueryRow("SELECT overall_status FROM jobs WHERE id=?", id).Scan(&jobStatus) != nil || reopened.db.QueryRow("SELECT status FROM job_runs WHERE job_id=?", id).Scan(&runStatus) != nil || reopened.db.QueryRow("SELECT COUNT(*) FROM job_events WHERE job_id=? AND event_type='completed'", id).Scan(&completed) != nil || reopened.db.QueryRow("SELECT COUNT(*),COALESCE(MAX(text),'') FROM transcript_segments WHERE job_id=?", id).Scan(&segments, &text) != nil {
		t.Fatal("synthetic persisted task could not be observed")
	}
	if jobStatus != "completed" || runStatus != "completed" || completed != 1 || segments != 1 || text != "Synthetic durable transcript" {
		t.Fatal("reopen did not retain exactly one completed task and transcript")
	}
}

func TestExecutionPublicationFailureAllowsExplicitRetryAndPersistsOnce(t *testing.T) {
	store, files, input := lifecycleFixture(t)
	bad := lifecycleResult(t, true)
	adapter := &lifecycleRunner{result: lifecycleResult(t, false), first: &bad}
	executor := &lifecycleExecutor{inner: application.NewExecutor(store, adapter, time.Now), done: make(chan error, 4)}
	queue := application.NewScheduler(executor, 1)
	t.Cleanup(func() { _ = queue.Shutdown(context.Background()) })
	if queue.Enqueue(input.Job.ID) != nil || !errors.Is(lifecycleAwait(t, executor), ErrDatabase) {
		t.Fatal("duplicate segment did not reproduce the publication failure")
	}
	reservation, err := queue.Reserve(context.Background(), input.Job.ID)
	if err != nil {
		t.Fatal("failed publication did not leave the scheduler idle")
	}
	reservation.Release(false)
	var status string
	var segments int
	if store.db.QueryRow("SELECT status FROM job_runs WHERE job_id=?", input.Job.ID).Scan(&status) != nil || store.db.QueryRow("SELECT COUNT(*) FROM transcript_segments WHERE job_id=?", input.Job.ID).Scan(&segments) != nil || status != "failed" || segments != 0 || adapter.calls.Load() != 1 {
		t.Fatal("failed publication did not produce a retryable failed row without partial output")
	}
	submission := application.NewSubmission(store, nil, nil, files, queue, "synthetic-runner")
	if submission.Retry(context.Background(), input.Job.ID) != nil || lifecycleAwait(t, executor) != nil {
		t.Fatal("explicit retry could not finish after a publication failure")
	}
	if queue.Shutdown(context.Background()) != nil || adapter.calls.Load() != 2 {
		t.Fatal("explicit retry executed the synthetic runner more than once")
	}
	lifecycleAssertCompletedAfterReopen(t, store, input.Job.ID)
}

func TestExecutionRealSQLiteContentionAutomaticallyResumesQueuedTaskOnce(t *testing.T) {
	store, _, input := lifecycleFixture(t)
	adapter := &lifecycleRunner{result: lifecycleResult(t, false)}
	executor := &lifecycleExecutor{inner: application.NewExecutor(store, adapter, time.Now), done: make(chan error, 8)}
	queue := application.NewScheduler(executor, 1)
	t.Cleanup(func() { _ = queue.Shutdown(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	blocker, err := sql.Open("sqlite", schema.URI(store.path, nil))
	if err != nil {
		t.Fatal("synthetic contention connection failed")
	}
	defer blocker.Close()
	connection, err := blocker.Conn(ctx)
	if err != nil {
		t.Fatal("synthetic contention connection unavailable")
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal("synthetic write contention failed")
	}
	defer connection.ExecContext(context.Background(), "ROLLBACK")
	started := time.Now()
	if queue.Enqueue(input.Job.ID) != nil || !errors.Is(lifecycleAwait(t, executor), ErrDatabase) {
		t.Fatal("contention did not reproduce an input-read failure")
	}
	if time.Since(started) < 4*time.Second || adapter.calls.Load() != 0 {
		t.Fatal("production SQLite lock wait was bypassed or runner started before admission")
	}
	if _, err := connection.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal("synthetic contention was not released")
	}
	if lifecycleAwait(t, executor) != nil || queue.Shutdown(ctx) != nil || adapter.calls.Load() != 1 {
		t.Fatal("queued task did not resume automatically exactly once after the lock was released")
	}
	lifecycleAssertCompletedAfterReopen(t, store, input.Job.ID)
}

type lifecyclePublicationStore struct {
	*Store
	before func(context.Context, domain.Completion) error
}

func (s *lifecyclePublicationStore) Complete(ctx context.Context, completion domain.Completion) error {
	if err := s.before(ctx, completion); err != nil {
		return err
	}
	return s.Store.Complete(ctx, completion)
}

func TestExecutionPublicationCleanupCannotOverwriteNewLeaseDeletionOrCommittedResult(t *testing.T) {
	for _, mode := range []string{"new-lease", "deletion", "already-committed"} {
		t.Run(mode, func(t *testing.T) {
			store, _, input := lifecycleFixture(t)
			adapter := &lifecycleRunner{result: lifecycleResult(t, false)}
			publication := &lifecyclePublicationStore{Store: store}
			publication.before = func(ctx context.Context, completion domain.Completion) error {
				switch mode {
				case "new-lease":
					if store.Requeue(ctx, completion.Lease) != nil {
						t.Error("synthetic old attempt could not be fenced")
						return ErrDatabase
					}
					if _, err := store.Begin(ctx, input.Job.ID, 3000); err != nil {
						t.Error("synthetic replacement attempt could not begin")
						return ErrDatabase
					}
				case "deletion":
					if _, err := store.PrepareDeletion(ctx, input.Job.ID); err != nil {
						t.Error("synthetic deletion could not be prepared")
						return ErrDatabase
					}
				case "already-committed":
					if store.Complete(ctx, completion) != nil {
						t.Error("synthetic ambiguous publication could not commit")
					}
					return ErrDatabase
				}
				return nil
			}
			executor := &lifecycleExecutor{inner: application.NewExecutor(publication, adapter, time.Now), done: make(chan error, 4)}
			queue := application.NewScheduler(executor, 1)
			t.Cleanup(func() { _ = queue.Shutdown(context.Background()) })
			if queue.Enqueue(input.Job.ID) != nil || !errors.Is(lifecycleAwait(t, executor), domain.ErrStaleAttempt) || queue.Shutdown(context.Background()) != nil {
				t.Fatal("publication cleanup lost the authoritative lease fence")
			}
			var status string
			var attempt, failed, completed, deletions int
			if store.db.QueryRow("SELECT status,attempt_id FROM job_runs WHERE job_id=?", input.Job.ID).Scan(&status, &attempt) != nil || store.db.QueryRow("SELECT COUNT(*) FROM job_events WHERE job_id=? AND event_type='failed'", input.Job.ID).Scan(&failed) != nil || store.db.QueryRow("SELECT COUNT(*) FROM job_events WHERE job_id=? AND event_type='completed'", input.Job.ID).Scan(&completed) != nil || store.db.QueryRow("SELECT COUNT(*) FROM job_deletion_ops WHERE job_id=?", input.Job.ID).Scan(&deletions) != nil {
				t.Fatal("synthetic finalization fence could not be observed")
			}
			if failed != 0 || adapter.calls.Load() != 1 {
				t.Fatal("finalization changed another owner's status or replayed work")
			}
			switch mode {
			case "new-lease":
				if status != "running" || attempt != 2 || completed != 0 || deletions != 0 {
					t.Fatal("cleanup overwrote the replacement attempt")
				}
			case "deletion":
				if status != "running" || attempt != 1 || completed != 0 || deletions != 1 {
					t.Fatal("cleanup bypassed the deletion tombstone")
				}
			case "already-committed":
				if status != "completed" || attempt != 1 || completed != 1 || deletions != 0 {
					t.Fatal("cleanup overwrote an already committed result")
				}
				lifecycleAssertCompletedAfterReopen(t, store, input.Job.ID)
			}
		})
	}
}
