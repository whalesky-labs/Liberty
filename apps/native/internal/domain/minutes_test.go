package domain

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestMinutesSourceNeverFallsBackFromRequestedRun(t *testing.T) {
	active := "one"
	wrong := "other"
	speaker := "synthetic-speaker"
	r := Result{Job: JobSummary{ActiveSummaryRunID: &active, DiarizationStatus: "completed"}, Speakers: []Segment{{Speaker: &speaker}}, Summary: Summary{Overview: "legacy"}, SummaryRuns: []SummaryRun{{ID: active, Status: "completed", Result: &SummaryResult{Overview: "plain old summary"}}, {ID: "failed", Status: "failed"}}}
	if _, err := ResolveSummaryExport(r, nil, "missing"); !errors.Is(err, ErrSummaryMissing) {
		t.Fatal("requested run fell back", err)
	}
	if _, err := ResolveSummaryExport(r, nil, "failed"); !errors.Is(err, ErrSummaryIncomplete) {
		t.Fatal("failed run exported")
	}
	source, err := ResolveSummaryExport(r, nil, "")
	if err != nil || !reflect.DeepEqual(source.MinutesPayload.GlobalSummary, []string{"plain old summary"}) || len(source.MinutesPayload.SpeakerReports) != 1 || source.MinutesPayload.SpeakerReports[0].MatchStatus != "missing_from_ai" {
		t.Fatal("missing speaker/unstructured text lost", err)
	}
	p := source.MinutesPayload
	p.SourceSummaryRunID = &wrong
	r.SummaryRuns[0].MinutesPayload = &p
	if _, err := ResolveSummaryExport(r, nil, ""); !errors.Is(err, ErrMinutesSource) {
		t.Fatal("wrong run payload accepted")
	}
	p.SourceSummaryRunID = &active
	p.Participants = nil
	if _, err := ResolveSummaryExport(r, nil, ""); !errors.Is(err, ErrMinutesCoverage) {
		t.Fatal("incomplete speaker coverage accepted")
	}
	p = source.MinutesPayload
	p.SpeakerReports[0].MatchStatus = "matched"
	p.Participants[0].MatchStatus = "matched"
	before := cloneMinutes(p)
	if _, err := ResolveSummaryExport(r, nil, ""); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p, before) {
		t.Fatal("read/export mutated persisted payload")
	}
}
func TestMinutesUserMetadataAndLegacyProjection(t *testing.T) {
	ai := MinutesInfo{MeetingName: "AI", MeetingTime: " 10:00 ", Host: "待补充"}
	p := MinutesPayload{SchemaVersion: 2, MeetingInfoSource: "user", MeetingInfo: MinutesInfo{MeetingName: " user ", MeetingTime: ""}}
	got := ProjectMinutesMetadata(&p, ai)
	if got.Source != "user" || got.Info.MeetingName != "user" || got.Info.MeetingTime != "" {
		t.Fatal("user blank overwritten")
	}
	p.MeetingInfoSource = "ai"
	got = ProjectMinutesMetadata(&p, ai)
	if got.Source != "ai" || got.Info.MeetingName != "AI" || got.Info.Host != "" || got.Info.MeetingTime != "10:00" {
		t.Fatal("AI metadata not normalized")
	}
	p.SchemaVersion = 1
	p.MeetingInfo.MeetingName = " legacy "
	got = ProjectMinutesMetadata(&p, MinutesInfo{})
	if got.Source != "ai" || got.Info.MeetingName != "legacy" {
		t.Fatal("legacy fallback lost")
	}
	got = ProjectMinutesMetadata(nil, MinutesInfo{MeetingName: "待补充", Recorder: "待补充姓名"})
	if got.Source != "empty" || got.Info != (MinutesInfo{}) {
		t.Fatal("placeholder treated as real metadata")
	}
}

func TestReconstructedMinutesRetainUnstructuredBodyAcrossBackfill(t *testing.T) {
	for _, sample := range []struct {
		name     string
		overview string
		global   []string
	}{
		{"plain", "Original unstructured summary", []string{"Original unstructured summary"}},
		{"metadata", "会议名称：合成会议\n会议时间：待补充\nOriginal summary\n1、Second paragraph", []string{"Original summary", "Second paragraph"}},
		{"structured", "发言内容\n【合成部门】：合成人员\n上周总结：\n1、Authoritative speaker content", []string{}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			runID := "synthetic-repeated-export"
			result := Result{Job: JobSummary{ID: "job-1700000000000-621", Title: "Synthetic meeting", ActiveSummaryRunID: &runID}, SummaryRuns: []SummaryRun{{ID: runID, Status: "completed", Result: &SummaryResult{Overview: sample.overview}}}}
			first, err := ResolveSummaryExport(result, nil, "")
			if err != nil || !first.ReconstructedPayload || !reflect.DeepEqual(first.MinutesPayload.GlobalSummary, sample.global) {
				t.Fatal("reconstruction did not persist only meaningful fallback content", first.MinutesPayload.GlobalSummary, err)
			}
			firstDocument := BuildMinutesDocument(result, first, nil)
			persisted := first.MinutesPayload.Clone()
			result.SummaryRuns[0].MinutesPayload = &persisted
			for attempt := range 3 {
				repeated, err := ResolveSummaryExport(result, nil, "")
				if err != nil || repeated.ReconstructedPayload || !reflect.DeepEqual(BuildMinutesDocument(result, repeated, nil), firstDocument) || !reflect.DeepEqual(persisted, first.MinutesPayload) {
					t.Fatal("read-back changed the original document or authoritative payload", attempt, err)
				}
			}
		})
	}
}

func TestPersistedMinutesNeverResurrectClearedUnstructuredBody(t *testing.T) {
	runID := "synthetic-cleared-export"
	result := Result{Job: JobSummary{Title: "Synthetic", ActiveSummaryRunID: &runID}, SummaryRuns: []SummaryRun{{ID: runID, Status: "completed", Result: &SummaryResult{Overview: "Stale unstructured summary"}, MinutesPayload: &MinutesPayload{SchemaVersion: 2, SourceSummaryRunID: &runID, MeetingInfoSource: "user"}}}}
	source, err := ResolveSummaryExport(result, nil, "")
	if err != nil || source.ReconstructedPayload || len(source.MinutesPayload.GlobalSummary) != 0 {
		t.Fatal("persisted payload was reconstructed", err)
	}
	document := BuildMinutesDocument(result, source, nil)
	for _, block := range document.SpeechBlocks {
		if strings.Contains(strings.Join(block.Summary, "\n"), "Stale unstructured summary") {
			t.Fatal("cleared body was repopulated from an old AI response")
		}
	}
	if len(document.FallbackOverview) != 0 || len(document.ClosingSummary) != 0 {
		t.Fatal("cleared body remained exportable")
	}
}
