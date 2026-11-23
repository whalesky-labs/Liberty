package jobdb

import (
	"context"
	"database/sql"
	"encoding/json"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/diagnostics"
)

func (s *Store) Input(ctx context.Context, id string) (domain.JobInput, error) {
	var input domain.JobInput
	if domain.ValidateJobID(id) != nil {
		return input, domain.ErrJobID
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := visible(ctx, tx, id); err != nil {
			return err
		}
		var hotwords string
		input.Job.ID = id
		input.Job.Source = "local"
		if tx.QueryRowContext(ctx, `SELECT title,lang,enable_speaker,duration_minutes,hotwords_json,COALESCE(python_path,''),COALESCE(runner_script_path,'') FROM jobs WHERE id=?`, id).
			Scan(&input.Job.Title, &input.Job.Lang, &input.Job.EnableSpeaker, &input.Job.DurationMinutes, &hotwords, &input.PythonPath, &input.RunnerPath) != nil {
			return ErrDatabase
		}
		var words []*string
		input.Job.Hotwords = []string{}
		if json.Unmarshal([]byte(hotwords), &words) == nil {
			valid := true
			for _, word := range words {
				if word == nil {
					valid = false
					break
				}
			}
			if valid {
				for _, word := range words {
					input.Job.Hotwords = append(input.Job.Hotwords, *word)
				}
			}
		}
		var file domain.SourceFile
		if tx.QueryRowContext(ctx, `SELECT id,name,kind,size_label,COALESCE(path,'') FROM job_source_files WHERE job_id=? ORDER BY rowid LIMIT 1`, id).
			Scan(&file.ID, &file.Name, &file.Kind, &file.SizeLabel, &input.SourcePath) != nil {
			return ErrDatabase
		}
		input.Job.SourceFiles = []domain.SourceFile{file}
		return nil
	})
	return input, err
}
func (s *Store) UpdateLog(ctx context.Context, l domain.RunLease, text string) error {
	if l.Validate() != nil {
		return domain.ErrLease
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		return updateCurrent(ctx, tx, `UPDATE jobs SET process_log=? WHERE id=? AND EXISTS(SELECT 1 FROM job_runs WHERE `+currentRun+`)`, text, l.JobID, l.JobID, l.AttemptID, l.Token)
	})
}

// Ensure adapter errors used as failure reasons cannot persist private paths.
func safeFailure(reason string) string { return diagnostics.Text(reason, 4096) }
