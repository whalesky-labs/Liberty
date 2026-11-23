package domain

import (
	"testing"
	"time"
)

func TestRemoteDashboardKeepsOriginalMetricsAndExcludesLocalResources(t *testing.T) {
	seconds := uint32(12)
	exported := "2026-11-18"
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.FixedZone("synthetic", 8*3600))
	results := []Result{
		{Job: JobSummary{ID: "complete", CreatedAt: "2026-11-18T02:00:00Z", OverallStatus: "completed", DurationMinutes: 4, ProcessingDurationSeconds: &seconds, ASRStatus: "completed", EnableSpeaker: true, DiarizationStatus: "completed", LastExportedAt: &exported}, SummaryRuns: []SummaryRun{{Status: "completed", Result: &SummaryResult{}}}},
		{Job: JobSummary{ID: "failed", CreatedAt: "2026-11-17T20:00:00Z", OverallStatus: "failed", Warnings: []RunnerWarning{{Code: "synthetic"}}, DurationMinutes: 2}},
		{Job: JobSummary{ID: "old", CreatedAt: "2026-10-01T00:00:00Z", OverallStatus: "completed"}},
		{Job: JobSummary{ID: "queued", CreatedAt: "2026-11-18T03:00:00Z", OverallStatus: "queued"}},
	}
	o, err := RemoteDashboard(results, "today", now)
	if err != nil {
		t.Fatal(err)
	}
	if o.Metrics.TotalJobs != 3 || o.Metrics.CompletedJobs != 1 || o.Metrics.FailedJobs != 1 || o.Metrics.ActiveJobs != 1 || o.Metrics.WarningJobs != 1 || o.Metrics.MediaDurationMinutes != 6 || o.Metrics.ProcessingDurationSeconds != 12 || o.Metrics.SummaryReadyJobs != 1 || o.Metrics.TranscriptReadyJobs != 1 || o.Metrics.SpeakerReadyJobs != 1 || o.Metrics.SpeakerEligibleJobs != 1 || o.Metrics.ExportedJobs != 1 {
		t.Fatal(o.Metrics)
	}
	if o.Companion != nil || o.Resources != (DashboardResources{}) || len(o.AttentionJobs) != 2 || o.AttentionJobs[0].ID != "queued" || len(o.RecentResults) != 1 || len(o.Trend) != 3 || o.Trend[0].Period != "04:00" {
		t.Fatal(o)
	}
	all, err := RemoteDashboard(results, "all", now)
	if err != nil || all.Metrics.TotalJobs != 4 || len(all.Trend) != 2 || all.TrendGranularity != "month" {
		t.Fatal(all, err)
	}
}
