package jobdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"liberty.local/liberty/internal/domain"
)

func (s *Store) MeetingDraft(ctx context.Context, id string) (*domain.MeetingDraft, error) {
	var draft domain.MeetingDraft
	var data, sources string
	err := s.db.QueryRowContext(ctx, `SELECT revision,summary_json,source_run_id,transcript_sha256,sources_json FROM meeting_drafts WHERE job_id=? AND NOT EXISTS(SELECT 1 FROM job_deletion_ops WHERE job_id=meeting_drafts.job_id)`, id).Scan(&draft.Revision, &data, &draft.SourceRunID, &draft.TranscriptSHA256, &sources)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil || json.Unmarshal([]byte(data), &draft.Summary) != nil || json.Unmarshal([]byte(sources), &draft.Sources) != nil || draft.Validate() != nil {
		return nil, ErrDatabase
	}
	return &draft, nil
}

func (s *Store) SaveMeetingDraft(ctx context.Context, id string, draft domain.MeetingDraft) error {
	if domain.ValidateJobID(id) != nil || draft.Validate() != nil {
		return domain.ErrMeetingDraft
	}
	data, _ := json.Marshal(draft.Summary)
	sources, _ := json.Marshal(draft.Sources)
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := visible(ctx, tx, id); err != nil {
			return err
		}
		var diarization string
		if tx.QueryRowContext(ctx, `SELECT diarization_status FROM jobs WHERE id=?`, id).Scan(&diarization) != nil {
			return ErrDatabase
		}
		kind := "transcript"
		if diarization == "completed" {
			var speakers bool
			if tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM transcript_segments WHERE job_id=? AND segment_type='speaker')`, id).Scan(&speakers) != nil {
				return ErrDatabase
			}
			if speakers {
				kind = "speaker"
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,start_ms,end_ms,speaker,text FROM transcript_segments WHERE job_id=? AND segment_type=? ORDER BY segment_order ASC`, id, kind)
		if err != nil {
			return ErrDatabase
		}
		segments := []domain.Segment{}
		for rows.Next() {
			var segment domain.Segment
			if rows.Scan(&segment.ID, &segment.StartMS, &segment.EndMS, &segment.Speaker, &segment.Text) != nil {
				rows.Close()
				return ErrDatabase
			}
			segments = append(segments, segment)
		}
		readErr := rows.Err()
		if rows.Close() != nil || readErr != nil {
			return ErrDatabase
		}
		transcript, err := domain.SummaryJSON(segments)
		if err != nil || domain.SummaryHash(transcript) != draft.TranscriptSHA256 {
			return domain.ErrMeetingDraftConflict
		}
		ids := make(map[string]bool, len(segments))
		for _, segment := range segments {
			ids[segment.ID] = true
		}
		for _, sources := range draft.Sources {
			for _, source := range sources {
				if !ids[source] {
					return domain.ErrMeetingDraftConflict
				}
			}
		}
		var result sql.Result
		if draft.Revision == 0 {
			result, err = tx.ExecContext(ctx, `INSERT INTO meeting_drafts(job_id,revision,summary_json,source_run_id,transcript_sha256,sources_json,updated_at) VALUES(?,1,?,?,?,?,?) ON CONFLICT(job_id) DO NOTHING`, id, string(data), draft.SourceRunID, draft.TranscriptSHA256, string(sources), domain.PersistedUTC(time.Now()))
		} else {
			result, err = tx.ExecContext(ctx, `UPDATE meeting_drafts SET revision=revision+1,summary_json=?,source_run_id=?,transcript_sha256=?,sources_json=?,updated_at=? WHERE job_id=? AND revision=?`, string(data), draft.SourceRunID, draft.TranscriptSHA256, string(sources), domain.PersistedUTC(time.Now()), id, draft.Revision)
		}
		if err != nil {
			return ErrDatabase
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			return domain.ErrMeetingDraftConflict
		}
		return event(ctx, tx, id, "meeting_notes_edited", "Meeting notes saved", time.Now().UnixMilli())
	})
}
