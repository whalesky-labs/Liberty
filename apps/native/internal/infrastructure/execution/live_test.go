package execution

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/runner"
)

type liveFixture struct {
	job    domain.JobSummary
	lease  domain.RunLease
	calls  int
	change bool
}

func (f *liveFixture) ListJobs(context.Context) ([]domain.JobSummary, error) {
	return []domain.JobSummary{f.job}, nil
}
func (f *liveFixture) ReadResult(context.Context, string) (domain.Result, error) {
	return domain.Result{Job: f.job}, nil
}
func (f *liveFixture) ActiveLease(context.Context, string) (*domain.RunLease, error) {
	f.calls++
	l := f.lease
	if f.change && f.calls%2 == 0 {
		l.Token++
	}
	return &l, nil
}
func TestLiveProgressUsesCurrentAttemptAndCannotPublishTerminalState(t *testing.T) {
	dir := t.TempDir()
	files, err := runner.OpenFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	l := domain.RunLease{JobID: "job-1700000000000-99", AttemptID: 2, Token: 3}
	if err := files.CreateJob(l.JobID); err != nil {
		t.Fatal(err)
	}
	attempt, err := files.CreateAttempt(l)
	if err != nil {
		t.Fatal(err)
	}
	f := &liveFixture{job: domain.JobSummary{ID: l.JobID, OverallStatus: "transcribing"}, lease: l}
	reader := &LiveReader{Reader: f, Runs: f, Files: files}
	write := func(stage string) {
		t.Helper()
		percent := 92
		if stage == "completed" {
			percent = 100
		}
		data, _ := json.Marshal(map[string]any{"protocolVersion": 2, "revision": 2, "stage": stage, "progressPercent": percent, "statusMessage": "api_key=hidden_value /synthetic/private.wav", "failureReason": nil, "updatedAt": "2026-11-17T00:00:00Z"})
		if _, err := runner.ParseProgress(data); err != nil {
			t.Fatal("invalid test progress", err)
		}
		if os.WriteFile(filepath.Join(attempt, "progress.json"), data, 0600) != nil {
			t.Fatal("progress fixture")
		}
	}
	write("speaker_processing")
	result, err := reader.ReadResult(context.Background(), l.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Job.OverallStatus != "speaker_processing" || result.Job.ProgressPercent == nil || *result.Job.ProgressPercent != 92 {
		t.Fatal("active progress missing")
	}
	if result.Job.ProgressMessage == nil || strings.Contains(*result.Job.ProgressMessage, "hidden") || strings.Contains(*result.Job.ProgressMessage, "private.wav") {
		t.Fatal("private diagnostic leaked")
	}
	f.change = true
	result, err = reader.ReadResult(context.Background(), l.JobID)
	if err != nil || result.Job.OverallStatus != "transcribing" || result.Job.ProgressPercent != nil {
		t.Fatal("stale attempt overlaid current task")
	}
	f.change = false
	write("completed")
	result, err = reader.ReadResult(context.Background(), l.JobID)
	if err != nil || result.Job.OverallStatus == "completed" {
		t.Fatal("uncommitted completion exposed")
	}
	f.job.OverallStatus = "failed"
	f.calls = 0
	result, err = reader.ReadResult(context.Background(), l.JobID)
	if err != nil || result.Job.OverallStatus != "failed" || f.calls != 0 {
		t.Fatal("terminal snapshot overridden by stale progress")
	}
}
