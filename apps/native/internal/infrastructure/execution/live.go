package execution

import (
	"context"
	"strings"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/diagnostics"
	"liberty.local/liberty/internal/infrastructure/runner"
)

type ActiveRuns interface {
	ActiveLease(context.Context, string) (*domain.RunLease, error)
}
type LiveReader struct {
	Reader application.MeetingReader
	Runs   ActiveRuns
	Files  *runner.Files
}

func (r *LiveReader) ReadRevision(ctx context.Context) (uint64, error) {
	if reader, ok := r.Reader.(interface {
		ReadRevision(context.Context) (uint64, error)
	}); ok {
		return reader.ReadRevision(ctx)
	}
	return 0, nil
}

func (r *LiveReader) QueryJobs(ctx context.Context, q domain.QueueQuery) (domain.JobPage, error) {
	if reader, ok := r.Reader.(interface {
		QueryJobs(context.Context, domain.QueueQuery) (domain.JobPage, error)
	}); ok {
		page, err := reader.QueryJobs(ctx, q)
		if err != nil {
			return domain.JobPage{}, err
		}
		for i := range page.Items {
			if err := r.overlay(ctx, &page.Items[i]); err != nil {
				return domain.JobPage{}, err
			}
		}
		return page, nil
	}
	jobs, err := r.ListJobs(ctx)
	if err != nil {
		return domain.JobPage{}, err
	}
	return domain.QueryJobs(jobs, q), nil
}

func (r *LiveReader) ListJobs(ctx context.Context) ([]domain.JobSummary, error) {
	jobs, err := r.Reader.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	for j := range jobs {
		if err := r.overlay(ctx, &jobs[j]); err != nil {
			return nil, err
		}
	}
	return jobs, nil
}
func (r *LiveReader) ReadResult(ctx context.Context, id string) (domain.Result, error) {
	result, err := r.Reader.ReadResult(ctx, id)
	if err == nil {
		err = r.overlay(ctx, &result.Job)
	}
	return result, err
}
func (r *LiveReader) overlay(ctx context.Context, job *domain.JobSummary) error {
	if job.OverallStatus == "completed" || job.OverallStatus == "failed" {
		return nil
	}
	lease, err := r.Runs.ActiveLease(ctx, job.ID)
	if err != nil {
		return err
	}
	if lease == nil {
		return nil
	}
	data, err := r.Files.ReadAttemptFile(*lease, "progress.json", runner.MaxEventBytes)
	if err != nil {
		return nil
	}
	progress, err := runner.ParseProgress(data)
	if err != nil {
		return nil
	}
	current, err := r.Runs.ActiveLease(ctx, job.ID)
	if err != nil {
		return err
	}
	if current == nil || *current != *lease {
		return nil
	}
	if progress.StatusMessage != nil && strings.TrimSpace(*progress.StatusMessage) != "" {
		text := diagnostics.Text(*progress.StatusMessage, 4096)
		job.ProgressMessage = &text
	}
	if progress.ProgressPercent != nil {
		value := float64(min(100, *progress.ProgressPercent))
		job.ProgressPercent = &value
	}
	// Terminal UI state is published only with the validated result transaction.
	// A Python progress file alone cannot claim the job was saved successfully.
	defaults := map[string]float64{"queued": 0, "transcribing": 12, "speaker_processing": 92}
	if fallback, ok := defaults[progress.Stage]; ok {
		job.UploadStatus = "uploaded"
		job.ASRStatus = progress.Stage
		job.OverallStatus = progress.Stage
		job.SummaryStatus = "idle"
		if job.ProgressPercent == nil {
			job.ProgressPercent = &fallback
		}
	}
	return nil
}
