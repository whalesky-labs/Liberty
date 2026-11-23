package domain

import (
	"fmt"
	"testing"
)

func TestQueueMatchesExistingSourceSearchAndPageBehavior(t *testing.T) {
	jobs := make([]JobSummary, 0)
	for i := 0; i < 14; i++ {
		jobs = append(jobs, JobSummary{ID: fmt.Sprint(i), Source: "local", Title: "会议", CreatedAt: fmt.Sprintf("2026-11-%02d", i+1), OverallStatus: "completed", SourceFiles: []SourceFile{{Name: fmt.Sprintf("Budget-%02d.wav", i)}}})
	}
	jobs = append(jobs, JobSummary{ID: "remote", Source: "remote", Title: "会议"})
	q := QueueQuery{Source: "local", Filter: "all", Search: "  BUDGET  ", Page: 1}
	page := QueryJobs(jobs, q)
	if page.Total != 14 || page.PageCount != 2 || len(page.Items) != 10 || page.Items[0].ID != "13" {
		t.Fatalf("unexpected first page: %+v", page)
	}
	q.Page = 99
	page = QueryJobs(jobs, q)
	if page.Page != 2 || len(page.Items) != 4 || page.From != 11 || page.To != 14 {
		t.Fatalf("unexpected last page: %+v", page)
	}
	q.Search = "remote"
	page = QueryJobs(jobs, q)
	if page.Total != 0 || page.Page != 1 || page.PageCount != 1 || page.From != 0 || page.To != 0 {
		t.Fatal("empty page is not normalized")
	}
	if jobs[0].ID != "0" {
		t.Fatal("query mutated caller input")
	}
}

func TestProcessingGroup(t *testing.T) {
	statuses := []string{"idle", "uploaded", "queued", "transcribing", "speaker_processing", "summarizing", "completed", "failed"}
	var jobs []JobSummary
	for _, status := range statuses {
		jobs = append(jobs, JobSummary{Source: "local", OverallStatus: status})
	}
	if page := QueryJobs(jobs, QueueQuery{Source: "local", Filter: "processing"}); page.Total != 4 {
		t.Fatalf("processing count: %d", page.Total)
	}
}
