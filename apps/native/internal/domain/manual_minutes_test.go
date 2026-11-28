package domain

import "testing"

func TestManualMinutesPreserveSourceMetadataAndAllEditedSections(t *testing.T) {
	r := Result{Job: JobSummary{Title: "Task title"}, Draft: &MeetingDraft{SourceRunID: "run"}, Summary: Summary{Overview: "Overview", Topics: []string{"Topic"}, Decisions: []string{"Decision"}, ActionItems: []string{"Action"}, Risks: []string{"Risk"}, FollowUps: []string{"Follow-up"}}, SummaryRuns: []SummaryRun{{ID: "run", Result: &SummaryResult{}, MinutesPayload: &MinutesPayload{SchemaVersion: 2, MeetingInfoSource: "user", MeetingInfo: MinutesInfo{MeetingName: "Formal meeting", Recorder: "Recorder", MeetingTime: "2026-11-28", MeetingLocation: "Synthetic room"}}}}}
	d := ManualMinutesDocument(r)
	if d.Title != "Formal meeting" || d.Info.Recorder != "Recorder" || d.Info.MeetingLocation != "Synthetic room" || len(d.SpeechBlocks) != 6 {
		t.Fatal("manual export lost source metadata or edited sections", d)
	}
}
