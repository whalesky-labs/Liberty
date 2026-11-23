package legacydb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"liberty.local/liberty/internal/domain"
)

const activeStatuses = "'queued', 'transcribing', 'speaker_processing', 'summarizing'"
const hasSummary = "EXISTS (SELECT 1 FROM ai_summary_runs runs WHERE runs.job_id = jobs.id AND runs.status = 'completed' AND runs.result_json IS NOT NULL)"
const hasWarning = "TRIM(COALESCE(warnings_json, '[]')) NOT IN ('', '[]')"

// Dashboard reads bounded aggregate projections in one transaction. It never
// loads full transcripts to compute the landing page.
func (r *Reader) Dashboard(ctx context.Context, key string, now time.Time) (domain.DashboardOverview, error) {
	now = now.In(time.Local)
	q, err := domain.DashboardPeriod(key, now)
	if err != nil {
		return domain.DashboardOverview{}, err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return domain.DashboardOverview{}, ErrDatabase
	}
	defer tx.Rollback()
	result := domain.DashboardOverview{Range: key, TrendGranularity: q.Granularity}
	filter, args := visibleJobs, []any{}
	if q.StartAt != nil {
		filter += " AND created_at >= ?"
		args = append(args, *q.StartAt)
	}
	m := &result.Metrics
	err = tx.QueryRowContext(ctx, `SELECT COUNT(1),
		COALESCE(SUM(MAX(duration_minutes,0)),0),
		COALESCE(SUM(MAX(COALESCE(processing_duration_seconds,0),0)),0),
		COALESCE(SUM(CASE WHEN overall_status IN (`+activeStatuses+`) THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN overall_status='completed' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN overall_status='failed' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN overall_status='completed' AND asr_status='completed' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN overall_status='completed' AND enable_speaker=1 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN overall_status='completed' AND enable_speaker=1 AND diarization_status='completed' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN overall_status='completed' AND `+hasSummary+` THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN overall_status='completed' AND last_exported_at IS NOT NULL THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN `+hasWarning+` THEN 1 ELSE 0 END),0)`+filter, args...).Scan(
		&m.TotalJobs, &m.MediaDurationMinutes, &m.ProcessingDurationSeconds, &m.ActiveJobs, &m.CompletedJobs, &m.FailedJobs,
		&m.TranscriptReadyJobs, &m.SpeakerEligibleJobs, &m.SpeakerReadyJobs, &m.SummaryReadyJobs, &m.ExportedJobs, &m.WarningJobs)
	if err != nil {
		return result, ErrDatabase
	}
	period := "date(created_at, 'localtime')"
	if q.Granularity == "hour" {
		period = "strftime('%H:00',created_at,'localtime')"
	}
	if q.Granularity == "month" {
		period = "strftime('%Y-%m',created_at,'localtime')"
	}
	trendArgs := append(append([]any{}, args...), q.Limit)
	rows, err := tx.QueryContext(ctx, `SELECT * FROM (SELECT `+period+` AS period,COUNT(1),
		SUM(CASE WHEN overall_status='completed' THEN 1 ELSE 0 END),
		SUM(CASE WHEN overall_status='failed' THEN 1 ELSE 0 END),
		COALESCE(SUM(MAX(duration_minutes,0)),0),COALESCE(SUM(MAX(COALESCE(processing_duration_seconds,0),0)),0)`+
		filter+` GROUP BY period ORDER BY period DESC LIMIT ?) ORDER BY period ASC`, trendArgs...)
	if err != nil {
		return result, ErrDatabase
	}
	result.Trend = []domain.DashboardTrend{}
	for rows.Next() {
		var point domain.DashboardTrend
		if rows.Scan(&point.Period, &point.TotalJobs, &point.CompletedJobs, &point.FailedJobs, &point.MediaDurationMinutes, &point.ProcessingDurationSeconds) != nil {
			rows.Close()
			return result, ErrDatabase
		}
		result.Trend = append(result.Trend, point)
	}
	readErr := rows.Err()
	if rows.Close() != nil || readErr != nil {
		return result, ErrDatabase
	}
	result.AttentionJobs, err = dashboardJobs(ctx, tx, filter, args, true)
	if err != nil {
		return result, err
	}
	result.RecentResults, err = dashboardJobs(ctx, tx, filter, args, false)
	if err != nil {
		return result, err
	}
	resources := &result.Resources
	if tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(1) FROM ai_model_configs),
		(SELECT COUNT(1) FROM ai_model_configs WHERE enabled=1),
		(SELECT COUNT(1) FROM ai_summary_templates),(SELECT COUNT(1) FROM meeting_members)`).Scan(
		&resources.AIModels, &resources.EnabledAIModels, &resources.Templates, &resources.Members) != nil {
		return result, ErrDatabase
	}
	companion := &domain.DashboardCompanion{}
	var experience int64
	err = tx.QueryRowContext(ctx, `SELECT profile.name,profile.experience,COALESCE(wallet.balance,0),
		EXISTS (SELECT 1 FROM pet_daily_check_ins check_ins WHERE check_ins.pet_id=profile.id AND check_ins.check_in_date=?),
		(SELECT COUNT(1) FROM farm_plots WHERE status IN ('needs_water','mature')) +
		(SELECT COUNT(1) FROM work_game_tasks WHERE status IN ('needsCare','claimable'))
		FROM pet_profile profile LEFT JOIN pet_wallets wallet ON wallet.pet_id=profile.id AND wallet.currency_key='LP'
		WHERE profile.id='default-pet'`, now.Format("2006-01-02")).Scan(&companion.Name, &experience, &companion.LPBalance, &companion.CheckedInToday, &companion.ClaimableActivities)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, ErrDatabase
	}
	if err == nil {
		companion.SetExperience(experience)
		result.Companion = companion
	}
	if tx.Commit() != nil {
		return result, ErrDatabase
	}
	return result, nil
}

func dashboardJobs(ctx context.Context, tx *sql.Tx, filter string, args []any, attention bool) ([]domain.DashboardJob, error) {
	order := ""
	if attention {
		filter += ` AND (overall_status IN (` + activeStatuses + `) OR overall_status='failed' OR ` + hasWarning +
			` OR (overall_status='completed' AND NOT ` + hasSummary + `) OR (overall_status='completed' AND last_exported_at IS NULL))`
		order = `CASE WHEN overall_status IN (` + activeStatuses + `) THEN 0 WHEN overall_status='failed' THEN 1 WHEN ` + hasWarning + ` THEN 2 ELSE 3 END ASC,`
	} else {
		filter += ` AND overall_status='completed'`
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,title,created_at,duration_minutes,overall_status,diarization_status,warnings_json,`+hasSummary+`,last_exported_at`+filter+` ORDER BY `+order+` created_at DESC LIMIT 6`, args...)
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	jobs := []domain.DashboardJob{}
	for rows.Next() {
		var job domain.DashboardJob
		var warnings string
		if rows.Scan(&job.ID, &job.Title, &job.CreatedAt, &job.DurationMinutes, &job.OverallStatus, &job.DiarizationStatus, &warnings, &job.HasSummary, &job.LastExportedAt) != nil {
			return nil, ErrDatabase
		}
		var parsed []json.RawMessage
		if json.Unmarshal([]byte(warnings), &parsed) == nil && strings.TrimSpace(warnings) != "null" {
			job.WarningCount = len(parsed)
		} else if strings.TrimSpace(warnings) != "" {
			job.WarningCount = 1
		}
		jobs = append(jobs, job)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return jobs, nil
}
