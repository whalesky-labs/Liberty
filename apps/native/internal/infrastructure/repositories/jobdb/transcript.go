package jobdb

import (
	"context"
	"database/sql"
	"strings"

	"liberty.local/liberty/internal/domain"
)

// RenameSpeaker preserves the original operation: update only the speaker
// projection, leaving transcript text and historical AI snapshots untouched.
func (s *Store) RenameSpeaker(ctx context.Context, id, from, to string) error {
	if domain.ValidateJobID(id) != nil {
		return domain.ErrJobID
	}
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	if to == "" {
		return domain.ErrResourceInvalid
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := visible(ctx, tx, id); err != nil {
			return err
		}
		if from == "" {
			return execute(ctx, tx, `UPDATE transcript_segments SET speaker=? WHERE job_id=? AND segment_type='speaker' AND (speaker IS NULL OR trim(speaker)='')`, to, id)
		}
		return execute(ctx, tx, `UPDATE transcript_segments SET speaker=? WHERE job_id=? AND segment_type='speaker' AND speaker=?`, to, id, from)
	})
}
