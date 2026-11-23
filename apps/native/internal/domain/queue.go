package domain

import (
	"sort"
	"strings"
)

const QueuePageSize = 10

type QueueQuery struct {
	Source string
	Filter string
	Search string
	Page   int
}

type JobPage struct {
	Source    string
	Items     []JobSummary
	Page      int
	PageCount int
	From      int
	To        int
	Total     int
	Counts    QueueCounts
}

// Counts describe the current source before status/search filters, as the
// original queue's four metric cards do.
type QueueCounts struct{ Total, Processing, Completed, Failed int }

func IsProcessing(status string) bool {
	switch status {
	case "queued", "transcribing", "speaker_processing", "summarizing":
		return true
	default:
		return false
	}
}

// QueryJobs mirrors features/jobs/application/jobQueue.ts, including search by
// source filename, processing status grouping and the ten-item page boundary.
func QueryJobs(jobs []JobSummary, q QueueQuery) JobPage {
	search := strings.ToLower(strings.TrimSpace(q.Search))
	filtered := make([]JobSummary, 0, len(jobs))
	var counts QueueCounts
	for _, job := range jobs {
		if job.Source != q.Source {
			continue
		}
		counts.Total++
		if IsProcessing(job.OverallStatus) {
			counts.Processing++
		}
		if job.OverallStatus == "completed" {
			counts.Completed++
		}
		if job.OverallStatus == "failed" {
			counts.Failed++
		}
		if q.Filter == "processing" && !IsProcessing(job.OverallStatus) {
			continue
		}
		if (q.Filter == "completed" || q.Filter == "failed") && job.OverallStatus != q.Filter {
			continue
		}
		matches := search == "" || strings.Contains(strings.ToLower(job.Title), search)
		for _, file := range job.SourceFiles {
			matches = matches || strings.Contains(strings.ToLower(file.Name), search)
		}
		if matches {
			filtered = append(filtered, job)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].CreatedAt > filtered[j].CreatedAt })
	pages := max(1, (len(filtered)+QueuePageSize-1)/QueuePageSize)
	page := min(max(q.Page, 1), pages)
	start := (page - 1) * QueuePageSize
	end := min(start+QueuePageSize, len(filtered))
	from := 0
	if len(filtered) > 0 {
		from = start + 1
	}
	return JobPage{Source: q.Source, Items: filtered[start:end], Page: page, PageCount: pages, From: from, To: end, Total: len(filtered), Counts: counts}
}
