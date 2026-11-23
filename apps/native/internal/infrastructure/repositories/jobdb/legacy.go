package jobdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

// ImportLegacy runs before recovery/scheduling while the profile lock is held.
// The complete batch and completion marker commit together. A failed read or
// database write leaves the marker unset and all previously imported rows intact.
func (s *Store) ImportLegacy(ctx context.Context, source application.LegacyJobSource) (application.LegacyImportResult, error) {
	var result application.LegacyImportResult
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var marker string
		err := tx.QueryRowContext(ctx, "SELECT value FROM app_meta WHERE key='legacy_jobs_imported'").Scan(&marker)
		if err != nil && err != sql.ErrNoRows {
			return ErrDatabase
		}
		if marker == "1" {
			result.AlreadyImported = true
			return nil
		}
		if source == nil {
			return domain.ErrResourceInvalid
		}
		preserved := 0
		result.Skipped, err = source.Each(ctx, func(job application.LegacyJob) error {
			if domain.ValidateJobID(job.Result.Job.ID) != nil {
				return domain.ErrJobID
			}
			var exists bool
			if tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM jobs WHERE id=?) OR EXISTS(SELECT 1 FROM job_deletion_ops WHERE job_id=?)", job.Result.Job.ID, job.Result.Job.ID).Scan(&exists) != nil {
				return ErrDatabase
			}
			if exists {
				preserved++
				return nil
			}
			if err := saveLegacyJob(ctx, tx, job); err != nil {
				return err
			}
			result.Imported++
			return nil
		})
		if err != nil {
			return err
		}
		result.Skipped += preserved
		return execute(ctx, tx, "INSERT INTO app_meta(key,value) VALUES('legacy_jobs_imported','1') ON CONFLICT(key) DO UPDATE SET value=excluded.value")
	})
	if err != nil {
		return application.LegacyImportResult{}, err
	}
	return result, nil
}

func jsonArray[T any](values []T) (string, error) {
	if values == nil {
		values = []T{}
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", ErrDatabase
	}
	return string(data), nil
}
func saveLegacyJob(ctx context.Context, tx *sql.Tx, job application.LegacyJob) error {
	r := job.Result
	j := r.Job
	warnings, err := jsonArray(j.Warnings)
	if err != nil {
		return err
	}
	hotwords, err := jsonArray(j.Hotwords)
	if err != nil {
		return err
	}
	formats, err := jsonArray(j.ExportFormats)
	if err != nil {
		return err
	}
	columns := []string{"id", "title", "created_at", "duration_minutes", "lang", "enable_speaker", "runner_protocol_version", "asr_backend", "diarization_status", "warnings_json", "summary_template", "upload_status", "asr_status", "summary_status", "overall_status", "processing_started_at_ms", "processing_finished_at_ms", "processing_duration_seconds", "failure_reason", "process_log", "python_path", "runner_script_path", "active_summary_run_id", "last_exported_at", "hotwords_json", "export_formats_json"}
	placeholders := []string{}
	for range columns {
		placeholders = append(placeholders, "?")
	}
	if err := execute(ctx, tx, "INSERT INTO jobs("+strings.Join(columns, ",")+") VALUES("+strings.Join(placeholders, ",")+")",
		j.ID, j.Title, j.CreatedAt, j.DurationMinutes, j.Lang, j.EnableSpeaker, j.RunnerProtocolVersion, j.ASRBackend, j.DiarizationStatus, warnings, j.SummaryTemplate, j.UploadStatus, j.ASRStatus, j.SummaryStatus, j.OverallStatus, j.ProcessingStartedAtMS, j.ProcessingFinishedAtMS, j.ProcessingDurationSeconds, j.FailureReason, r.ProcessLog, job.PythonPath, job.RunnerPath, nil, j.LastExportedAt, hotwords, formats); err != nil {
		return err
	}
	if err := execute(ctx, tx, "DELETE FROM job_source_files WHERE job_id=?", j.ID); err != nil {
		return err
	}
	for _, file := range job.Sources {
		f := file.Metadata
		if err := execute(ctx, tx, "INSERT INTO job_source_files(id,job_id,name,path,size_label,kind) VALUES(?,?,?,?,?,?)", f.ID, j.ID, f.Name, file.Path, f.SizeLabel, f.Kind); err != nil {
			return err
		}
	}
	for _, projection := range []struct {
		kind     string
		segments []domain.Segment
	}{{"transcript", r.Transcript}, {"speaker", r.Speakers}} {
		if err := execute(ctx, tx, "DELETE FROM transcript_segments WHERE job_id=? AND segment_type=?", j.ID, projection.kind); err != nil {
			return err
		}
		for i, s := range projection.segments {
			if err := execute(ctx, tx, "INSERT INTO transcript_segments(id,job_id,segment_type,start_ms,end_ms,speaker,text,segment_order) VALUES(?,?,?,?,?,?,?,?)", j.ID+":"+projection.kind+":"+s.ID, j.ID, projection.kind, s.StartMS, s.EndMS, s.Speaker, s.Text, i); err != nil {
				return err
			}
		}
	}
	if !domain.HasSummaryContent(r.Summary) {
		return nil
	}
	id := "imported-summary-" + j.ID
	result := domain.LegacySummaryResult(r)
	payload, err := domain.ReconstructMinutes(r, result, nil, "", &id)
	if err != nil {
		return err
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return ErrDatabase
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return ErrDatabase
	}
	return execute(ctx, tx, `INSERT INTO ai_summary_runs(id,job_id,model_config_id,template_id,include_speaker,include_timestamp,extra_instructions,status,error_message,prompt_preview,raw_response,result_json,minutes_payload_json,created_at,updated_at)
 VALUES(?,?,NULL,NULL,?,1,'','completed',NULL,'Imported from legacy JSON task',NULL,?,?,?,?)`, id, j.ID, j.EnableSpeaker, string(resultJSON), string(payloadJSON), j.CreatedAt, j.CreatedAt)
}
