package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

func TestManualNotesPersistWithoutAIAndExportWithSources(t *testing.T) {
	ctx := context.Background()
	scopes, store, reader, _ := windowUseCases(t, summaryProvider{})
	id := permissionJobIDs[0]
	parent, err := scopes.OpenFromMain(ctx, application.Workbench, "local", id)
	if err != nil {
		t.Fatal(err)
	}
	notes, err := parent.OpenChild(application.MeetingNotes)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := reader.ReadResult(ctx, id)
	data, _ := domain.SummaryJSON(r.PrimarySegments())
	draft := domain.MeetingDraft{Summary: domain.Summary{Overview: "Manual overview", Decisions: []string{"Approved synthetic decision"}}, TranscriptSHA256: domain.SummaryHash(data), Sources: map[string][]string{"decisions": {r.PrimarySegments()[0].ID}}}
	if err := parent.SaveMeetingDraft(ctx, draft); !errors.Is(err, application.ErrWindowScope) {
		t.Fatal("wrong window wrote notes", err)
	}
	if err := notes.SaveMeetingDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	read, err := reader.ReadResult(ctx, id)
	if err != nil || read.Draft == nil || read.Summary.Overview != draft.Summary.Overview || len(read.SummaryRuns) != 0 || read.SummarySourceChanged {
		t.Fatal("read-only projection lost manual notes", err)
	}
	if err := parent.ExportText(ctx, domain.ExportNotes, domain.TextExportLabels{}, func(data []byte) error {
		if !strings.Contains(string(data), draft.Summary.Decisions[0]) {
			t.Fatal("notes export ignored draft")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := parent.ExportWord(ctx, "", func(document domain.MinutesDocument) error {
		found := false
		for _, block := range document.SpeechBlocks {
			for _, line := range block.Summary {
				found = found || line == draft.Summary.Decisions[0]
			}
		}
		if !found {
			t.Fatal("Word export ignored draft")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	segment := r.PrimarySegments()[0]
	if err := parent.EditSegment(ctx, domain.SegmentEdit{Original: segment, Text: "New source fact", Speaker: segment.Speaker}); err != nil {
		t.Fatal(err)
	}
	read, err = reader.ReadResult(ctx, id)
	if err != nil || !read.SummarySourceChanged || read.Summary.Overview != draft.Summary.Overview {
		t.Fatal("source change hid manual notes or failed to warn", err)
	}
	draft.Revision = 1
	if err := store.SaveMeetingDraft(ctx, id, draft); !errors.Is(err, domain.ErrMeetingDraftConflict) {
		t.Fatal("transaction accepted stale transcript", err)
	}
	notes.Close()
	if err := notes.SaveMeetingDraft(ctx, draft); !errors.Is(err, application.ErrWindowScope) {
		t.Fatal("closed editor wrote notes", err)
	}
}
