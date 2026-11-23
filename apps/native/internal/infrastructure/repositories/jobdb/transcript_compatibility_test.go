//go:build compatibility

package jobdb

import (
	"context"
	"reflect"
	"testing"

	"liberty.local/liberty/internal/infrastructure/repositories/legacydb"
)

func TestWriteSpeakerRenamePreservesTranscriptAndAISnapshots(t *testing.T) {
	ctx := context.Background()
	s, path := fixtureStore(t)
	r, err := legacydb.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	id := "job-1700000000000-0"
	before, err := r.ReadResult(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if s.RenameSpeaker(ctx, id, "speaker-0", "  合成人员  ") != nil {
		t.Fatal("rename failed")
	}
	after, err := r.ReadResult(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Transcript, after.Transcript) || !reflect.DeepEqual(before.SummaryRuns, after.SummaryRuns) {
		t.Fatal("rename changed source transcript or historical summaries")
	}
	if len(after.Speakers) != 1 || after.Speakers[0].Speaker == nil || *after.Speakers[0].Speaker != "合成人员" {
		t.Fatal("speaker projection was not renamed")
	}
	other, err := r.ReadResult(ctx, "job-1700000000000-4")
	if err != nil || *other.Speakers[0].Speaker != "speaker-0" {
		t.Fatal("rename escaped job scope")
	}
	if s.RenameSpeaker(ctx, id, "合成人员", " ") == nil {
		t.Fatal("blank name accepted")
	}
	if _, err := s.db.Exec(`UPDATE transcript_segments SET speaker=NULL WHERE job_id=? AND segment_type='speaker'`, id); err != nil {
		t.Fatal(err)
	}
	if s.RenameSpeaker(ctx, id, "", "Unknown replacement") != nil {
		t.Fatal("null speaker rename")
	}
	if s.RenameSpeaker(ctx, "job-1700000000000-14", "speaker-0", "forbidden") == nil {
		t.Fatal("pending deletion accepted mutation")
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_speaker BEFORE UPDATE ON transcript_segments WHEN NEW.speaker='rejected' BEGIN SELECT RAISE(ABORT,'synthetic'); END`); err != nil {
		t.Fatal(err)
	}
	if s.RenameSpeaker(ctx, id, "Unknown replacement", "rejected") == nil {
		t.Fatal("failed transaction accepted")
	}
	after, err = r.ReadResult(ctx, id)
	if err != nil || *after.Speakers[0].Speaker != "Unknown replacement" {
		t.Fatal("failed rename not rolled back")
	}
}
