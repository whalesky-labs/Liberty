package jobdb

import (
	"context"
	"database/sql"
	"liberty.local/liberty/internal/domain"
)

func (s *Store) ActiveLease(ctx context.Context, id string) (*domain.RunLease, error) {
	if domain.ValidateJobID(id) != nil {
		return nil, domain.ErrJobID
	}
	l := domain.RunLease{JobID: id}
	err := s.db.QueryRowContext(ctx, `SELECT attempt_id,lease_token,COALESCE(output_dir,'') FROM job_runs WHERE job_id=? AND status='running' AND NOT EXISTS(SELECT 1 FROM job_deletion_ops WHERE job_id=job_runs.job_id)`, id).Scan(&l.AttemptID, &l.Token, &l.OutputDir)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, ErrDatabase
	}
	if err := l.Validate(); err != nil {
		return nil, err
	}
	return &l, nil
}
