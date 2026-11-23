package jobdb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

type Deletion = domain.JobDeletion

func scanDeletion(row interface{ Scan(...any) error }) (Deletion, error) {
	var d Deletion
	err := row.Scan(&d.ID, &d.JobID, &d.TrashName, &d.Phase, &d.PID, &d.ProcessIdentity)
	if err == sql.ErrNoRows {
		return d, application.ErrJobNotFound
	}
	if err != nil {
		return d, ErrDatabase
	}
	return d, nil
}

const deletionColumns = `operation_id,job_id,trash_name,phase,runner_pid,runner_process_identity`

func (s *Store) PrepareDeletion(ctx context.Context, id string) (Deletion, error) {
	var d Deletion
	if domain.ValidateJobID(id) != nil {
		return d, domain.ErrJobID
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return d, ErrDatabase
	}
	operation := "delete-" + hex.EncodeToString(nonce[:])
	now := time.Now().UnixMilli()
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := execute(ctx, tx, `INSERT INTO job_deletion_ops(operation_id,job_id,trash_name,phase,runner_pid,runner_process_identity,created_at_ms,updated_at_ms)
   SELECT ?,jobs.id,?,'prepared',job_runs.pid,job_runs.process_identity,?,? FROM jobs LEFT JOIN job_runs ON job_runs.job_id=jobs.id WHERE jobs.id=? ON CONFLICT(job_id) DO NOTHING`,
			operation, id+"-"+operation, now, now, id); err != nil {
			return err
		}
		var err error
		d, err = scanDeletion(tx.QueryRowContext(ctx, `SELECT `+deletionColumns+` FROM job_deletion_ops WHERE job_id=?`, id))
		return err
	})
	return d, err
}
func (s *Store) PendingDeletions(ctx context.Context) ([]Deletion, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+deletionColumns+` FROM job_deletion_ops ORDER BY created_at_ms,operation_id`)
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	result := []Deletion{}
	for rows.Next() {
		d, err := scanDeletion(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return result, nil
}

// AdvanceDeletion records a completed filesystem/process step. All transitions
// are compare-and-swap and repeatable; the tombstone survives deletion of jobs.
func (s *Store) AdvanceDeletion(ctx context.Context, d Deletion, next string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		current, err := scanDeletion(tx.QueryRowContext(ctx, `SELECT `+deletionColumns+` FROM job_deletion_ops WHERE operation_id=? AND job_id=?`, d.ID, d.JobID))
		if err != nil {
			return err
		}
		if current.Phase == next {
			return nil
		}
		allowed := map[string]string{"prepared": "fenced", "fenced": "trashed", "trashed": "database_deleted"}
		if allowed[current.Phase] != next {
			return domain.ErrConflict
		}
		if next == "fenced" {
			if err := execute(ctx, tx, `UPDATE job_runs SET lease_token=lease_token+1,status='fenced' WHERE job_id=? AND lease_token < ?`, d.JobID, int64(1<<63-1)); err != nil {
				return err
			}
		}
		if next == "database_deleted" {
			// Foreign keys cascade source/segments/summaries/events/runs together.
			if err := execute(ctx, tx, `DELETE FROM jobs WHERE id=?`, d.JobID); err != nil {
				return err
			}
		}
		return execute(ctx, tx, `UPDATE job_deletion_ops SET phase=?,updated_at_ms=?,last_error=NULL WHERE operation_id=?`, next, time.Now().UnixMilli(), d.ID)
	})
}
func (s *Store) FinishDeletion(ctx context.Context, d Deletion) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var phase string
		err := tx.QueryRowContext(ctx, `SELECT phase FROM job_deletion_ops WHERE operation_id=? AND job_id=?`, d.ID, d.JobID).Scan(&phase)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return ErrDatabase
		}
		if phase != "database_deleted" {
			return domain.ErrConflict
		}
		return execute(ctx, tx, `DELETE FROM job_deletion_ops WHERE operation_id=? AND job_id=?`, d.ID, d.JobID)
	})
}
