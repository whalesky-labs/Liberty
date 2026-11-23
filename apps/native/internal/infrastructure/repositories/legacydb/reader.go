// Package legacydb reads the existing Liberty SQLite format. It intentionally
// exposes no write port until the migration's write and recovery gates pass.
package legacydb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"

	_ "modernc.org/sqlite"
)

const SchemaVersion = "10"

var (
	ErrDatabase = errors.New("database_read_failed")
	ErrSchema   = errors.New("database_schema_unsupported")
)

type Reader struct{ db *sql.DB }

// Open requires an explicitly supplied file; it neither discovers the user's
// profile nor creates, migrates, checkpoints, or repairs a database.
func Open(ctx context.Context, path string) (*Reader, error) {
	if strings.TrimSpace(path) == "" {
		return nil, ErrDatabase
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrDatabase
	}
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrDatabase
	}
	uriPath := filepath.ToSlash(abs)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "busy_timeout(5000)"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, ErrDatabase
	}
	db.SetMaxOpenConns(1)
	r := &Reader{db: db}
	var version string
	if err := db.QueryRowContext(ctx, "SELECT value FROM app_meta WHERE key = 'schema_version'").Scan(&version); err != nil || version != SchemaVersion {
		db.Close()
		return nil, ErrSchema
	}
	return r, nil
}

func (r *Reader) Close() error { return r.db.Close() }

const jobColumns = `id, title, created_at, overall_status, diarization_status, duration_minutes,
lang, enable_speaker, runner_protocol_version, asr_backend, warnings_json, summary_template,
upload_status, asr_status, summary_status, processing_started_at_ms, processing_finished_at_ms,
processing_duration_seconds, failure_reason, active_summary_run_id, last_exported_at, hotwords_json, export_formats_json`
const visibleJobs = " FROM jobs WHERE NOT EXISTS (SELECT 1 FROM job_deletion_ops WHERE job_deletion_ops.job_id = jobs.id)"

type scanner interface{ Scan(...any) error }

type jobRow struct {
	value                       domain.JobSummary
	warnings, hotwords, formats string
	targets                     []any
}

func newJobRow() *jobRow {
	row := &jobRow{}
	job := &row.value
	row.targets = []any{&job.ID, &job.Title, &job.CreatedAt, &job.OverallStatus, &job.DiarizationStatus, (*uint32Column)(&job.DurationMinutes),
		&job.Lang, &job.EnableSpeaker, &job.RunnerProtocolVersion, &job.ASRBackend, &row.warnings, &job.SummaryTemplate,
		&job.UploadStatus, &job.ASRStatus, &job.SummaryStatus, &job.ProcessingStartedAtMS, &job.ProcessingFinishedAtMS,
		&job.ProcessingDurationSeconds, &job.FailureReason, &job.ActiveSummaryRunID, &job.LastExportedAt, &row.hotwords, &row.formats}
	return row
}

func scanJob(source scanner) (domain.JobSummary, error) {
	return newJobRow().read(source)
}

func (row *jobRow) read(source scanner) (domain.JobSummary, error) {
	row.value = domain.JobSummary{Source: "local", SourceFiles: []domain.SourceFile{}}
	row.warnings, row.hotwords, row.formats = "", "", ""
	err := source.Scan(row.targets...)
	row.value.Hotwords = readStrings(row.hotwords, []string{})
	row.value.ExportFormats = readStrings(row.formats, nil)
	if row.value.ExportFormats == nil {
		row.value.ExportFormats = []string{"txt", "md", "srt", "docx"}
	}
	row.value.Warnings = readWarnings(row.warnings)
	switch row.value.ASRBackend {
	case "funasr", "sherpa-onnx", "unknown":
	default:
		row.value.ASRBackend = "unknown"
	}
	switch row.value.DiarizationStatus {
	case "disabled", "pending", "processing", "completed", "unavailable", "failed", "legacy_unverified":
	default:
		row.value.DiarizationStatus = "legacy_unverified"
	}
	return row.value, err
}

func readStrings(text string, fallback []string) []string {
	if text == "[]" {
		return []string{}
	}
	var values []string
	if json.Unmarshal([]byte(text), &values) != nil || values == nil {
		return fallback
	}
	if strings.Contains(text, "null") {
		quoted := false
		for index := 0; index < len(text); index++ {
			switch text[index] {
			case '\\':
				if quoted {
					index++
				}
			case '"':
				quoted = !quoted
			case 'n':
				if !quoted {
					return fallback
				}
			}
		}
	}
	return values
}

func readWarnings(text string) []domain.RunnerWarning {
	if text == "[]" {
		return []domain.RunnerWarning{}
	}
	var values []struct {
		Code    *string `json:"code"`
		Message *string `json:"message"`
	}
	result := []domain.RunnerWarning{}
	if json.Unmarshal([]byte(text), &values) != nil {
		return result
	}
	for _, item := range values {
		if item.Code == nil || item.Message == nil {
			return []domain.RunnerWarning{}
		}
		result = append(result, domain.RunnerWarning{Code: *item.Code, Message: *item.Message})
	}
	return result
}

func (r *Reader) ListJobs(ctx context.Context) ([]domain.JobSummary, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, ErrDatabase
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT "+jobColumns+visibleJobs+" ORDER BY created_at DESC")
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	jobs := make([]domain.JobSummary, 0)
	projection := newJobRow()
	for rows.Next() {
		job, err := projection.read(rows)
		if err != nil {
			return nil, ErrDatabase
		}
		jobs = append(jobs, job)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	if rows.Close() != nil {
		return nil, ErrDatabase
	}
	indexes := make(map[string]int, len(jobs))
	for i, job := range jobs {
		indexes[job.ID] = i
	}
	sources, err := tx.QueryContext(ctx, `SELECT source.job_id, source.id, source.name, source.kind, source.size_label
		FROM job_source_files source INNER JOIN jobs ON jobs.id = source.job_id
		WHERE NOT EXISTS (SELECT 1 FROM job_deletion_ops WHERE job_deletion_ops.job_id = jobs.id)
		ORDER BY source.rowid ASC`)
	if err != nil {
		return nil, ErrDatabase
	}
	defer sources.Close()
	var jobID string
	var file domain.SourceFile
	sourceTargets := []any{&jobID, &file.ID, &file.Name, &file.Kind, &file.SizeLabel}
	for sources.Next() {
		file = domain.SourceFile{}
		if sources.Scan(sourceTargets...) != nil {
			return nil, ErrDatabase
		}
		if i, ok := indexes[jobID]; ok {
			jobs[i].SourceFiles = append(jobs[i].SourceFiles, file)
		}
	}
	if sources.Err() != nil {
		return nil, ErrDatabase
	}
	if sources.Close() != nil || tx.Commit() != nil {
		return nil, ErrDatabase
	}
	return jobs, nil
}

// ReadResult uses one SQLite transaction so the job and both segment projections
// describe the same committed state, even while the old application is writing.
func (r *Reader) ReadResult(ctx context.Context, id string) (domain.Result, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return domain.Result{}, ErrDatabase
	}
	defer tx.Rollback()
	job, err := scanJob(tx.QueryRowContext(ctx, "SELECT "+jobColumns+visibleJobs+" AND id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Result{}, application.ErrJobNotFound
	}
	if err != nil {
		return domain.Result{}, ErrDatabase
	}
	sourceRows, err := tx.QueryContext(ctx, "SELECT id, name, kind, size_label FROM job_source_files WHERE job_id = ? ORDER BY rowid ASC", id)
	if err != nil {
		return domain.Result{}, ErrDatabase
	}
	var source domain.SourceFile
	sourceTargets := []any{&source.ID, &source.Name, &source.Kind, &source.SizeLabel}
	for sourceRows.Next() {
		source = domain.SourceFile{}
		if sourceRows.Scan(sourceTargets...) != nil {
			sourceRows.Close()
			return domain.Result{}, ErrDatabase
		}
		job.SourceFiles = append(job.SourceFiles, source)
	}
	sourceErr := sourceRows.Err()
	if sourceRows.Close() != nil || sourceErr != nil {
		return domain.Result{}, ErrDatabase
	}
	transcript, err := readSegments(ctx, tx, id, "transcript")
	if err != nil {
		return domain.Result{}, err
	}
	speakers, err := readSegments(ctx, tx, id, "speaker")
	if err != nil {
		return domain.Result{}, err
	}
	runs, err := readSummaryRuns(ctx, tx, id)
	if err != nil {
		return domain.Result{}, err
	}
	var log *string
	if tx.QueryRowContext(ctx, "SELECT process_log FROM jobs WHERE id = ?", id).Scan(&log) != nil {
		return domain.Result{}, ErrDatabase
	}
	if tx.Commit() != nil {
		return domain.Result{}, ErrDatabase
	}
	result := domain.Result{Job: job, Transcript: transcript, Speakers: speakers, SummaryRuns: runs, ProcessLog: log}
	if result.Job.DurationMinutes == 0 {
		result.Job.DurationMinutes = (domain.RunnerResult{Transcript: transcript, Speakers: speakers}).Duration(0)
	}
	result.HydrateSummary()
	return result, nil
}

const maxInitialSegmentCapacity = 65536

func readSegments(ctx context.Context, tx *sql.Tx, jobID, kind string) ([]domain.Segment, error) {
	var count int64
	if tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcript_segments WHERE job_id = ? AND segment_type = ?`, jobID, kind).Scan(&count) != nil || count < 0 {
		return nil, ErrDatabase
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, start_ms, end_ms, speaker, text
		FROM transcript_segments WHERE job_id = ? AND segment_type = ? ORDER BY segment_order ASC`, jobID, kind)
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	segments := make([]domain.Segment, 0, int(min(count, maxInitialSegmentCapacity)))
	var segment domain.Segment
	targets := []any{&segment.ID, (*uint64Column)(&segment.StartMS), (*uint64Column)(&segment.EndMS), &segment.Speaker, &segment.Text}
	for rows.Next() {
		segment = domain.Segment{}
		if rows.Scan(targets...) != nil {
			return nil, ErrDatabase
		}
		segments = append(segments, segment)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return segments, nil
}
