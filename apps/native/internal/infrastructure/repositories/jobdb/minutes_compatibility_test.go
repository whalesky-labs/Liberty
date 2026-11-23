//go:build compatibility

package jobdb

import (
	"context"
	"liberty.local/liberty/internal/domain"
	"testing"
)

func TestWriteMinutesBackfillPreservesExistingAndHistoricalResult(t *testing.T) {
	s, _ := fixtureStore(t)
	ctx := context.Background()
	id := "job-1700000000000-0"
	run := "synthetic-summary"
	p := domain.MinutesPayload{SchemaVersion: 2, MeetingInfoSource: "user", SourceSummaryRunID: &run, MeetingInfo: domain.MinutesInfo{MeetingName: "Synthetic user edit"}}
	if updated, err := s.BackfillMinutes(ctx, id, run, p); err != nil || updated {
		t.Fatal("valid existing payload overwritten", err)
	}
	if _, err := s.db.Exec("UPDATE ai_summary_runs SET minutes_payload_json='invalid' WHERE id=?", run); err != nil {
		t.Fatal(err)
	}
	var before string
	if s.db.QueryRow("SELECT result_json FROM ai_summary_runs WHERE id=?", run).Scan(&before) != nil {
		t.Fatal("fixture")
	}
	if updated, err := s.BackfillMinutes(ctx, id, run, p); err != nil || !updated {
		t.Fatal("corrupt payload not recovered", err)
	}
	var after string
	if s.db.QueryRow("SELECT result_json FROM ai_summary_runs WHERE id=?", run).Scan(&after) != nil || before != after {
		t.Fatal("backfill rewrote AI result")
	}
	if _, err := s.BackfillMinutes(ctx, "job-1700000000000-4", run, p); err == nil {
		t.Fatal("cross-job run accepted")
	}
	if _, err := s.BackfillMinutes(ctx, id, "synthetic-failed", domain.MinutesPayload{}); err == nil {
		t.Fatal("failed run backfilled")
	}
	if updated, err := s.BackfillMinutes(ctx, id, run, p); err != nil || updated {
		t.Fatal("repeat backfill applied", err)
	}
}
