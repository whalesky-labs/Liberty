package jobdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"reflect"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/repositories/legacydb"
)

func (s *Store) ListSummaries(ctx context.Context, jobID string) ([]domain.SummaryRun, error) {
	rows, err := s.db.QueryContext(ctx, summaryRunSQL+" WHERE job_id=?"+summaryVisible+" ORDER BY datetime(updated_at) DESC,updated_at DESC,id DESC", jobID)
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	values := []domain.SummaryRun{}
	for rows.Next() {
		run, scanErr := scanSummaryRun(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, run)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return values, nil
}

// Summary persistence is lease fenced in the same way as media execution. A
// worker may only publish a chunk or terminal result when its attempt/token
// pair still owns the running row.
func (s *Store) SummaryRun(ctx context.Context, jobID, runID string) (domain.SummaryRun, error) {
	return scanSummaryRun(s.db.QueryRowContext(ctx, summaryRunSQL+" WHERE job_id=? AND id=?"+summaryVisible, jobID, runID))
}

func (s *Store) RunningSummary(ctx context.Context, jobID string) (*domain.SummaryRun, error) {
	run, err := scanSummaryRun(s.db.QueryRowContext(ctx, summaryRunSQL+" WHERE job_id=? AND status='running'"+summaryVisible+" ORDER BY updated_at DESC,id DESC LIMIT 1", jobID))
	if err == domain.ErrSummaryMissing {
		return nil, nil
	}
	if err != nil {
		return nil, ErrDatabase
	}
	return &run, nil
}

const summaryRunColumns = `id,job_id,COALESCE(model_config_id,''),COALESCE(template_id,''),include_speaker,include_timestamp,extra_instructions,status,error_message,prompt_preview,raw_response,result_json,minutes_payload_json,created_at,updated_at`
const summaryRunSQL = `SELECT ` + summaryRunColumns + ` FROM ai_summary_runs`
const summaryVisible = ` AND NOT EXISTS(SELECT 1 FROM job_deletion_ops d WHERE d.job_id=ai_summary_runs.job_id)`

// scanSummaryRun is kept separate because nullable JSON columns must be
// decoded with the same serde-compatible rules used by the read-only reader.
func scanSummaryRun(row rowScanner) (domain.SummaryRun, error) {
	var run domain.SummaryRun
	var resultJSON, minutesJSON *string
	if err := row.Scan(&run.ID, &run.JobID, &run.ModelConfigID, &run.TemplateID, &run.IncludeSpeaker, &run.IncludeTimestamp, &run.ExtraInstructions, &run.Status, &run.ErrorMessage, &run.PromptPreview, &run.RawResponse, &resultJSON, &minutesJSON, &run.CreatedAt, &run.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return run, domain.ErrSummaryMissing
		}
		return run, ErrDatabase
	}
	if resultJSON != nil {
		run.Result = legacydb.DecodeSummaryResult(*resultJSON)
	}
	if minutesJSON != nil {
		run.MinutesPayload = legacydb.DecodeMinutesPayload(*minutesJSON)
	}
	return run, nil
}

func (s *Store) CreateSummary(ctx context.Context, seed domain.SummarySeed) (domain.SummaryRun, error) {
	e := seed.Execution
	if e.Lease != (domain.SummaryLease{}) || domain.ValidateJobID(e.Run.JobID) != nil || e.Run.ID == "" || e.ChunkCount < 1 || len(seed.Chunks) != e.ChunkCount || e.Run.Result != nil || e.Run.MinutesPayload != nil || e.Run.RawResponse != nil {
		return domain.SummaryRun{}, domain.ErrSummaryExecution
	}
	if _, err := e.Snapshot(); err != nil {
		return domain.SummaryRun{}, err
	}
	e.Run.Status, e.Run.ErrorMessage = "running", nil
	seen := make(map[int]struct{}, len(seed.Chunks))
	for _, chunk := range seed.Chunks {
		if chunk.Index < 0 || chunk.Index >= e.ChunkCount || chunk.SHA256 == "" || chunk.UserPrompt == "" ||
			chunk.SHA256 != domain.SummaryHash([]byte(chunk.UserPrompt)) {
			return domain.SummaryRun{}, domain.ErrSummaryExecution
		}
		if _, ok := seen[chunk.Index]; ok {
			return domain.SummaryRun{}, domain.ErrSummaryExecution
		}
		seen[chunk.Index] = struct{}{}
	}
	if len(seen) != e.ChunkCount {
		return domain.SummaryRun{}, domain.ErrSummaryExecution
	}
	if e.Run.CreatedAt == "" {
		e.Run.CreatedAt = domain.PersistedUTC(time.Now())
	}
	if e.Run.UpdatedAt == "" {
		e.Run.UpdatedAt = e.Run.CreatedAt
	}
	return s.transactionResult(ctx, func(tx *sql.Tx) (domain.SummaryRun, error) {
		if err := visible(ctx, tx, e.Run.JobID); err != nil {
			return domain.SummaryRun{}, err
		}
		if value, err := scanSummaryRun(tx.QueryRowContext(ctx, summaryRunSQL+" WHERE job_id=? AND status='running'"+summaryVisible+" ORDER BY updated_at DESC,id DESC LIMIT 1", e.Run.JobID)); err == nil {
			return value, nil
		} else if err != domain.ErrSummaryMissing {
			return domain.SummaryRun{}, err
		}
		resultJSON, err := optionalJSON(e.Run.Result)
		if err != nil {
			return domain.SummaryRun{}, err
		}
		minutesJSON, err := optionalJSON(e.Run.MinutesPayload)
		if err != nil {
			return domain.SummaryRun{}, err
		}
		if err := execute(ctx, tx, `INSERT INTO ai_summary_runs(id,job_id,model_config_id,template_id,include_speaker,include_timestamp,extra_instructions,status,error_message,prompt_preview,raw_response,result_json,minutes_payload_json,created_at,updated_at,transcript_revision,transcript_sha256,transcript_snapshot_json,execution_snapshot_json,chunk_count,attempt_id,lease_token)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, e.Run.ID, e.Run.JobID, nullString(e.Run.ModelConfigID), nullString(e.Run.TemplateID), e.Run.IncludeSpeaker, e.Run.IncludeTimestamp, e.Run.ExtraInstructions, "running", nil, e.Run.PromptPreview, nil, resultJSON, minutesJSON, e.Run.CreatedAt, e.Run.UpdatedAt, e.TranscriptRevision, e.TranscriptSHA256, e.TranscriptJSON, e.SnapshotJSON, e.ChunkCount, 0, 0); err != nil {
			return domain.SummaryRun{}, err
		}
		for _, chunk := range seed.Chunks {
			if err := execute(ctx, tx, `INSERT INTO ai_summary_run_chunks(run_id,chunk_index,chunk_sha256,user_prompt,status,created_at,updated_at) VALUES(?,?,?,?,'pending',?,?)`, e.Run.ID, chunk.Index, chunk.SHA256, chunk.UserPrompt, e.Run.CreatedAt, e.Run.CreatedAt); err != nil {
				return domain.SummaryRun{}, err
			}
		}
		if err := execute(ctx, tx, `UPDATE jobs SET summary_status='summarizing' WHERE id=?`, e.Run.JobID); err != nil {
			return domain.SummaryRun{}, err
		}
		return e.Run, nil
	})
}

func optionalJSON[T any](value *T) (*string, error) {
	if value == nil {
		return nil, nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, domain.ErrSummaryExecution
	}
	v := string(b)
	return &v, nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (s *Store) transactionResult(ctx context.Context, fn func(*sql.Tx) (domain.SummaryRun, error)) (domain.SummaryRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.SummaryRun{}, ErrDatabase
	}
	defer tx.Rollback()
	value, err := fn(tx)
	if err != nil {
		return domain.SummaryRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.SummaryRun{}, ErrDatabase
	}
	return value, nil
}

func (s *Store) ClaimSummary(ctx context.Context, jobID, runID, now string) (*domain.SummaryLease, error) {
	var lease *domain.SummaryLease
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := visible(ctx, tx, jobID); err != nil {
			return err
		}
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM ai_summary_runs WHERE job_id=? AND id=?`, jobID, runID).Scan(&status); err == sql.ErrNoRows {
			return domain.ErrSummaryMissing
		} else if err != nil {
			return ErrDatabase
		}
		if status == "completed" {
			return nil
		}
		var competing string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM ai_summary_runs WHERE job_id=? AND status='running' AND id<>? LIMIT 1`, jobID, runID).Scan(&competing); err == nil {
			return domain.ErrSummaryRunning
		} else if err != sql.ErrNoRows {
			return ErrDatabase
		}
		res, err := tx.ExecContext(ctx, `UPDATE ai_summary_runs SET attempt_id=attempt_id+1,lease_token=lease_token+1,status='running',error_message=NULL,updated_at=? WHERE job_id=? AND id=? AND status IN ('running','failed') AND execution_snapshot_json<>'' AND chunk_count>0 AND attempt_id >= 0 AND lease_token >= 0 AND attempt_id < ? AND lease_token < ?`, now, jobID, runID, int64(math.MaxInt64), int64(math.MaxInt64))
		if err != nil {
			return ErrDatabase
		}
		count, err := res.RowsAffected()
		if err != nil || count != 1 {
			return domain.ErrSummaryExecution
		}
		var attempt, token int64
		if err := tx.QueryRowContext(ctx, `SELECT attempt_id,lease_token FROM ai_summary_runs WHERE id=?`, runID).Scan(&attempt, &token); err != nil || attempt < 1 || token < 1 {
			return domain.ErrSummaryLease
		}
		if err := execute(ctx, tx, `UPDATE jobs SET summary_status='summarizing' WHERE id=?`, jobID); err != nil {
			return err
		}
		lease = &domain.SummaryLease{JobID: jobID, RunID: runID, AttemptID: attempt, Token: token}
		return nil
	})
	return lease, err
}

func (s *Store) LoadSummaryExecution(ctx context.Context, lease domain.SummaryLease) (domain.SummaryExecution, error) {
	return loadSummaryExecution(ctx, s.db, lease)
}

type summaryQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadSummaryExecution(ctx context.Context, db summaryQuery, lease domain.SummaryLease) (domain.SummaryExecution, error) {
	if err := lease.Validate(); err != nil {
		return domain.SummaryExecution{}, err
	}
	var e domain.SummaryExecution
	var resultJSON, minutesJSON *string
	var revision, hash, transcript, snapshot string
	var count int
	var run domain.SummaryRun
	err := db.QueryRowContext(ctx, `SELECT `+summaryRunColumns+`,transcript_revision,transcript_sha256,transcript_snapshot_json,execution_snapshot_json,chunk_count FROM ai_summary_runs WHERE id=? AND job_id=? AND attempt_id=? AND lease_token=? AND status='running'`+summaryVisible, lease.RunID, lease.JobID, lease.AttemptID, lease.Token).Scan(
		&run.ID, &run.JobID, &run.ModelConfigID, &run.TemplateID, &run.IncludeSpeaker, &run.IncludeTimestamp, &run.ExtraInstructions, &run.Status, &run.ErrorMessage, &run.PromptPreview, &run.RawResponse, &resultJSON, &minutesJSON, &run.CreatedAt, &run.UpdatedAt, &revision, &hash, &transcript, &snapshot, &count)
	if err == sql.ErrNoRows {
		return e, domain.ErrSummaryLease
	}
	if err != nil {
		return e, ErrDatabase
	}
	if resultJSON != nil {
		run.Result = legacydb.DecodeSummaryResult(*resultJSON)
	}
	if minutesJSON != nil {
		run.MinutesPayload = legacydb.DecodeMinutesPayload(*minutesJSON)
	}
	e = domain.SummaryExecution{Run: run, Lease: lease, TranscriptRevision: revision, TranscriptSHA256: hash, TranscriptJSON: transcript, SnapshotJSON: snapshot, ChunkCount: count}
	return e, nil
}

func (s *Store) SummaryChunks(ctx context.Context, lease domain.SummaryLease) ([]domain.SummaryChunk, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, ErrDatabase
	}
	defer tx.Rollback()
	if _, err := loadSummaryExecution(ctx, tx, lease); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT chunk_index,chunk_sha256,user_prompt,status,COALESCE(raw_response,''),COALESCE(structured_result_json,'') FROM ai_summary_run_chunks WHERE run_id=? ORDER BY chunk_index`, lease.RunID)
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	values := []domain.SummaryChunk{}
	for rows.Next() {
		var c domain.SummaryChunk
		var status string
		if rows.Scan(&c.Index, &c.SHA256, &c.UserPrompt, &status, &c.Raw, &c.Structured) != nil {
			return nil, ErrDatabase
		}
		c.Completed = status == "completed"
		values = append(values, c)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return values, nil
}

func (s *Store) SaveSummaryChunk(ctx context.Context, lease domain.SummaryLease, chunk domain.SummaryChunk, now string) error {
	if err := lease.Validate(); err != nil || chunk.Index < 0 || chunk.SHA256 == "" || chunk.Raw == "" || chunk.Structured == "" {
		return domain.ErrSummaryLease
	}
	parsed, err := domain.ParseSummaryStructured(chunk.Raw)
	canonical, encodeErr := domain.SummaryJSON(parsed)
	if err != nil || encodeErr != nil || string(canonical) != chunk.Structured {
		return domain.ErrSummaryExecution
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var prompt string
		if err := tx.QueryRowContext(ctx, `SELECT user_prompt FROM ai_summary_run_chunks WHERE run_id=? AND chunk_index=?`, lease.RunID, chunk.Index).Scan(&prompt); err == sql.ErrNoRows {
			return domain.ErrSummaryLease
		} else if err != nil {
			return ErrDatabase
		}
		if domain.SummaryHash([]byte(prompt)) != chunk.SHA256 {
			return domain.ErrSummaryExecution
		}
		res, err := tx.ExecContext(ctx, `UPDATE ai_summary_run_chunks SET status='completed',raw_response=?,structured_result_json=?,attempt_id=?,lease_token=?,updated_at=?,completed_at=? WHERE run_id=? AND chunk_index=? AND chunk_sha256=? AND status='pending' AND EXISTS(SELECT 1 FROM ai_summary_runs r WHERE r.id=? AND r.job_id=? AND r.attempt_id=? AND r.lease_token=? AND r.status='running' AND NOT EXISTS(SELECT 1 FROM job_deletion_ops d WHERE d.job_id=r.job_id))`, chunk.Raw, chunk.Structured, lease.AttemptID, lease.Token, now, now, lease.RunID, chunk.Index, chunk.SHA256, lease.RunID, lease.JobID, lease.AttemptID, lease.Token)
		if err != nil {
			return ErrDatabase
		}
		n, err := res.RowsAffected()
		if err != nil || n != 1 {
			return domain.ErrSummaryLease
		}
		return nil
	})
}

func (s *Store) CompleteSummary(ctx context.Context, lease domain.SummaryLease, completion domain.SummaryCompletion) error {
	if err := lease.Validate(); err != nil {
		return err
	}
	if completion.Raw == "" || completion.CompletedAt == "" || !json.Valid([]byte(completion.Diagnostics)) || completion.Minutes.SchemaVersion != 2 || completion.Minutes.SourceSummaryRunID == nil || *completion.Minutes.SourceSummaryRunID != lease.RunID {
		return domain.ErrSummaryExecution
	}
	resultJSON, err := json.Marshal(completion.Result)
	if err != nil {
		return domain.ErrSummaryExecution
	}
	minutesJSON, err := json.Marshal(completion.Minutes)
	if err != nil {
		return domain.ErrSummaryExecution
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		execution, err := loadSummaryExecution(ctx, tx, lease)
		if err != nil {
			return err
		}
		snapshot, err := execution.Snapshot()
		if err != nil {
			return err
		}
		parsed, err := domain.ParseSummaryResponse(completion.Raw, snapshot.FallbackTitle)
		if err != nil || !reflect.DeepEqual(parsed, completion.Result) || completion.Minutes.TemplateID != snapshot.TemplateID {
			return domain.ErrSummaryExecution
		}
		if err := domain.ValidateMinutesCoverage(snapshot.Job.Result(), completion.Minutes); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE ai_summary_runs SET status='completed',error_message=NULL,raw_response=?,result_json=?,minutes_payload_json=?,diagnostics_json=?,completed_at=?,updated_at=? WHERE id=? AND job_id=? AND attempt_id=? AND lease_token=? AND status='running' AND NOT EXISTS(SELECT 1 FROM job_deletion_ops WHERE job_id=?) AND chunk_count=(SELECT COUNT(*) FROM ai_summary_run_chunks WHERE run_id=? ) AND chunk_count=(SELECT COUNT(*) FROM ai_summary_run_chunks WHERE run_id=? AND status='completed')`, completion.Raw, string(resultJSON), string(minutesJSON), completion.Diagnostics, completion.CompletedAt, completion.CompletedAt, lease.RunID, lease.JobID, lease.AttemptID, lease.Token, lease.JobID, lease.RunID, lease.RunID)
		if err != nil {
			return ErrDatabase
		}
		count, err := res.RowsAffected()
		if err != nil || count != 1 {
			return domain.ErrSummaryLease
		}
		if err := execute(ctx, tx, `UPDATE jobs SET summary_status='completed',active_summary_run_id=? WHERE id=?`, lease.RunID, lease.JobID); err != nil {
			return err
		}
		return event(ctx, tx, lease.JobID, "ai_summary_completed", "AI 总结已完成。", time.Now().UnixMilli())
	})
}

func (s *Store) FailSummary(ctx context.Context, lease domain.SummaryLease, reason, now string) error {
	if err := lease.Validate(); err != nil {
		return err
	}
	// Accept stable public codes only, never provider bodies, SQL or credentials.
	switch reason {
	case "ai_execution_snapshot_invalid", "ai_model_unavailable", "ai_model_endpoint_changed", "ai_request_failed", "ai_response_invalid", "minutes_payload_invalid", "ai_execution_failed":
	default:
		reason = "ai_execution_failed"
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var jobID string
		if err := tx.QueryRowContext(ctx, `SELECT job_id FROM ai_summary_runs WHERE id=? AND job_id=? AND attempt_id=? AND lease_token=? AND status='running' AND NOT EXISTS(SELECT 1 FROM job_deletion_ops WHERE job_id=?)`, lease.RunID, lease.JobID, lease.AttemptID, lease.Token, lease.JobID).Scan(&jobID); err == sql.ErrNoRows {
			return domain.ErrSummaryLease
		} else if err != nil {
			return ErrDatabase
		}
		res, err := tx.ExecContext(ctx, `UPDATE ai_summary_runs SET status='failed',error_message=?,updated_at=? WHERE id=? AND job_id=? AND attempt_id=? AND lease_token=? AND status='running'`, reason, now, lease.RunID, lease.JobID, lease.AttemptID, lease.Token)
		if err != nil {
			return ErrDatabase
		}
		if count, e := res.RowsAffected(); e != nil || count != 1 {
			return domain.ErrSummaryLease
		}
		var status string
		if tx.QueryRowContext(ctx, `SELECT CASE WHEN EXISTS(SELECT 1 FROM ai_summary_runs WHERE job_id=? AND status='running') THEN 'summarizing' WHEN EXISTS(SELECT 1 FROM ai_summary_runs WHERE job_id=? AND status='completed' AND result_json IS NOT NULL) THEN 'completed' WHEN EXISTS(SELECT 1 FROM ai_summary_runs WHERE job_id=? AND status='failed') THEN 'failed' ELSE 'idle' END`, jobID, jobID, jobID).Scan(&status) != nil {
			return ErrDatabase
		}
		return execute(ctx, tx, `UPDATE jobs SET summary_status=? WHERE id=?`, status, jobID)
	})
}

func (s *Store) RecoverableSummaries(ctx context.Context) ([]domain.SummaryRun, error) {
	rows, err := s.db.QueryContext(ctx, summaryRunSQL+" WHERE status='running' AND execution_snapshot_json<>'' AND chunk_count>0 AND NOT EXISTS(SELECT 1 FROM job_deletion_ops d WHERE d.job_id=ai_summary_runs.job_id) ORDER BY created_at,id")
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	result := []domain.SummaryRun{}
	for rows.Next() {
		r, err := scanSummaryRun(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return result, nil
}

func (s *Store) SelectSummary(ctx context.Context, jobID, runID string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := visible(ctx, tx, jobID); err != nil {
			return err
		}
		run, err := scanSummaryRun(tx.QueryRowContext(ctx, summaryRunSQL+" WHERE job_id=? AND id=?", jobID, runID))
		if err != nil {
			return err
		}
		if run.Status != "completed" || run.Result == nil {
			return domain.ErrSummaryMissing
		}
		res, err := tx.ExecContext(ctx, `UPDATE jobs SET active_summary_run_id=?,summary_status='completed' WHERE id=? AND EXISTS(SELECT 1 FROM ai_summary_runs WHERE id=? AND job_id=? AND status='completed' AND result_json IS NOT NULL)`, runID, jobID, runID, jobID)
		if err != nil {
			return ErrDatabase
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return domain.ErrSummaryMissing
		}
		return nil
	})
}

func (s *Store) DeleteSummary(ctx context.Context, jobID, runID string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := visible(ctx, tx, jobID); err != nil {
			return err
		}
		var active sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT active_summary_run_id FROM jobs WHERE id=?`, jobID).Scan(&active); err != nil {
			return application.ErrJobNotFound
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM ai_summary_runs WHERE job_id=? AND id=?`, jobID, runID)
		if err != nil {
			return ErrDatabase
		}
		if count, e := res.RowsAffected(); e != nil || count != 1 {
			return domain.ErrSummaryMissing
		}
		var next any
		if active.Valid && active.String == runID {
			var replacement string
			err := tx.QueryRowContext(ctx, `SELECT id FROM ai_summary_runs WHERE job_id=? AND status='completed' AND result_json IS NOT NULL ORDER BY completed_at DESC,updated_at DESC,id DESC LIMIT 1`, jobID).Scan(&replacement)
			if err == nil {
				next = replacement
			} else if err != sql.ErrNoRows {
				return ErrDatabase
			}
		} else if active.Valid {
			next = active.String
		}
		var status string
		if tx.QueryRowContext(ctx, `SELECT CASE WHEN EXISTS(SELECT 1 FROM ai_summary_runs WHERE job_id=? AND status='running') THEN 'summarizing' WHEN EXISTS(SELECT 1 FROM ai_summary_runs WHERE job_id=? AND status='completed' AND result_json IS NOT NULL) THEN 'completed' WHEN EXISTS(SELECT 1 FROM ai_summary_runs WHERE job_id=? AND status='failed') THEN 'failed' ELSE 'idle' END`, jobID, jobID, jobID).Scan(&status) != nil {
			return ErrDatabase
		}
		return execute(ctx, tx, `UPDATE jobs SET active_summary_run_id=?,summary_status=? WHERE id=?`, next, status, jobID)
	})
}
