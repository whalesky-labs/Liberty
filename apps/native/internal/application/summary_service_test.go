package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
)

type summaryReader struct{ result domain.Result }

func (r summaryReader) ListJobs(context.Context) ([]domain.JobSummary, error) {
	return []domain.JobSummary{r.result.Job}, nil
}
func (r summaryReader) ReadResult(context.Context, string) (domain.Result, error) {
	return r.result, nil
}

type summaryResources struct {
	model   application.SummaryModel
	options application.SummaryOptions
}

func (r summaryResources) SummaryModel(context.Context, string) (application.SummaryModel, error) {
	return r.model, nil
}
func (r summaryResources) SummaryOptions(context.Context) (application.SummaryOptions, error) {
	return r.options, nil
}
func (r summaryResources) Members(context.Context) ([]domain.MeetingMember, error) { return nil, nil }

type summaryProvider struct{}

func (summaryProvider) Complete(context.Context, domain.SummaryRequest) (string, error) {
	return `{"choices":[{"finish_reason":"stop","message":{"content":"{\"title\":\"Synthetic\",\"overview\":\"Synthetic overview\",\"topics\":[\"topic\"],\"decisions\":[],\"actionItems\":[],\"risks\":[],\"followUps\":[],\"speakerReports\":[],\"globalSummary\":[]}"}}]}`, nil
}

func summaryFixture(t *testing.T, provider application.SummaryProvider) (*application.SummaryService, *jobdb.Store, summaryReader, summaryResources) {
	t.Helper()
	ctx := context.Background()
	store, _, err := jobdb.OpenPrepared(ctx, filepath.Join(t.TempDir(), "liberty.sqlite3"), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	job := domain.JobInput{Job: domain.JobSummary{ID: "job-1700000000000-321", Title: "Synthetic summary", CreatedAt: "2026-11-18T00:00:00Z", Lang: "zh", EnableSpeaker: false, SourceFiles: []domain.SourceFile{{ID: "file-1", Name: "fixture.wav", Kind: "audio", SizeLabel: "1 KB"}}}, SourcePath: filepath.Join(t.TempDir(), "fixture.wav")}
	if err := store.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	lease, err := store.Begin(ctx, job.Job.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(ctx, domain.Completion{Lease: lease, FinishedAtMS: 2, Result: domain.RunnerResult{ProtocolVersion: 2, ASRBackend: "funasr", DiarizationStatus: "disabled", Warnings: []domain.RunnerWarning{}, Transcript: []domain.Segment{{ID: "segment-1", StartMS: 0, EndMS: 1000, Text: "Synthetic transcript"}}, Speakers: []domain.Segment{}}}); err != nil {
		t.Fatal(err)
	}
	reader := summaryReader{result: domain.Result{Job: domain.JobSummary{ID: job.Job.ID, Source: "local", Title: job.Job.Title, Lang: "zh", EnableSpeaker: false, DiarizationStatus: "disabled", Hotwords: []string{}}, Transcript: []domain.Segment{{ID: "segment-1", Text: "Synthetic transcript"}}, Summary: domain.EmptySummary()}}
	template := domain.SummaryTemplate{ID: "template-1", Name: "Synthetic template", Prompt: "Summarize the meeting."}
	resources := summaryResources{model: application.SummaryModel{Metadata: domain.AIModel{ID: "model-1", Name: "Synthetic model", BaseURL: "https://example.test", Model: "synthetic"}, Credential: domain.NewSecret("synthetic-token")}, options: application.SummaryOptions{Models: []application.SummaryModelOption{{ID: "model-1", Name: "Synthetic model", Model: "synthetic", Enabled: true}}, Templates: []domain.SummaryTemplate{template}}}
	service := application.NewSummaryService(reader, store, resources, provider, application.NewPet(store))
	t.Cleanup(service.Close)
	return service, store, reader, resources
}

func waitSummary(t *testing.T, store *jobdb.Store, job, id, status string) domain.SummaryRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, err := store.SummaryRun(context.Background(), job, id)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == status {
			return run
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("summary did not reach expected durable state", status)
	return domain.SummaryRun{}
}

func TestSummaryServicePersistsAndPublishesLeaseFencedRun(t *testing.T) {
	service, store, reader, resources := summaryFixture(t, summaryProvider{})
	ctx := context.Background()
	run, err := service.Start(ctx, reader.result.Job.ID, application.SummaryForm{ModelID: resources.model.Metadata.ID, TemplateID: resources.options.Templates[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "running" {
		t.Fatal("acceptance must return durable running state")
	}
	run = waitSummary(t, store, run.JobID, run.ID, "completed")
	if run.Result == nil || run.Result.Overview != "Synthetic overview" || run.MinutesPayload == nil {
		t.Fatal("result/minutes were not published")
	}
	if err := store.CompleteSummary(ctx, domain.SummaryLease{JobID: run.JobID, RunID: run.ID, AttemptID: 1, Token: 1}, domain.SummaryCompletion{}); err == nil {
		t.Fatal("stale completion accepted")
	}
}

type gatedSummaryProvider struct {
	entered chan domain.SummaryRequest
	release chan struct{}
	calls   atomic.Int32
}

func (p *gatedSummaryProvider) Complete(ctx context.Context, request domain.SummaryRequest) (string, error) {
	p.calls.Add(1)
	p.entered <- request
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-p.release:
		return (summaryProvider{}).Complete(ctx, request)
	}
}
func newGatedProvider() *gatedSummaryProvider {
	return &gatedSummaryProvider{entered: make(chan domain.SummaryRequest, 8), release: make(chan struct{})}
}
func waitProvider(t *testing.T, p *gatedSummaryProvider) domain.SummaryRequest {
	t.Helper()
	select {
	case r := <-p.entered:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("provider never called")
		return domain.SummaryRequest{}
	}
}
func TestSummarySurvivesWindowCancellationAndDeduplicates(t *testing.T) {
	provider := newGatedProvider()
	service, store, reader, resources := summaryFixture(t, provider)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	form := application.SummaryForm{ModelID: resources.model.Metadata.ID, TemplateID: resources.options.Templates[0].ID}
	run, err := service.Start(ctx, reader.result.Job.ID, form)
	if err != nil {
		t.Fatal(err)
	}
	request := waitProvider(t, provider)
	if request.BaseURL != resources.model.Metadata.BaseURL {
		t.Fatal("wrong endpoint")
	}
	cancel()
	for range 10 {
		same, err := service.Start(context.Background(), run.JobID, form)
		if err != nil || same.ID != run.ID {
			t.Fatal("duplicate request was not joined", err)
		}
	}
	if provider.calls.Load() != 1 {
		t.Fatal("duplicate paid requests")
	}
	close(provider.release)
	waitSummary(t, store, run.JobID, run.ID, "completed")
	service.Close()
	events, err := store.PetEvents(context.Background(), 100)
	if err != nil || len(events) != 1 || events[0].EventSource != "ai_summary_completed" || events[0].Metadata == nil {
		t.Fatal("summary reward missing or duplicated", err)
	}
	var metadata struct {
		Source string `json:"source"`
	}
	if json.Unmarshal([]byte(*events[0].Metadata), &metadata) != nil || metadata.Source != run.ID {
		t.Fatal("wrong reward key")
	}
}
func TestSummaryShutdownLeavesRecoverableRun(t *testing.T) {
	provider := newGatedProvider()
	service, store, reader, resources := summaryFixture(t, provider)
	run, err := service.Start(context.Background(), reader.result.Job.ID, application.SummaryForm{ModelID: resources.model.Metadata.ID, TemplateID: resources.options.Templates[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	waitProvider(t, provider)
	service.Close()
	if _, err := service.Start(context.Background(), run.JobID, application.SummaryForm{}); !errors.Is(err, domain.ErrSummaryClosed) {
		t.Fatal("closed service accepted work", err)
	}
	runs, err := store.RecoverableSummaries(context.Background())
	if err != nil || len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatal("shutdown lost recovery", err)
	}
	recovered := application.NewSummaryService(reader, store, resources, summaryProvider{})
	defer recovered.Close()
	if err := recovered.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitSummary(t, store, run.JobID, run.ID, "completed")
}
func TestSummaryRecoveryRejectsChangedCredentialEndpoint(t *testing.T) {
	provider := newGatedProvider()
	service, store, reader, resources := summaryFixture(t, provider)
	run, err := service.Start(context.Background(), reader.result.Job.ID, application.SummaryForm{ModelID: resources.model.Metadata.ID, TemplateID: resources.options.Templates[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	waitProvider(t, provider)
	service.Close()
	resources.model.Metadata.BaseURL = "https://replacement.example.test"
	never := newGatedProvider()
	recovered := application.NewSummaryService(reader, store, resources, never)
	defer recovered.Close()
	if err := recovered.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	failed := waitSummary(t, store, run.JobID, run.ID, "failed")
	if failed.ErrorMessage == nil || *failed.ErrorMessage != "ai_model_endpoint_changed" || never.calls.Load() != 0 {
		t.Fatal("changed endpoint received transcript or credential")
	}
}
func TestSummaryDeletionCancelsInFlightRequest(t *testing.T) {
	provider := newGatedProvider()
	service, store, reader, resources := summaryFixture(t, provider)
	run, err := service.Start(context.Background(), reader.result.Job.ID, application.SummaryForm{ModelID: resources.model.Metadata.ID, TemplateID: resources.options.Templates[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	waitProvider(t, provider)
	if err := service.Delete(context.Background(), run.JobID, run.ID); err != nil {
		t.Fatal(err)
	}
	service.Close()
	if _, err := store.SummaryRun(context.Background(), run.JobID, run.ID); !errors.Is(err, domain.ErrSummaryMissing) {
		t.Fatal("deleted run republished", err)
	}
}

func TestSummaryWindowGrantIsBoundAndRevoked(t *testing.T) {
	service, _, reader, _ := summaryFixture(t, summaryProvider{})
	scopes := application.NewWindowScopes(application.NewMeetings(reader))
	scopes.EnableSummary(service)
	ctx := context.Background()
	parent, err := scopes.OpenFromMain(ctx, application.Workbench, "local", reader.result.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parent.SummaryState(ctx); !errors.Is(err, application.ErrWindowScope) {
		t.Fatal("workbench acquired summary mutation port", err)
	}
	child, err := parent.OpenChild(application.AISummary)
	if err != nil {
		t.Fatal(err)
	}
	state, err := child.SummaryState(ctx)
	if err != nil || state.Result.Job.ID != reader.result.Job.ID {
		t.Fatal("summary not bound to job", err)
	}
	replacement, err := parent.OpenChild(application.AISummary)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.GenerateSummary(ctx, application.SummaryForm{}); !errors.Is(err, application.ErrWindowScope) {
		t.Fatal("replaced window generated summary", err)
	}
	replacement.Close()
	if err := replacement.DeleteSummary(ctx, "any"); !errors.Is(err, application.ErrWindowScope) {
		t.Fatal("closed window deleted summary", err)
	}
}

type intermittentSummaryProvider struct{ calls atomic.Int32 }

func (p *intermittentSummaryProvider) Complete(ctx context.Context, r domain.SummaryRequest) (string, error) {
	if p.calls.Add(1) == 2 {
		return "", errors.New("synthetic provider failure")
	}
	return (summaryProvider{}).Complete(ctx, r)
}
func TestSummaryRetryReusesCompletedChunks(t *testing.T) {
	provider := &intermittentSummaryProvider{}
	unused, store, reader, resources := summaryFixture(t, provider)
	unused.Close()
	reader.result.Transcript[0].Text = strings.Repeat("合成会议内容。\n", 6000)
	service := application.NewSummaryService(reader, store, resources, provider)
	defer service.Close()
	ctx := context.Background()
	form := application.SummaryForm{ModelID: resources.model.Metadata.ID, TemplateID: resources.options.Templates[0].ID}
	run, err := service.Start(ctx, reader.result.Job.ID, form)
	if err != nil {
		t.Fatal(err)
	}
	waitSummary(t, store, run.JobID, run.ID, "failed")
	service.Close()
	resumed := application.NewSummaryService(reader, store, resources, provider)
	defer resumed.Close()
	run, err = resumed.Start(ctx, run.JobID, application.SummaryForm{RunID: run.ID})
	if err != nil || run.Status != "running" {
		t.Fatal("failed run did not resume", err)
	}
	waitSummary(t, store, run.JobID, run.ID, "completed")
	prompt := domain.BuildSummaryPrompt(domain.SummaryPromptInput{Job: reader.result, Template: resources.options.Templates[0]})
	plan, err := domain.PlanSummary(prompt)
	if err != nil || len(plan.UserPrompts) < 2 {
		t.Fatal("fixture did not span chunks", err)
	}
	if provider.calls.Load() != int32(len(plan.UserPrompts)+1) {
		t.Fatal("retry sent an already completed chunk again")
	}
}

type summaryDeletionPorts struct{}

func (summaryDeletionPorts) Execute(context.Context, string) error { return nil }
func (summaryDeletionPorts) Trash(string, string) error            { return nil }
func (summaryDeletionPorts) Purge(string) error                    { return nil }
func (summaryDeletionPorts) Terminate(*uint32, *string) error      { return nil }

type cancelObservedProvider struct{ entered chan context.Context }

func (p *cancelObservedProvider) Complete(ctx context.Context, _ domain.SummaryRequest) (string, error) {
	p.entered <- ctx
	<-ctx.Done()
	return "", ctx.Err()
}

func TestWholeJobDeletionCancelsAcceptedSummaryBeforeServiceClose(t *testing.T) {
	provider := &cancelObservedProvider{entered: make(chan context.Context, 1)}
	service, store, reader, resources := summaryFixture(t, provider)
	ctx := context.Background()
	run, err := service.Start(ctx, reader.result.Job.ID, application.SummaryForm{ModelID: resources.model.Metadata.ID, TemplateID: resources.options.Templates[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	var request context.Context
	select {
	case request = <-provider.entered:
	case <-time.After(time.Second):
		t.Fatal("request never started")
	}
	ports := summaryDeletionPorts{}
	scheduler := application.NewScheduler(ports, 1)
	defer scheduler.Shutdown(ctx)
	deleter := application.NewDeleter(store, ports, ports, scheduler, service)
	if err := deleter.Delete(ctx, run.JobID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-request.Done():
	default:
		t.Fatal("whole job deletion left the provider request active")
	}
	if _, err := store.SummaryRun(ctx, run.JobID, run.ID); !errors.Is(err, domain.ErrSummaryMissing) {
		t.Fatal("deleted summary remains", err)
	}
	service.Close()
	if _, err := store.SummaryRun(ctx, run.JobID, run.ID); !errors.Is(err, domain.ErrSummaryMissing) {
		t.Fatal("canceled result republished", err)
	}
}

type summaryAcceptanceBarrier struct {
	*jobdb.Store
	entered, release, tombstone chan struct{}
}

func (s *summaryAcceptanceBarrier) CreateSummary(ctx context.Context, seed domain.SummarySeed) (domain.SummaryRun, error) {
	close(s.entered)
	<-s.release
	return s.Store.CreateSummary(ctx, seed)
}
func (s *summaryAcceptanceBarrier) PrepareDeletion(ctx context.Context, id string) (domain.JobDeletion, error) {
	value, err := s.Store.PrepareDeletion(ctx, id)
	if err == nil {
		close(s.tombstone)
	}
	return value, err
}
func TestJobDeletionFencesSummaryAcceptanceAlreadyInProgress(t *testing.T) {
	provider := newGatedProvider()
	original, store, reader, resources := summaryFixture(t, provider)
	original.Close()
	barrier := &summaryAcceptanceBarrier{Store: store, entered: make(chan struct{}), release: make(chan struct{}), tombstone: make(chan struct{})}
	service := application.NewSummaryService(reader, barrier, resources, provider)
	defer service.Close()
	released := false
	defer func() {
		if !released {
			close(barrier.release)
		}
	}()
	ctx := context.Background()
	started := make(chan error, 1)
	go func() {
		_, err := service.Start(ctx, reader.result.Job.ID, application.SummaryForm{ModelID: resources.model.Metadata.ID, TemplateID: resources.options.Templates[0].ID})
		started <- err
	}()
	select {
	case <-barrier.entered:
	case <-time.After(time.Second):
		t.Fatal("acceptance never entered")
	}
	ports := summaryDeletionPorts{}
	scheduler := application.NewScheduler(ports, 1)
	defer scheduler.Shutdown(ctx)
	deleted := make(chan error, 1)
	go func() {
		deleted <- application.NewDeleter(barrier, ports, ports, scheduler, service).Delete(ctx, reader.result.Job.ID)
	}()
	select {
	case <-barrier.tombstone:
	case <-time.After(time.Second):
		t.Fatal("deletion never fenced the job")
	}
	close(barrier.release)
	released = true
	if err := <-started; !errors.Is(err, application.ErrJobNotFound) {
		t.Fatal("summary acceptance survived deletion", err)
	}
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	if provider.calls.Load() != 0 {
		t.Fatal("deleted job reached provider")
	}
}
