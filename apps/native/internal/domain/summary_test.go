package domain

import "testing"

func TestSummarySelectionPreservesCompletedResultAndActionFormatting(t *testing.T) {
	selected := "failed-new"
	r := Result{Job: JobSummary{ActiveSummaryRunID: &selected}, SummaryRuns: []SummaryRun{
		{ID: "failed-new", Status: "failed", UpdatedAt: "3"},
		{ID: "completed-new", Status: "completed", UpdatedAt: "2", Result: &SummaryResult{Overview: "new", ActionItems: []SummaryAction{{Task: "follow up", Owner: " Alice ", DueDate: "tomorrow"}, {Task: "keep", Owner: " "}}}},
		{ID: "completed-old", Status: "completed", UpdatedAt: "1", Result: &SummaryResult{Overview: "old"}},
	}}
	r.HydrateSummary()
	if r.Job.ActiveSummaryRunID == nil || *r.Job.ActiveSummaryRunID != "completed-new" || r.Summary.Overview != "new" || r.Summary.ActionItems[0] != "follow up（ Alice  / tomorrow）" || r.Summary.ActionItems[1] != "keep" {
		t.Fatal("invalid active summary projection")
	}
	selected = "completed-old"
	r.Job.ActiveSummaryRunID = &selected
	r.HydrateSummary()
	if r.Summary.Overview != "old" {
		t.Fatal("explicit selection not honored")
	}
	r.SummaryRuns[2].Result = nil
	r.HydrateSummary()
	if r.Summary.Overview != "new" {
		t.Fatal("corrupt selected run hid a valid result")
	}
}
func TestSummaryLatestTieAndEmptyQueuedFallback(t *testing.T) {
	r := Result{SummaryRuns: []SummaryRun{{ID: "first", Status: "failed", UpdatedAt: "2"}, {ID: "second", Status: "running", UpdatedAt: "2"}}}
	r.HydrateSummary()
	if r.Job.ActiveSummaryRunID == nil || *r.Job.ActiveSummaryRunID != "second" {
		t.Fatal("Rust max_by chooses last equal timestamp")
	}
	r.SummaryRuns = nil
	r.Job.SummaryStatus = "queued"
	r.HydrateSummary()
	if r.Job.ActiveSummaryRunID != nil || r.Job.SummaryStatus != "idle" || r.Summary.Topics == nil {
		t.Fatal("empty legacy summary not normalized")
	}
}
