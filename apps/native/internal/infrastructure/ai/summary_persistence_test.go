package ai_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/ai"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
	"liberty.local/liberty/internal/infrastructure/repositories/legacydb"
	"liberty.local/liberty/internal/infrastructure/repositories/schema"
)

type completionResources struct{ base string }

func (r completionResources) SummaryModel(context.Context, string) (application.SummaryModel, error) {
	return application.SummaryModel{Metadata: domain.AIModel{ID: "model-1", Name: "Synthetic model", BaseURL: r.base, Model: "synthetic"}, Credential: domain.NewSecret("synthetic-test-value")}, nil
}
func (completionResources) SummaryOptions(context.Context) (application.SummaryOptions, error) {
	return application.SummaryOptions{Templates: []domain.SummaryTemplate{{ID: "template-1", Name: "Synthetic template", Prompt: "Summarize the synthetic meeting."}}}, nil
}
func (completionResources) Members(context.Context) ([]domain.MeetingMember, error) { return nil, nil }

func TestHTTPIncompleteSummaryStaysFailedAcrossReopenAndExplicitRetry(t *testing.T) {
	for _, reason := range []string{
		`"finish_reason":"length","FINISH_REASON":"stop"`,
		`"finish_reason":"length","finish_reason":"stop"`,
	} {
		t.Run(reason, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			content, err := json.Marshal(`{"title":"Exact title","overview":"Exact overview","topics":[],"decisions":[],"actionItems":[],"risks":[],"followUps":[],"speakerReports":[],"globalSummary":[]}`)
			if err != nil {
				t.Fatal(err)
			}
			bad := `{"choices":[{` + reason + `,"message":{"content":` + string(content) + `}}]}`
			good := `{"choices":[{"finish_reason":"stop","message":{"content":[{"text":` + string(content) + `,"TEXT":false}]}}]}`
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
					t.Error("wrong completion request")
				}
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "application/json")
				if calls.Add(1) == 1 {
					_, _ = io.WriteString(w, bad)
				} else {
					_, _ = io.WriteString(w, good)
				}
			}))
			defer server.Close()
			database := filepath.Join(t.TempDir(), "synthetic.sqlite3")
			jobID := "job-1700000000000-901"
			seedSummaryMeeting(t, ctx, database, jobID)
			resources := completionResources{base: server.URL + "/v1"}
			run := executeHTTPCompletion(t, ctx, database, jobID, resources, application.SummaryForm{ModelID: "model-1", TemplateID: "template-1"})
			if run.Status != "failed" {
				t.Fatal("incomplete HTTP response was published as a successful summary")
			}
			for range 2 {
				assertSummaryPublication(t, ctx, database, jobID, run.ID, false)
			}
			if calls.Load() != 1 {
				t.Fatal("invalid completion triggered an automatic provider retry")
			}
			// A new service and repository connection represent a subsequent app
			// session. Only the explicit retry may request and publish new content.
			retried := executeHTTPCompletion(t, ctx, database, jobID, resources, application.SummaryForm{RunID: run.ID})
			if retried.ID != run.ID || retried.Status != "completed" || calls.Load() != 2 {
				t.Fatal("explicit retry did not complete the same durable run")
			}
			for range 2 {
				assertSummaryPublication(t, ctx, database, jobID, run.ID, true)
			}
		})
	}
}

func seedSummaryMeeting(t *testing.T, ctx context.Context, database, id string) {
	t.Helper()
	store, _, err := jobdb.OpenPrepared(ctx, database, nil, true)
	if err != nil {
		t.Fatal("isolated database unavailable")
	}
	defer store.Close()
	input := domain.JobInput{Job: domain.JobSummary{ID: id, Title: "Synthetic meeting", CreatedAt: "2026-11-20T00:00:00Z", Lang: "zh", SourceFiles: []domain.SourceFile{{ID: "source-1", Name: "synthetic.wav", Kind: "audio"}}}, SourcePath: filepath.Join(filepath.Dir(database), "synthetic.wav")}
	if store.Create(ctx, input) != nil {
		t.Fatal("synthetic meeting creation failed")
	}
	lease, err := store.Begin(ctx, id, 1)
	if err != nil {
		t.Fatal("synthetic execution unavailable")
	}
	if store.Complete(ctx, domain.Completion{Lease: lease, FinishedAtMS: 2, Result: domain.RunnerResult{ProtocolVersion: 2, ASRBackend: "funasr", DiarizationStatus: "disabled", Warnings: []domain.RunnerWarning{}, Transcript: []domain.Segment{{ID: "segment-1", EndMS: 1000, Text: "Synthetic transcript"}}, Speakers: []domain.Segment{}}}) != nil {
		t.Fatal("synthetic transcript unavailable")
	}
}

func executeHTTPCompletion(t *testing.T, ctx context.Context, database, id string, resources completionResources, form application.SummaryForm) domain.SummaryRun {
	t.Helper()
	store, err := jobdb.Open(ctx, database)
	if err != nil {
		t.Fatal("summary repository unavailable")
	}
	defer store.Close()
	reader, err := legacydb.Open(ctx, database)
	if err != nil {
		t.Fatal("meeting reader unavailable")
	}
	defer reader.Close()
	service := application.NewSummaryService(reader, store, resources, ai.Client{})
	defer service.Close()
	run, err := service.Start(ctx, id, form)
	if err != nil {
		t.Fatal("summary request rejected", err)
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		current, err := store.SummaryRun(ctx, id, run.ID)
		if err != nil {
			t.Fatal("summary state unavailable")
		}
		if current.Status == "completed" || current.Status == "failed" {
			return current
		}
		select {
		case <-ctx.Done():
			t.Fatal("summary did not reach a durable terminal state")
		case <-ticker.C:
		}
	}
}

func assertSummaryPublication(t *testing.T, ctx context.Context, database, jobID, runID string, completed bool) {
	t.Helper()
	reader, err := legacydb.Open(ctx, database)
	if err != nil {
		t.Fatal("reopened meeting reader unavailable")
	}
	defer reader.Close()
	result, err := reader.ReadResult(ctx, jobID)
	if err != nil || len(result.SummaryRuns) != 1 {
		t.Fatal("durable summary missing")
	}
	run := result.SummaryRuns[0]
	status, publications, attempt := "failed", 0, 1
	if completed {
		status, publications, attempt = "completed", 1, 2
		if run.Result == nil || run.Result.Overview != "Exact overview" || run.MinutesPayload == nil || run.ErrorMessage != nil || result.Summary.Overview != "Exact overview" || result.Job.ActiveSummaryRunID == nil || *result.Job.ActiveSummaryRunID != runID {
			t.Fatal("valid retry did not publish exact content and minutes")
		}
	} else if run.Result != nil || run.MinutesPayload != nil || run.RawResponse != nil || run.ErrorMessage == nil || *run.ErrorMessage != "ai_response_invalid" || result.Summary.Overview != "" {
		t.Fatal("failed completion exposed content or lost its safe error")
	}
	if run.ID != runID || run.Status != status || result.Job.SummaryStatus != status {
		t.Fatal("durable run and meeting summary status differ")
	}
	db, err := sql.Open("sqlite", schema.URI(database, url.Values{"mode": {"ro"}}))
	if err != nil {
		t.Fatal("read-only publication audit unavailable")
	}
	defer db.Close()
	// The reader can select a failed run for display. The persisted active
	// pointer, however, must only be published after a successful completion.
	var published *string
	if db.QueryRowContext(ctx, "SELECT active_summary_run_id FROM jobs WHERE id=?", jobID).Scan(&published) != nil || (published != nil) != completed || completed && *published != runID {
		t.Fatal("failed completion published the persisted active summary")
	}
	var events, chunks, payloads, gotAttempt int
	if db.QueryRowContext(ctx, "SELECT COUNT(*) FROM job_events WHERE job_id=? AND event_type='ai_summary_completed'", jobID).Scan(&events) != nil || events != publications {
		t.Fatal("failed response emitted a completion event or retry duplicated it")
	}
	if db.QueryRowContext(ctx, "SELECT COUNT(*) FROM ai_summary_run_chunks WHERE run_id=? AND status='completed'", runID).Scan(&chunks) != nil || chunks != publications {
		t.Fatal("invalid response marked a reusable chunk completed")
	}
	if db.QueryRowContext(ctx, "SELECT COUNT(*) FROM ai_summary_run_chunks WHERE run_id=? AND (raw_response IS NOT NULL OR structured_result_json IS NOT NULL)", runID).Scan(&payloads) != nil || payloads != publications {
		t.Fatal("invalid response left a reusable chunk payload")
	}
	if db.QueryRowContext(ctx, "SELECT attempt_id FROM ai_summary_runs WHERE id=?", runID).Scan(&gotAttempt) != nil || gotAttempt != attempt {
		t.Fatal("retry attempt was not durable")
	}
}
