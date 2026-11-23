package runner_test

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/execution"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
	"liberty.local/liberty/internal/infrastructure/repositories/legacydb"
	"liberty.local/liberty/internal/infrastructure/repositories/schema"
	"liberty.local/liberty/internal/infrastructure/runner"
)

type v1TestRuntime struct{ value execution.Runtime }

func (r v1TestRuntime) Resolve(context.Context) (execution.Runtime, error) { return r.value, nil }

type v1TestStore struct {
	*jobdb.Store
	directory string
}

func (s v1TestStore) Attach(ctx context.Context, lease domain.RunLease, pid uint32, identity string, now int64) error {
	if err := s.Store.Attach(ctx, lease, pid, identity, now); err != nil {
		return err
	}
	// Release the fixture only after the child identity is stored for this attempt.
	return os.WriteFile(filepath.Join(s.directory, "jobs", lease.JobID, lease.OutputDir, "synthetic-permit"), nil, 0600)
}

func TestV1FailureCannotPersistSuccessAndExplicitRetryKeepsExactTranscript(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	directory := t.TempDir()
	database := filepath.Join(directory, "synthetic.sqlite3")
	inputPath, script := filepath.Join(directory, "synthetic.wav"), filepath.Join(directory, "synthetic-runner")
	if os.WriteFile(inputPath, []byte("synthetic"), 0600) != nil || os.WriteFile(script, nil, 0600) != nil {
		t.Fatal("synthetic files unavailable")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal("test executable unavailable")
	}
	if runtime.GOOS != "windows" {
		// Keep a stable launcher alive through the attachment/permit fence.
		// The synthetic Go helper's race-runtime startup must not change the
		// process image whose identity this persistence test is recording.
		t.Setenv("LIBERTY_V1_TEST_EXECUTABLE", executable)
		launcher := "\"$LIBERTY_V1_TEST_EXECUTABLE\" \"$@\"\nfixture_status=$?\nexit \"$fixture_status\"\n"
		if os.WriteFile(script, []byte(launcher), 0600) != nil {
			t.Fatal("synthetic launcher unavailable")
		}
		executable = "/bin/sh"
	}
	files, err := runner.OpenFiles(directory)
	if err != nil {
		t.Fatal("isolated attempt root unavailable")
	}
	defer files.Close()
	store, _, err := jobdb.OpenPrepared(ctx, database, nil, true)
	if err != nil {
		t.Fatal("isolated database unavailable")
	}
	defer func() { _ = store.Close() }()
	input := domain.JobInput{Job: domain.JobSummary{ID: "job-1700000000000-801", Title: "Synthetic V1 execution", CreatedAt: "2026-11-20T00:00:00Z", Lang: "zh", SourceFiles: []domain.SourceFile{{ID: "synthetic-source", Name: "synthetic.wav", Kind: "audio", SizeLabel: "1 B"}}}, SourcePath: inputPath}
	if store.Create(ctx, input) != nil {
		t.Fatal("synthetic task creation failed")
	}
	runtime := v1TestRuntime{execution.Runtime{Python: executable, Script: script, Backend: "funasr", Threads: 1, BatchSeconds: 1}}
	adapter := &execution.Runner{Files: files, Runtime: runtime}
	execute := func(mode string) error {
		t.Setenv("LIBERTY_RUNNER_TEST_CHILD", mode)
		return application.NewExecutor(v1TestStore{Store: store, directory: directory}, adapter, time.Now).Execute(ctx, input.Job.ID)
	}
	err = execute("v1_failure_alias")
	if !errors.Is(err, domain.ErrRunnerResult) {
		t.Fatalf("V1 failure execution returned %v, want %v", err, domain.ErrRunnerResult)
	}
	for range 2 {
		if store.Close() != nil {
			t.Fatal("failure close failed")
		}
		store, err = jobdb.Open(ctx, database)
		if err != nil {
			t.Fatal("failure reopen failed")
		}
		result := readV1PersistedResult(t, ctx, database, input.Job.ID)
		if result.Job.OverallStatus != "failed" || len(result.Transcript) != 0 || result.Job.FailureReason == nil || *result.Job.FailureReason != "runner_execution_failed" {
			t.Fatal("failure state or safe failure reason did not survive reopen")
		}
		assertV1RunState(t, ctx, database, "failed", 1)
	}
	if store.Retry(ctx, input.Job.ID, "", "") != nil {
		t.Fatal("explicit retry rejected")
	}
	if err := execute("v1_exact"); err != nil {
		t.Fatalf("valid V1 retry did not complete: %v", err)
	}
	for range 2 {
		if store.Close() != nil {
			t.Fatal("success close failed")
		}
		store, err = jobdb.Open(ctx, database)
		if err != nil {
			t.Fatal("success reopen failed")
		}
		result := readV1PersistedResult(t, ctx, database, input.Job.ID)
		if result.Job.OverallStatus != "completed" || result.Job.FailureReason != nil || len(result.Transcript) != 1 || result.Transcript[0].Text != "Exact V1 transcript" || result.Job.RunnerProtocolVersion == nil || *result.Job.RunnerProtocolVersion != 1 || result.Job.DiarizationStatus != "disabled" {
			t.Fatal("valid retry did not retain exact transcript and V1 normalization")
		}
		assertV1RunState(t, ctx, database, "completed", 2)
	}
}

func readV1PersistedResult(t *testing.T, ctx context.Context, database, id string) domain.Result {
	t.Helper()
	reader, err := legacydb.Open(ctx, database)
	if err != nil {
		t.Fatal("result reader unavailable")
	}
	defer reader.Close()
	result, err := reader.ReadResult(ctx, id)
	if err != nil {
		t.Fatal("persisted result unavailable")
	}
	return result
}

func assertV1RunState(t *testing.T, ctx context.Context, database, status string, attempt int64) {
	t.Helper()
	db, err := sql.Open("sqlite", schema.URI(database, url.Values{"mode": {"ro"}}))
	if err != nil {
		t.Fatal("read-only state audit unavailable")
	}
	defer db.Close()
	var got string
	var number int64
	if db.QueryRowContext(ctx, "SELECT status,attempt_id FROM job_runs").Scan(&got, &number) != nil || got != status || number != attempt {
		t.Fatal("persisted lease state differs from execution outcome")
	}
	var completed int
	if db.QueryRowContext(ctx, "SELECT COUNT(*) FROM job_events WHERE event_type='completed'").Scan(&completed) != nil || completed != int(attempt-1) {
		t.Fatal("failed attempt published a completion event or retry duplicated completion")
	}
	rows, err := db.QueryContext(ctx, "SELECT message FROM job_events")
	if err != nil {
		t.Fatal("event audit unavailable")
	}
	defer rows.Close()
	for rows.Next() {
		var message string
		if rows.Scan(&message) != nil || strings.Contains(message, "synthetic actual failure") || strings.Contains(message, "alias") {
			t.Fatal("untrusted Runner text reached durable events")
		}
	}
	if rows.Err() != nil {
		t.Fatal("event audit interrupted")
	}
}
