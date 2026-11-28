package domain

import (
	"errors"
	"strings"
	"unicode/utf8"
)

var ErrTranscriptConflict = errors.New("transcript_edit_conflict")
var ErrTranscriptEdit = errors.New("transcript_edit_invalid")

type SegmentEdit struct {
	Original Segment
	Text     string
	Speaker  *string
}

func (e SegmentEdit) Validate() error {
	if e.Original.ID == "" || len(e.Original.ID) > 256 || e.Original.EndMS < e.Original.StartMS ||
		strings.TrimSpace(e.Text) == "" || len(e.Text) > 32768 || !utf8.ValidString(e.Text) || strings.ContainsRune(e.Text, 0) {
		return ErrTranscriptEdit
	}
	if e.Speaker != nil && (len(*e.Speaker) > 256 || !utf8.ValidString(*e.Speaker) || strings.ContainsAny(*e.Speaker, "\x00\r\n")) {
		return ErrTranscriptEdit
	}
	return nil
}

func (r Result) CanEditTranscript() bool {
	return r.Job.Source == "local" && r.Job.OverallStatus == "completed" && len(r.PrimarySegments()) > 0 && r.Job.DiarizationStatus != "legacy_unverified"
}
