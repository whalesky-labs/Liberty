package application_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
	"liberty.local/liberty/internal/infrastructure/repositories/legacydb"
)

var permissionJobIDs = []string{"job-1700000000000-501", "job-1700000000000-502"}

func windowUseCases(t *testing.T, provider application.SummaryProvider) (*application.WindowScopes, *jobdb.Store, *legacydb.Reader, *application.SummaryService) {
	t.Helper()
	ctx := context.Background()
	database := filepath.Join(t.TempDir(), "liberty.sqlite3")
	store, _, err := jobdb.OpenPrepared(ctx, database, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, job := range permissionJobIDs {
		input := domain.JobInput{Job: domain.JobSummary{ID: job, Title: job, CreatedAt: "2026-11-19T00:00:00Z", Lang: "zh", EnableSpeaker: true, SourceFiles: []domain.SourceFile{{ID: job + "-file-1", Name: "fixture.wav", Kind: "audio", SizeLabel: "1 KB"}}}, SourcePath: filepath.Join(t.TempDir(), "fixture.wav")}
		if err := store.Create(ctx, input); err != nil {
			t.Fatal("create synthetic job", err)
		}
		lease, err := store.Begin(ctx, job, 1)
		if err != nil {
			t.Fatal(err)
		}
		speaker := "original"
		result := domain.RunnerResult{ProtocolVersion: 2, ASRBackend: "funasr", DiarizationStatus: "completed", Warnings: []domain.RunnerWarning{}, Transcript: []domain.Segment{{ID: job + "-transcript-1", StartMS: 0, EndMS: 1000, Text: "Synthetic transcript " + job}}, Speakers: []domain.Segment{{ID: job + "-speaker-1", StartMS: 0, EndMS: 1000, Speaker: &speaker, Text: "Synthetic transcript " + job}}}
		if err := store.Complete(ctx, domain.Completion{Lease: lease, FinishedAtMS: 2, Result: result}); err != nil {
			t.Fatal("complete synthetic job", err)
		}
	}
	reader, err := legacydb.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	meetings := application.NewMeetings(reader)
	resources := summaryResources{model: application.SummaryModel{Metadata: domain.AIModel{ID: "model-1", BaseURL: "https://example.test", Model: "synthetic"}}, options: application.SummaryOptions{Templates: []domain.SummaryTemplate{{ID: "template-1", Name: "Synthetic", Prompt: "Summarize the meeting."}}}}
	service := application.NewSummaryService(meetings, store, resources, provider)
	t.Cleanup(service.Close)
	scopes := application.NewWindowScopes(meetings)
	scopes.EnableEditing(store)
	scopes.EnableWordExport(application.NewWordExport(meetings, store))
	scopes.EnableSummary(service)
	return scopes, store, reader, service
}

func acceptedWindowSummary(t *testing.T, ctx context.Context, grant *application.JobWindow, store *jobdb.Store, job string) domain.SummaryRun {
	t.Helper()
	if err := grant.GenerateSummary(ctx, application.SummaryForm{ModelID: "model-1", TemplateID: "template-1"}); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ListSummaries(ctx, job)
	if err != nil || len(runs) != 1 {
		t.Fatal("summary was not durably accepted for its bound job", err)
	}
	return runs[0]
}

func TestWindowUseCasesPersistOnlyTheBoundJob(t *testing.T) {
	ctx := context.Background()
	scopes, store, reader, _ := windowUseCases(t, summaryProvider{})
	first, second := permissionJobIDs[0], permissionJobIDs[1]
	parent, err := scopes.OpenFromMain(ctx, application.Workbench, "local", first)
	if err != nil {
		t.Fatal(err)
	}
	notes, err := parent.OpenChild(application.MeetingNotes)
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.RenameSpeaker(ctx, "original", "renamed"); err != nil {
		t.Fatal(err)
	}
	if err := notes.RenameSpeaker(ctx, "renamed", "forbidden"); !errors.Is(err, application.ErrWindowScope) {
		t.Fatal("notes window acquired transcript mutation", err)
	}
	for _, job := range permissionJobIDs {
		result, err := reader.ReadResult(ctx, job)
		if err != nil {
			t.Fatal(err)
		}
		want := "original"
		if job == first {
			want = "renamed"
		}
		if len(result.Speakers) != 1 || result.Speakers[0].Speaker == nil || *result.Speakers[0].Speaker != want || result.Transcript[0].Text != "Synthetic transcript "+job {
			t.Fatal("rename crossed jobs or modified the transcript")
		}
	}
	if err := parent.ExportText(ctx, domain.ExportTranscript, domain.TextExportLabels{}, func(content []byte) error {
		if !strings.Contains(string(content), "renamed: Synthetic transcript "+first) || strings.Contains(string(content), second) {
			t.Error("text export did not use the persisted bound result")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	child, err := parent.OpenChild(application.AISummary)
	if err != nil {
		t.Fatal(err)
	}
	firstRun := acceptedWindowSummary(t, ctx, child, store, first)
	waitSummary(t, store, first, firstRun.ID, "completed")
	nextParent, err := scopes.OpenFromMain(ctx, application.Workbench, "local", second)
	if err != nil {
		t.Fatal(err)
	}
	parent.Close()
	if err := child.SelectSummary(ctx, firstRun.ID); err != nil {
		t.Fatal("parent job change revoked an independent AI window", err)
	}
	state, err := child.SummaryState(ctx)
	if err != nil || state.Result.Job.ID != first || state.Result.Job.ActiveSummaryRunID == nil || *state.Result.Job.ActiveSummaryRunID != firstRun.ID {
		t.Fatal("selection did not persist on the original job", err)
	}
	nextChild, err := nextParent.OpenChild(application.AISummary)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []struct {
		name string
		call func() error
	}{
		{"cross-job resume", func() error { return nextChild.GenerateSummary(ctx, application.SummaryForm{RunID: firstRun.ID}) }},
		{"cross-job select", func() error { return nextChild.SelectSummary(ctx, firstRun.ID) }},
		{"cross-job delete", func() error { return nextChild.DeleteSummary(ctx, firstRun.ID) }},
		{"revoked resume", func() error { return child.GenerateSummary(ctx, application.SummaryForm{RunID: firstRun.ID}) }},
		{"revoked select", func() error { return child.SelectSummary(ctx, firstRun.ID) }},
		{"revoked delete", func() error { return child.DeleteSummary(ctx, firstRun.ID) }},
	} {
		t.Run(action.name, func(t *testing.T) {
			err := action.call()
			want := domain.ErrSummaryMissing
			if strings.HasPrefix(action.name, "revoked") {
				want = application.ErrWindowScope
			}
			if !errors.Is(err, want) {
				t.Fatal("summary escaped its job or generation", err)
			}
		})
	}
	secondRun := acceptedWindowSummary(t, ctx, nextChild, store, second)
	waitSummary(t, store, second, secondRun.ID, "completed")
	publications := 0
	if err := nextParent.ExportWord(ctx, firstRun.ID, func(domain.MinutesDocument) error { publications++; return nil }); !errors.Is(err, domain.ErrSummaryMissing) || publications != 0 {
		t.Fatal("Word export accepted another job's summary", err)
	}
	if err := nextParent.ExportWord(ctx, secondRun.ID, func(document domain.MinutesDocument) error {
		publications++
		if document.Info.MeetingName != second {
			t.Error("Word export used another job's meeting")
		}
		return nil
	}); err != nil || publications != 1 {
		t.Fatal("bound Word export failed", err)
	}
	for _, run := range []domain.SummaryRun{firstRun, secondRun} {
		result, err := reader.ReadResult(ctx, run.JobID)
		if err != nil || len(result.SummaryRuns) != 1 || result.SummaryRuns[0].ID != run.ID || result.Job.ActiveSummaryRunID == nil || *result.Job.ActiveSummaryRunID != run.ID {
			t.Fatal("rejected operation changed persisted summary ownership", err)
		}
	}
	if err := nextChild.DeleteSummary(ctx, secondRun.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SummaryRun(ctx, second, secondRun.ID); !errors.Is(err, domain.ErrSummaryMissing) {
		t.Fatal("authorized deletion did not persist", err)
	}
	if _, err := store.SummaryRun(ctx, first, firstRun.ID); err != nil {
		t.Fatal("authorized deletion affected the other job", err)
	}
}

func TestWindowAcceptedSummarySurvivesIssuerCancellationAndClose(t *testing.T) {
	provider := newGatedProvider()
	scopes, store, _, _ := windowUseCases(t, provider)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	parent, err := scopes.OpenFromMain(ctx, application.Workbench, "local", permissionJobIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	child, err := parent.OpenChild(application.AISummary)
	if err != nil {
		t.Fatal(err)
	}
	run := acceptedWindowSummary(t, ctx, child, store, permissionJobIDs[0])
	waitProvider(t, provider)
	cancel()
	parent.Close()
	child.Close()
	if err := child.DeleteSummary(context.Background(), run.ID); !errors.Is(err, application.ErrWindowScope) {
		t.Fatal("closed issuer could delete accepted work", err)
	}
	replacement, err := scopes.OpenFromMain(context.Background(), application.AISummary, "local", run.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if err := replacement.GenerateSummary(context.Background(), application.SummaryForm{RunID: run.ID}); err != nil || provider.calls.Load() != 1 {
		t.Fatal("replacement duplicated an accepted AI request", err)
	}
	close(provider.release)
	waitSummary(t, store, run.JobID, run.ID, "completed")
}

func TestWindowSummaryDeletionFencesAcceptedWork(t *testing.T) {
	ctx := context.Background()
	provider := newGatedProvider()
	scopes, store, _, service := windowUseCases(t, provider)
	grant, err := scopes.OpenFromMain(ctx, application.AISummary, "local", permissionJobIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	run := acceptedWindowSummary(t, ctx, grant, store, permissionJobIDs[0])
	waitProvider(t, provider)
	if err := grant.DeleteSummary(ctx, run.ID); err != nil {
		t.Fatal("authorized window could not delete its accepted summary", err)
	}
	close(provider.release)
	service.Close()
	if _, err := store.SummaryRun(ctx, run.JobID, run.ID); !errors.Is(err, domain.ErrSummaryMissing) {
		t.Fatal("deleted summary was republished after the worker finished", err)
	}
	if runs, err := store.ListSummaries(ctx, permissionJobIDs[1]); err != nil || len(runs) != 0 {
		t.Fatal("summary deletion affected another job", err)
	}
}
