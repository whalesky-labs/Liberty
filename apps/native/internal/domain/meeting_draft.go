package domain

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

var ErrMeetingDraft = errors.New("meeting_draft_invalid")
var ErrMeetingDraftConflict = errors.New("meeting_draft_conflict")

// Manual notes are independent of immutable AI runs. They can also be written
// without configuring AI, and are never replaced by a late AI completion.
type MeetingDraft struct {
	Revision                      int64
	Summary                       Summary
	SourceRunID, TranscriptSHA256 string
	Sources                       map[string][]string
}

func (d MeetingDraft) Validate() error {
	if d.Revision < 0 || d.Revision >= 1<<63-1 || len(d.SourceRunID) > 256 || len(d.TranscriptSHA256) != 64 {
		return ErrMeetingDraft
	}
	if _, err := hex.DecodeString(d.TranscriptSHA256); err != nil {
		return ErrMeetingDraft
	}
	for section, ids := range d.Sources {
		switch section {
		case "summary", "topics", "decisions", "actionItems", "risks", "followUps":
		default:
			return ErrMeetingDraft
		}
		if len(ids) > 50 {
			return ErrMeetingDraft
		}
		for _, id := range ids {
			if id == "" || len(id) > 256 || strings.ContainsAny(id, "\x00\r\n") {
				return ErrMeetingDraft
			}
		}
	}
	data, err := json.Marshal(d.Summary)
	if err != nil || len(data) > 256*1024 {
		return ErrMeetingDraft
	}
	values := []string{d.Summary.Overview}
	for _, group := range [][]string{d.Summary.Topics, d.Summary.Decisions, d.Summary.ActionItems, d.Summary.Risks, d.Summary.FollowUps} {
		if len(group) > 1000 {
			return ErrMeetingDraft
		}
		values = append(values, group...)
	}
	for _, value := range values {
		if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return ErrMeetingDraft
		}
	}
	return nil
}
