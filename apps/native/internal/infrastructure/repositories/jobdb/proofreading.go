package jobdb

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"liberty.local/liberty/internal/domain"
)

func (s *Store) EditSegment(ctx context.Context, id string, edit domain.SegmentEdit) error {
	if domain.ValidateJobID(id) != nil {
		return domain.ErrJobID
	}
	if err := edit.Validate(); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := visible(ctx, tx, id); err != nil {
			return err
		}
		var state, diarization string
		if err := tx.QueryRowContext(ctx, `SELECT overall_status,diarization_status FROM jobs WHERE id=?`, id).Scan(&state, &diarization); err != nil {
			return ErrDatabase
		}
		if state != "completed" || diarization == "legacy_unverified" {
			return domain.ErrTranscriptEdit
		}
		kind := "transcript"
		if diarization == "completed" {
			var hasSpeakers bool
			if tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM transcript_segments WHERE job_id=? AND segment_type='speaker')`, id).Scan(&hasSpeakers) != nil {
				return ErrDatabase
			}
			if hasSpeakers {
				kind = "speaker"
			}
		}
		var speaker any
		if edit.Speaker != nil && strings.TrimSpace(*edit.Speaker) != "" {
			speaker = strings.TrimSpace(*edit.Speaker)
		}
		old := edit.Original
		result, err := tx.ExecContext(ctx, `UPDATE transcript_segments SET text=?,speaker=? WHERE job_id=? AND id=? AND segment_type=? AND start_ms=? AND end_ms=? AND text=? AND speaker IS ?`, strings.TrimSpace(edit.Text), speaker, id, old.ID, kind, old.StartMS, old.EndMS, old.Text, old.Speaker)
		if err != nil {
			return ErrDatabase
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return domain.ErrTranscriptConflict
		}
		// Events contain no transcript, speaker name or media path. Historical
		// AI input/result snapshots remain immutable and can be compared later.
		return event(ctx, tx, id, "transcript_edited", "Transcript corrected", time.Now().UnixMilli())
	})
}
