package diagnostics_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
	"liberty.local/liberty/internal/infrastructure/repositories/legacydb"
	"liberty.local/liberty/internal/infrastructure/repositories/schema"
	"liberty.local/liberty/internal/infrastructure/runner"
)

// The real subprocess uses only synthetic strings. No shell, user media,
// default profile or system credential service is involved.
func TestDiagnosticOutputChild(t *testing.T) {
	if os.Getenv("LIBERTY_REDACTION_TEST_CHILD") != "1" {
		t.Skip("subprocess helper")
	}
	fmt.Fprintln(os.Stdout, `{'token':'first\'private_tail', 'status':'ready'}`)
	fmt.Fprintln(os.Stdout, `providerBody={"text":"first", "more":["private_tail"]}; ready`)
	fmt.Fprintln(os.Stderr, `open '/synthetic/private_folder private_tail.wav' failed`)
	fmt.Fprintln(os.Stderr, `open '\\synthetic-host\share\private_tail.wav' failed`)
	os.Exit(0)
}

func TestSubprocessDiagnosticsAndFailureRemainRedactedAfterReopen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	directory := t.TempDir()
	path := filepath.Join(directory, "synthetic.sqlite3")
	store, _, err := jobdb.OpenPrepared(ctx, path, nil, true)
	if err != nil {
		t.Fatal("isolated database initialization failed")
	}
	defer store.Close()
	input := domain.JobInput{
		Job:        domain.JobSummary{ID: "job-1700000000000-99", Title: "Synthetic diagnostics", CreatedAt: "2026-11-20T00:00:00.000Z", Lang: "zh", SourceFiles: []domain.SourceFile{{ID: "synthetic-source", Name: "fixture.wav", Kind: "audio", SizeLabel: "1 KB"}}},
		SourcePath: filepath.Join(directory, "fixture.wav"),
	}
	if store.Create(ctx, input) != nil {
		t.Fatal("synthetic job creation failed")
	}
	lease, err := store.Begin(ctx, input.Job.ID, 1000)
	if err != nil {
		t.Fatal("synthetic attempt could not begin")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal("test executable unavailable")
	}
	var lines []string
	err = runner.RunCommand(ctx, executable, []string{"-test.run=^TestDiagnosticOutputChild$"}, directory, map[string]string{"LIBERTY_REDACTION_TEST_CHILD": "1"}, func(line string) error {
		lines = append(lines, line)
		return store.UpdateLog(ctx, lease, strings.Join(lines, "\n"))
	})
	if err != nil || len(lines) != 4 {
		t.Fatal("real subprocess diagnostics did not reach the persistent log")
	}
	log := strings.Join(lines, "\n")
	assertSafeDiagnostic(t, log)
	if strings.Count(log, "ready") != 2 || strings.Count(log, "failed") != 2 {
		t.Fatal("subprocess output lost useful status outside private values")
	}
	if store.Fail(ctx, lease, 2000, nil, `providerBody={"first":"synthetic", "tail":"private_tail"}; failed`, log) != nil {
		t.Fatal("failure projection could not be persisted")
	}
	if store.Close() != nil {
		t.Fatal("database close failed")
	}
	for range 2 {
		reader, err := legacydb.Open(ctx, path)
		if err != nil {
			t.Fatal("persistent result could not be reopened")
		}
		result, err := reader.ReadResult(ctx, input.Job.ID)
		closeErr := reader.Close()
		if err != nil || closeErr != nil || result.ProcessLog == nil || *result.ProcessLog != log || result.Job.FailureReason == nil || result.Job.OverallStatus != "failed" {
			t.Fatal("reopened result lost diagnostic or failure state")
		}
		assertSafeDiagnostic(t, *result.Job.FailureReason)
	}
	// Verify all three persisted failure projections, not just the UI DTO.
	db, err := sql.Open("sqlite", schema.URI(path, url.Values{"mode": {"ro"}}))
	if err != nil {
		t.Fatal("read-only failure audit could not open")
	}
	defer db.Close()
	for _, query := range []string{
		"SELECT failure_reason FROM jobs WHERE id=?",
		"SELECT last_error FROM job_runs WHERE job_id=?",
		"SELECT message FROM job_events WHERE job_id=? AND event_type='failed'",
	} {
		var message string
		if db.QueryRowContext(ctx, query, input.Job.ID).Scan(&message) != nil {
			t.Fatal("persisted failure projection unavailable")
		}
		assertSafeDiagnostic(t, message)
		if !strings.Contains(message, "failed") {
			t.Fatal("failure projection lost its status")
		}
	}
}

func assertSafeDiagnostic(t *testing.T, value string) {
	t.Helper()
	for _, fragment := range []string{"private_tail", "private_folder", "synthetic", "first"} {
		if strings.Contains(value, fragment) {
			t.Fatal("private fixture content reached a diagnostic consumer")
		}
	}
	if !strings.Contains(value, "[redacted") {
		t.Fatal("diagnostic contains no redaction marker")
	}
}
