package jobdb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

func execute(ctx context.Context, tx *sql.Tx, query string, args ...any) error {
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return ErrDatabase
	}
	return nil
}
func event(ctx context.Context, tx *sql.Tx, id, kind, message string, now int64) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ErrDatabase
	}
	return execute(ctx, tx, `INSERT INTO job_events(id,job_id,event_type,message,metadata_json,created_at) VALUES(?,?,?,?,NULL,?)`,
		"job-event-"+id+"-"+hex.EncodeToString(nonce[:]), id, kind, message, fmt.Sprint(now))
}
func visible(ctx context.Context, tx *sql.Tx, id string) error {
	var exists bool
	if tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE id=? AND NOT EXISTS(SELECT 1 FROM job_deletion_ops WHERE job_id=jobs.id))`, id).Scan(&exists) != nil {
		return ErrDatabase
	}
	if !exists {
		return application.ErrJobNotFound
	}
	return nil
}
func (s *Store) Create(ctx context.Context, input domain.JobInput) error {
	j := input.Job
	if domain.ValidateJobID(j.ID) != nil {
		return domain.ErrJobID
	}
	if strings.TrimSpace(j.Title) == "" || len(j.SourceFiles) != 1 || !filepath.IsAbs(input.SourcePath) {
		return domain.ErrConflict
	}
	hotwords, err := json.Marshal(j.Hotwords)
	if err != nil {
		return domain.ErrConflict
	}
	if j.Hotwords == nil {
		hotwords = []byte("[]")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := execute(ctx, tx, `INSERT INTO jobs(id,title,created_at,lang,enable_speaker,summary_template,upload_status,asr_status,summary_status,overall_status,diarization_status,hotwords_json,export_formats_json,python_path,runner_script_path)
   VALUES(?,?,?,?,?,?,'uploaded','queued','idle','queued',CASE WHEN ? THEN 'pending' ELSE 'disabled' END,?,'["txt","md","srt","docx"]',?,?)`,
			j.ID, j.Title, j.CreatedAt, j.Lang, j.EnableSpeaker, j.SummaryTemplate, j.EnableSpeaker, string(hotwords), input.PythonPath, input.RunnerPath); err != nil {
			return err
		}
		f := j.SourceFiles[0]
		if err := execute(ctx, tx, `INSERT INTO job_source_files(id,job_id,name,path,size_label,kind) VALUES(?,?,?,?,?,?)`, f.ID, j.ID, f.Name, input.SourcePath, f.SizeLabel, f.Kind); err != nil {
			return err
		}
		if err := execute(ctx, tx, `INSERT INTO job_runs(job_id,attempt_id,lease_token,status) VALUES(?,0,0,'queued')`, j.ID); err != nil {
			return err
		}
		return event(ctx, tx, j.ID, "created", "任务已创建。", time.Now().UnixMilli())
	})
}

func (s *Store) Begin(ctx context.Context, id string, started int64) (domain.RunLease, error) {
	lease := domain.RunLease{JobID: id}
	if domain.ValidateJobID(id) != nil || started < 0 {
		return lease, domain.ErrLease
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := visible(ctx, tx, id); err != nil {
			return err
		}
		var status string
		if tx.QueryRowContext(ctx, `SELECT overall_status FROM jobs WHERE id=?`, id).Scan(&status) != nil {
			return ErrDatabase
		}
		if status != "queued" {
			return domain.ErrConflict
		}
		err := tx.QueryRowContext(ctx, `INSERT INTO job_runs(job_id,attempt_id,lease_token,status,started_at_ms,heartbeat_at_ms)
   VALUES(?,1,1,'running',?,?) ON CONFLICT(job_id) DO UPDATE SET
   attempt_id=job_runs.attempt_id+1, lease_token=job_runs.lease_token+1,status='running',
   started_at_ms=excluded.started_at_ms,heartbeat_at_ms=excluded.heartbeat_at_ms,
   pid=NULL,process_identity=NULL,finished_at_ms=NULL,output_dir=NULL,last_error=NULL
   WHERE job_runs.status='queued' AND job_runs.attempt_id < ? AND job_runs.lease_token < ?
			RETURNING attempt_id,lease_token`, id, started, started, int64(math.MaxInt64-1), int64(math.MaxInt64-1)).Scan(&lease.AttemptID, &lease.Token)
		if err == sql.ErrNoRows {
			return domain.ErrConflict
		}
		if err != nil {
			return ErrDatabase
		}
		lease.OutputDir = fmt.Sprintf("attempts/attempt-%d-%d", lease.AttemptID, lease.Token)
		if err := execute(ctx, tx, `UPDATE job_runs SET output_dir=? WHERE job_id=?`, lease.OutputDir, id); err != nil {
			return err
		}
		if err := execute(ctx, tx, `UPDATE jobs SET asr_status='transcribing',summary_status='idle',overall_status='transcribing',
   diarization_status=CASE WHEN enable_speaker=1 THEN 'processing' ELSE 'disabled' END,warnings_json='[]',failure_reason=NULL,
   processing_started_at_ms=?,processing_finished_at_ms=NULL,processing_duration_seconds=NULL WHERE id=?`, started, id); err != nil {
			return err
		}
		return event(ctx, tx, id, "started", "任务开始处理。", started)
	})
	return lease, err
}

const currentRun = `job_id=? AND attempt_id=? AND lease_token=? AND status='running'
 AND NOT EXISTS(SELECT 1 FROM job_deletion_ops WHERE job_id=job_runs.job_id)`

func (s *Store) Heartbeat(ctx context.Context, l domain.RunLease, now int64) error {
	if l.Validate() != nil || now < 0 {
		return domain.ErrLease
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		return updateCurrent(ctx, tx, `UPDATE job_runs SET heartbeat_at_ms=? WHERE `+currentRun, now, l.JobID, l.AttemptID, l.Token)
	})
}
func (s *Store) Attach(ctx context.Context, l domain.RunLease, pid uint32, identity string, now int64) error {
	if l.Validate() != nil || pid == 0 || strings.TrimSpace(identity) == "" || now < 0 {
		return domain.ErrLease
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		return updateCurrent(ctx, tx, `UPDATE job_runs SET pid=?,process_identity=?,heartbeat_at_ms=? WHERE `+currentRun, pid, identity, now, l.JobID, l.AttemptID, l.Token)
	})
}
func updateCurrent(ctx context.Context, tx *sql.Tx, q string, args ...any) error {
	result, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return ErrDatabase
	}
	n, err := result.RowsAffected()
	if err != nil {
		return ErrDatabase
	}
	if n != 1 {
		return domain.ErrStaleAttempt
	}
	return nil
}
func (s *Store) Complete(ctx context.Context, c domain.Completion) error {
	if c.Lease.Validate() != nil || c.FinishedAtMS < 0 {
		return domain.ErrLease
	}
	if err := c.Result.Validate(); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		l := c.Lease
		if err := updateCurrent(ctx, tx, `UPDATE job_runs SET status='completed',finished_at_ms=?,pid=NULL,heartbeat_at_ms=NULL WHERE `+currentRun, c.FinishedAtMS, l.JobID, l.AttemptID, l.Token); err != nil {
			return err
		}
		for _, projection := range []struct {
			kind     string
			segments []domain.Segment
		}{{"transcript", c.Result.Transcript}, {"speaker", c.Result.Speakers}} {
			if err := execute(ctx, tx, `DELETE FROM transcript_segments WHERE job_id=? AND segment_type=?`, l.JobID, projection.kind); err != nil {
				return err
			}
			for i, segment := range projection.segments {
				if segment.StartMS > math.MaxInt64 || segment.EndMS > math.MaxInt64 {
					return domain.ErrRunnerResult
				}
				if err := execute(ctx, tx, `INSERT INTO transcript_segments(id,job_id,segment_type,start_ms,end_ms,speaker,text,segment_order) VALUES(?,?,?,?,?,?,?,?)`,
					l.JobID+":"+projection.kind+":"+segment.ID, l.JobID, projection.kind, segment.StartMS, segment.EndMS, segment.Speaker, segment.Text, i); err != nil {
					return err
				}
			}
		}
		warnings, err := json.Marshal(c.Result.Warnings)
		if err != nil {
			return domain.ErrRunnerResult
		}
		if c.Result.Warnings == nil {
			warnings = []byte("[]")
		}
		if err := execute(ctx, tx, `UPDATE jobs SET duration_minutes=?,processing_finished_at_ms=?,processing_duration_seconds=?,runner_protocol_version=?,asr_backend=?,
   diarization_status=?,warnings_json=?,summary_status='idle',asr_status='completed',overall_status='completed',failure_reason=NULL,process_log=? WHERE id=?`,
			c.Result.Duration(c.FallbackMinutes), c.FinishedAtMS, c.DurationSeconds, c.Result.ProtocolVersion, c.Result.ASRBackend, c.Result.DiarizationStatus, string(warnings), c.ProcessLog, l.JobID); err != nil {
			return err
		}
		return event(ctx, tx, l.JobID, "completed", "任务处理完成。", c.FinishedAtMS)
	})
}
func (s *Store) Fail(ctx context.Context, l domain.RunLease, finished int64, duration *uint32, reason, log string) error {
	reason = safeFailure(reason)
	if l.Validate() != nil || finished < 0 {
		return domain.ErrLease
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := updateCurrent(ctx, tx, `UPDATE job_runs SET status='failed',finished_at_ms=?,pid=NULL,heartbeat_at_ms=NULL,last_error=? WHERE `+currentRun, finished, reason, l.JobID, l.AttemptID, l.Token); err != nil {
			return err
		}
		if err := execute(ctx, tx, `UPDATE jobs SET processing_finished_at_ms=?,processing_duration_seconds=?,asr_status='failed',summary_status='idle',overall_status='failed',
   diarization_status=CASE WHEN enable_speaker=1 THEN 'failed' ELSE 'disabled' END,failure_reason=?,process_log=? WHERE id=?`, finished, duration, reason, log, l.JobID); err != nil {
			return err
		}
		return event(ctx, tx, l.JobID, "failed", reason, finished)
	})
}

// Requeue must follow verified process termination. It fences any previous
// writer even when recovery is repeated after a crash between filesystem steps.
func (s *Store) Requeue(ctx context.Context, l domain.RunLease) error {
	if l.Validate() != nil {
		return domain.ErrLease
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := updateCurrent(ctx, tx, `UPDATE job_runs SET lease_token=lease_token+1,status='queued',pid=NULL,process_identity=NULL,heartbeat_at_ms=NULL,
   started_at_ms=NULL,finished_at_ms=NULL,output_dir=NULL,last_error=NULL WHERE `+currentRun, l.JobID, l.AttemptID, l.Token); err != nil {
			return err
		}
		return execute(ctx, tx, `UPDATE jobs SET asr_status='queued',summary_status='idle',overall_status='queued',
   diarization_status=CASE WHEN enable_speaker=1 THEN 'pending' ELSE 'disabled' END,warnings_json='[]',failure_reason=NULL,
   processing_started_at_ms=NULL,processing_finished_at_ms=NULL,processing_duration_seconds=NULL WHERE id=?`, l.JobID)
	})
}
func (s *Store) Retry(ctx context.Context, id, python, script string) error {
	if domain.ValidateJobID(id) != nil {
		return domain.ErrJobID
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := visible(ctx, tx, id); err != nil {
			return err
		}
		var active bool
		if tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM job_runs WHERE job_id=? AND status IN ('running','queued'))`, id).Scan(&active) != nil {
			return ErrDatabase
		}
		if active {
			return domain.ErrConflict
		}
		if err := execute(ctx, tx, `UPDATE jobs SET upload_status='uploaded',asr_status='queued',summary_status='idle',overall_status='queued',runner_protocol_version=NULL,
   asr_backend='unknown',diarization_status=CASE WHEN enable_speaker=1 THEN 'pending' ELSE 'disabled' END,warnings_json='[]',failure_reason=NULL,process_log=NULL,duration_minutes=0,
   processing_started_at_ms=NULL,processing_finished_at_ms=NULL,processing_duration_seconds=NULL,python_path=?,runner_script_path=?,active_summary_run_id=NULL WHERE id=?`, python, script, id); err != nil {
			return err
		}
		for _, table := range []string{"transcript_segments", "ai_summary_runs"} {
			if err := execute(ctx, tx, "DELETE FROM "+table+" WHERE job_id=?", id); err != nil {
				return err
			}
		}
		if err := execute(ctx, tx, `INSERT INTO job_runs(job_id,attempt_id,lease_token,status) VALUES(?,0,0,'queued') ON CONFLICT(job_id) DO UPDATE SET
   status='queued',pid=NULL,process_identity=NULL,heartbeat_at_ms=NULL,started_at_ms=NULL,finished_at_ms=NULL,output_dir=NULL,last_error=NULL`, id); err != nil {
			return err
		}
		return event(ctx, tx, id, "retried", "任务已重试。", time.Now().UnixMilli())
	})
}

func (s *Store) Recoverable(ctx context.Context) ([]domain.RunRecord, error) {
	records := []domain.RunRecord{}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := execute(ctx, tx, `INSERT INTO job_runs(job_id,attempt_id,lease_token,status,started_at_ms,output_dir)
   SELECT id,CASE WHEN overall_status='queued' THEN 0 ELSE 1 END,CASE WHEN overall_status='queued' THEN 0 ELSE 1 END,
   CASE WHEN overall_status='queued' THEN 'queued' ELSE 'running' END,processing_started_at_ms,
   CASE WHEN overall_status='queued' THEN NULL ELSE 'attempts/attempt-1-1' END FROM jobs
   WHERE overall_status IN ('queued','transcribing','speaker_processing')
   AND NOT EXISTS(SELECT 1 FROM job_deletion_ops WHERE job_id=jobs.id) AND NOT EXISTS(SELECT 1 FROM job_runs WHERE job_id=jobs.id)`); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT job_id,attempt_id,lease_token,status,pid,process_identity,started_at_ms,heartbeat_at_ms,COALESCE(output_dir,'')
   FROM job_runs WHERE status IN ('queued','running') AND NOT EXISTS(SELECT 1 FROM job_deletion_ops WHERE job_id=job_runs.job_id) ORDER BY COALESCE(started_at_ms,0),job_id`)
		if err != nil {
			return ErrDatabase
		}
		defer rows.Close()
		for rows.Next() {
			var r domain.RunRecord
			if rows.Scan(&r.JobID, &r.AttemptID, &r.Token, &r.Status, &r.PID, &r.ProcessIdentity, &r.StartedAtMS, &r.HeartbeatAtMS, &r.OutputDir) != nil {
				return ErrDatabase
			}
			records = append(records, r)
		}
		if rows.Err() != nil {
			return ErrDatabase
		}
		return nil
	})
	return records, err
}
