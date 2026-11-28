package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

func TestCorrectionPersistsExportsAndRejectsStaleOrWrongWindow(t *testing.T) {
	ctx := context.Background()
	scopes, _, reader, _ := windowUseCases(t, summaryProvider{})
	id := permissionJobIDs[0]
	parent, err := scopes.OpenFromMain(ctx, application.Workbench, "local", id)
	if err != nil {
		t.Fatal(err)
	}
	before, err := reader.ReadResult(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	speaker := "Corrected speaker"
	edit := domain.SegmentEdit{Original: before.PrimarySegments()[0], Text: "Corrected fact", Speaker: &speaker}
	notes, _ := parent.OpenChild(application.MeetingNotes)
	if err := notes.EditSegment(ctx, edit); !errors.Is(err, application.ErrWindowScope) {
		t.Fatal("notes window edited a transcript", err)
	}
	if err := parent.EditSegment(ctx, edit); err != nil {
		t.Fatal(err)
	}
	if err := parent.EditSegment(ctx, edit); !errors.Is(err, domain.ErrTranscriptConflict) {
		t.Fatal("stale correction overwrote saved content", err)
	}
	after, err := reader.ReadResult(ctx, id)
	if err != nil || after.PrimarySegments()[0].Text != edit.Text || *after.PrimarySegments()[0].Speaker != speaker || after.Transcript[0].Text != before.Transcript[0].Text {
		t.Fatal("correction did not persist to the authoritative projection")
	}
	var exported string
	if err := parent.ExportText(ctx, domain.ExportTranscript, domain.TextExportLabels{}, func(data []byte) error { exported = string(data); return nil }); err != nil || !strings.Contains(exported, edit.Text) || !strings.Contains(exported, speaker) {
		t.Fatal("export ignored corrected text or attribution", err)
	}
	other, _ := reader.ReadResult(ctx, permissionJobIDs[1])
	edit.Original = other.PrimarySegments()[0]
	if err := parent.EditSegment(ctx, edit); !errors.Is(err, domain.ErrTranscriptConflict) {
		t.Fatal("segment identity escaped job boundary", err)
	}
	parent.Close()
	if err := parent.EditSegment(ctx, edit); !errors.Is(err, application.ErrWindowScope) {
		t.Fatal("revoked window edited transcript", err)
	}
}
