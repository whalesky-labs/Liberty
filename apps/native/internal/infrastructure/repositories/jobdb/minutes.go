package jobdb

import (
	"context"
	"database/sql"
	"encoding/json"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/repositories/legacydb"
)

func (s *Store) BackfillMinutes(ctx context.Context, id, run string, p domain.MinutesPayload) (bool, error) {
	data, err := json.Marshal(p.Clone())
	if err != nil {
		return false, ErrDatabase
	}
	if legacydb.DecodeMinutesPayload(string(data)) == nil {
		return false, domain.ErrResourceInvalid
	}
	if p.SourceSummaryRunID != nil && *p.SourceSummaryRunID != run {
		return false, domain.ErrMinutesSource
	}
	updated := false
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if err := visible(ctx, tx, id); err != nil {
			return err
		}
		var current *string
		if err := tx.QueryRowContext(ctx, "SELECT minutes_payload_json FROM ai_summary_runs WHERE id=? AND job_id=? AND status='completed'", run, id).Scan(&current); err == sql.ErrNoRows {
			return domain.ErrSummaryMissing
		} else if err != nil {
			return ErrDatabase
		}
		if current != nil && legacydb.DecodeMinutesPayload(*current) != nil {
			return nil
		}
		if err := execute(ctx, tx, "UPDATE ai_summary_runs SET minutes_payload_json=? WHERE id=? AND job_id=? AND status='completed'", string(data), run, id); err != nil {
			return err
		}
		updated = true
		return nil
	})
	return updated && err == nil, err
}
