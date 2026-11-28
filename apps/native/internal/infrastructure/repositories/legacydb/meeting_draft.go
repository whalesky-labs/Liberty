package legacydb

import (
	"context"
	"database/sql"
	"encoding/json"
	"liberty.local/liberty/internal/domain"
)

// Read the draft, source and AI evidence in the same transaction as the result.
// Read-only Native windows and exports see the same committed manual content.
func readMeetingDraft(ctx context.Context, tx *sql.Tx, r *domain.Result) error {
	var draft domain.MeetingDraft
	var data, sources string
	err := tx.QueryRowContext(ctx, `SELECT revision,summary_json,source_run_id,transcript_sha256,sources_json FROM meeting_drafts WHERE job_id=?`, r.Job.ID).Scan(&draft.Revision, &data, &draft.SourceRunID, &draft.TranscriptSHA256, &sources)
	if err != nil && err != sql.ErrNoRows {
		return ErrDatabase
	}
	var hash string
	if err == nil {
		if json.Unmarshal([]byte(data), &draft.Summary) != nil || json.Unmarshal([]byte(sources), &draft.Sources) != nil || draft.Validate() != nil {
			return ErrDatabase
		}
		r.Draft = &draft
		r.Summary = draft.Summary
		hash = draft.TranscriptSHA256
	} else if r.Job.ActiveSummaryRunID != nil {
		if tx.QueryRowContext(ctx, `SELECT transcript_sha256 FROM ai_summary_runs WHERE job_id=? AND id=?`, r.Job.ID, *r.Job.ActiveSummaryRunID).Scan(&hash) != nil {
			return ErrDatabase
		}
		if hash == "" {
			if tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM job_events WHERE job_id=? AND event_type='transcript_edited')`, r.Job.ID).Scan(&r.SummarySourceChanged) != nil {
				return ErrDatabase
			}
		}
	}
	if hash != "" {
		transcript, err := domain.SummaryJSON(r.PrimarySegments())
		if err != nil {
			return ErrDatabase
		}
		r.SummarySourceChanged = domain.SummaryHash(transcript) != hash
	}
	return nil
}
