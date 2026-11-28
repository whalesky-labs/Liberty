package jobdb

import (
	"context"
	"errors"
	"testing"

	"liberty.local/liberty/internal/domain"
)

func TestManualNotesDoNotChangeAIRunsAndUseRevisionConflicts(t *testing.T) {
	ctx := context.Background()
	store, seed := summaryStore(t)
	id := seed.Execution.Run.JobID
	if _, err := store.db.Exec(`INSERT INTO transcript_segments(id,job_id,segment_type,start_ms,end_ms,speaker,text,segment_order) VALUES('segment1',?,'transcript',0,0,NULL,'Synthetic transcript',0)`, id); err != nil {
		t.Fatal(err)
	}
	draft := domain.MeetingDraft{Summary: domain.Summary{Overview: "Manual facts", Decisions: []string{"Confirmed decision"}}, TranscriptSHA256: seed.Execution.TranscriptSHA256}
	if err := store.SaveMeetingDraft(ctx, id, draft); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMeetingDraft(ctx, id, draft); !errors.Is(err, domain.ErrMeetingDraftConflict) {
		t.Fatal("stale create replaced notes", err)
	}
	lease := seedSummary(t, store, seed)
	for i := range seed.Chunks {
		if err := store.SaveSummaryChunk(ctx, lease, completedChunk(seed.Chunks[i]), summaryTestTime); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.CompleteSummary(ctx, lease, summaryCompletion(t, seed)); err != nil {
		t.Fatal(err)
	}
	saved, err := store.MeetingDraft(ctx, id)
	if err != nil || saved.Revision != 1 || saved.Summary.Overview != "Manual facts" {
		t.Fatal("AI completion replaced manual notes", err)
	}
	saved.Summary.Overview = "Updated manually"
	if err := store.SaveMeetingDraft(ctx, id, *saved); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMeetingDraft(ctx, id, *saved); !errors.Is(err, domain.ErrMeetingDraftConflict) {
		t.Fatal("stale revision accepted", err)
	}
	run, err := store.SummaryRun(ctx, id, lease.RunID)
	if err != nil || run.Result == nil || run.Result.Overview != "Synthetic overview" {
		t.Fatal("manual edit overwrote AI history", err)
	}
}
