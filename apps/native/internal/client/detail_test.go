package client

import (
	"liberty.local/liberty/internal/domain"
	"testing"
)

func TestDetailProgressCannotRegressWithinAnAttemptButResetsOnRetry(t *testing.T) {
	start := uint64(10)
	percent := float64(80)
	job := domain.JobSummary{ID: "job", Source: "local", OverallStatus: "transcribing", ProcessingStartedAtMS: &start, ProgressPercent: &percent}
	var p DetailProgress
	if got := p.Update(job); got != 72 {
		t.Fatal(got)
	}
	percent = 20
	if got := p.Update(job); got != 72 {
		t.Fatal("same-attempt regression", got)
	}
	start = 20
	if got := p.Update(job); got != 20 {
		t.Fatal("retry retained prior progress", got)
	}
	job.OverallStatus = "completed"
	if got := p.Update(job); got != 100 {
		t.Fatal(got)
	}
	job.ID = "other"
	job.OverallStatus = "idle"
	job.SummaryStatus = "idle"
	job.ASRStatus = "idle"
	job.UploadStatus = "queued"
	job.ProgressPercent = nil
	if got := p.Update(job); got != 8 {
		t.Fatal("new task retained progress", got)
	}
}
func TestDetailLogsMatchReverseChronologyAndTonePrecedence(t *testing.T) {
	text := "start\r\n\x1b[31mfailed warning completed\x1b[0m\nwarning\r\n已完成\n\n"
	lines := DetailLogs(&text)
	if len(lines) != 4 || lines[0].Tone != "success" || lines[1].Tone != "warning" || lines[2].Tone != "error" || lines[2].Text != "failed warning completed" || lines[3].Text != "start" {
		t.Fatal(lines)
	}
}
