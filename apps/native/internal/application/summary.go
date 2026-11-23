package application

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"liberty.local/liberty/internal/domain"
)

// SummaryService owns the durable AI workflow. The desktop layer only submits
// a form and observes the resulting run; secrets, prompts and leases stay here.
type SummaryService struct {
	reader     MeetingReader
	repository SummaryRepository
	resources  SummaryResources
	provider   SummaryProvider
	pet        PetWorkflow
	now        func() time.Time
	mu         sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
	closed     bool
	workers    sync.WaitGroup
	active     map[string]*summaryWorker
}

func NewSummaryService(reader MeetingReader, repository SummaryRepository, resources SummaryResources, provider SummaryProvider, pet ...PetWorkflow) *SummaryService {
	ctx, cancel := context.WithCancel(context.Background())
	return &SummaryService{reader: reader, repository: repository, resources: resources, provider: provider, pet: optionalPetWorkflow(pet), now: time.Now, ctx: ctx, cancel: cancel, active: make(map[string]*summaryWorker)}
}

type summaryWorker struct {
	run    domain.SummaryRun
	cancel context.CancelFunc
}

func summaryRunID(now time.Time) (string, error) {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", domain.ErrSummaryExecution
	}
	return fmt.Sprintf("summary-run-%013d-%d", now.UnixMilli(), binary.BigEndian.Uint64(nonce[:])), nil
}

// Start durably accepts a request before launching profile-owned work. Closing
// the submitting window cancels only acceptance/observation, never accepted AI.
func (s *SummaryService) Start(ctx context.Context, jobID string, form SummaryForm) (domain.SummaryRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return domain.SummaryRun{}, domain.ErrSummaryClosed
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	defer cancel()
	if err := ctx.Err(); err != nil {
		return domain.SummaryRun{}, err
	}
	if active := s.active[jobID]; active != nil {
		if form.RunID != "" && form.RunID != active.run.ID {
			return domain.SummaryRun{}, domain.ErrSummaryRunning
		}
		return s.repository.SummaryRun(ctx, jobID, active.run.ID)
	}
	run, err := s.prepare(ctx, jobID, form)
	if err != nil || run.Status == "completed" {
		return run, err
	}
	lease, err := s.repository.ClaimSummary(ctx, run.JobID, run.ID, s.timestamp())
	if err != nil {
		return run, err
	}
	if lease == nil {
		return s.repository.SummaryRun(ctx, run.JobID, run.ID)
	}
	run.Status, run.ErrorMessage, run.UpdatedAt = "running", nil, s.timestamp()
	workerCtx, workerCancel := context.WithCancel(s.ctx)
	worker := &summaryWorker{run: run, cancel: workerCancel}
	s.active[jobID] = worker
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		defer workerCancel()
		_, _ = s.execute(workerCtx, run, lease)
		s.mu.Lock()
		if s.active[jobID] == worker {
			delete(s.active, jobID)
		}
		s.mu.Unlock()
	}()
	return run, nil
}

// Recover resumes only durable unfinished executions; completed chunks remain
// reusable. Call after deletion recovery and before accepting desktop input.
func (s *SummaryService) Recover(ctx context.Context) error {
	runs, err := s.repository.RecoverableSummaries(ctx)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if _, err := s.Start(ctx, run.JobID, SummaryForm{RunID: run.ID}); err != nil {
			return err
		}
	}
	return nil
}

func (s *SummaryService) Close() {
	s.cancel()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.workers.Wait()
}

func (s *SummaryService) prepare(ctx context.Context, jobID string, form SummaryForm) (domain.SummaryRun, error) {
	if domain.ValidateJobID(jobID) != nil {
		return domain.SummaryRun{}, ErrJobNotFound
	}
	if form.RunID != "" {
		run, err := s.repository.SummaryRun(ctx, jobID, strings.TrimSpace(form.RunID))
		if err != nil {
			return domain.SummaryRun{}, err
		}
		return run, nil
	}
	if existing, err := s.repository.RunningSummary(ctx, jobID); err != nil {
		return domain.SummaryRun{}, err
	} else if existing != nil {
		return *existing, nil
	}
	r, err := s.reader.ReadResult(ctx, jobID)
	if err != nil {
		return domain.SummaryRun{}, err
	}
	if r.Job.ID != jobID || r.Job.Source != "local" || len(r.PrimarySegments()) == 0 {
		return domain.SummaryRun{}, domain.ErrSummaryExecution
	}
	model, err := s.resources.SummaryModel(ctx, form.ModelID)
	if err != nil {
		return domain.SummaryRun{}, err
	}
	options, err := s.resources.SummaryOptions(ctx)
	if err != nil {
		return domain.SummaryRun{}, err
	}
	var template domain.SummaryTemplate
	for _, candidate := range options.Templates {
		if candidate.ID == form.TemplateID {
			template = candidate
			break
		}
	}
	if err := template.Validate(); err != nil {
		return domain.SummaryRun{}, domain.ErrResourceMissing
	}
	includeSpeaker := form.IncludeSpeaker && r.Job.DiarizationStatus == "completed" && len(r.Speakers) > 0
	members := []domain.MeetingMember{}
	if form.UseMembers && includeSpeaker {
		members, err = s.resources.Members(ctx)
		if err != nil {
			return domain.SummaryRun{}, err
		}
	}
	prompt := domain.BuildSummaryPrompt(domain.SummaryPromptInput{Job: r, Template: template, IncludeSpeaker: includeSpeaker, IncludeTimestamp: form.IncludeTimestamp, UseMembers: form.UseMembers && includeSpeaker, Members: members, Extra: strings.TrimSpace(form.Extra)})
	plan, err := domain.PlanSummary(prompt)
	if err != nil || len(plan.UserPrompts) == 0 {
		return domain.SummaryRun{}, domain.ErrSummaryPrompt
	}
	transcriptJSON, err := domain.SummaryJSON(r.PrimarySegments())
	if err != nil {
		return domain.SummaryRun{}, domain.ErrSummaryExecution
	}
	hash := domain.SummaryHash(transcriptJSON)
	snapshot := domain.SummaryExecutionSnapshot{SchemaVersion: 1, Job: domain.SnapshotSummaryJob(r), ModelBaseURL: model.Metadata.BaseURL, ModelName: model.Metadata.Model, TemplateID: template.ID, Members: members, RequiredSpeakers: plan.RequiredSpeakers, FallbackTitle: plan.FallbackTitle, SystemPrompt: prompt.System}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return domain.SummaryRun{}, domain.ErrSummaryExecution
	}
	now := s.now().UTC()
	created := domain.PersistedUTC(now)
	id, err := summaryRunID(now)
	if err != nil {
		return domain.SummaryRun{}, err
	}
	chunks := make([]domain.SummaryChunk, 0, len(plan.UserPrompts))
	for i, user := range plan.UserPrompts {
		chunks = append(chunks, domain.SummaryChunk{Index: i, SHA256: domain.SummaryHash([]byte(user)), UserPrompt: user})
	}
	run := domain.SummaryRun{ID: id, JobID: jobID, ModelConfigID: model.Metadata.ID, TemplateID: template.ID, IncludeSpeaker: includeSpeaker, IncludeTimestamp: form.IncludeTimestamp, ExtraInstructions: strings.TrimSpace(form.Extra), Status: "running", PromptPreview: stringPtr(prompt.System + "\n\n---\n\n" + prompt.User), CreatedAt: created, UpdatedAt: created}
	seed := domain.SummarySeed{Execution: domain.SummaryExecution{Run: run, TranscriptRevision: "sha256:" + hash, TranscriptSHA256: hash, TranscriptJSON: string(transcriptJSON), SnapshotJSON: string(snapshotJSON), ChunkCount: len(chunks)}, Chunks: chunks}
	persisted, err := s.repository.CreateSummary(ctx, seed)
	if err != nil {
		return domain.SummaryRun{}, err
	}
	return persisted, nil
}

func (s *SummaryService) execute(ctx context.Context, run domain.SummaryRun, lease *domain.SummaryLease) (value domain.SummaryRun, resultErr error) {
	failure := "ai_execution_failed"
	defer func() {
		// Shutdown leaves a resumable running row. A normal failure is terminal,
		// with completed chunks retained for an explicit retry.
		if resultErr != nil && ctx.Err() == nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.repository.FailSummary(cleanup, *lease, failure, s.timestamp())
		}
	}()
	execution, err := s.repository.LoadSummaryExecution(ctx, *lease)
	if err != nil {
		return run, err
	}
	failure = "ai_execution_snapshot_invalid"
	snapshot, err := execution.Snapshot()
	if err != nil {
		return run, err
	}
	chunks, err := s.repository.SummaryChunks(ctx, *lease)
	if err != nil {
		return run, err
	}
	if len(chunks) != execution.ChunkCount {
		return run, domain.ErrSummaryExecution
	}
	for i, c := range chunks {
		if c.Index != i || c.UserPrompt == "" || domain.SummaryHash([]byte(c.UserPrompt)) != c.SHA256 {
			return run, domain.ErrSummaryExecution
		}
		if c.Completed {
			parsed, e := domain.ParseSummaryStructured(c.Raw)
			encoded, encodeErr := domain.SummaryJSON(parsed)
			if e != nil || encodeErr != nil || string(encoded) != c.Structured {
				return run, domain.ErrSummaryExecution
			}
		}
	}
	failure = "ai_model_unavailable"
	model, err := s.resources.SummaryModel(ctx, execution.Run.ModelConfigID)
	if err != nil {
		return run, err
	}
	// A changed endpoint must never receive the old transcript, nor may the old
	// endpoint receive a replacement provider's credential on automatic recovery.
	if strings.TrimRight(model.Metadata.BaseURL, "/") != strings.TrimRight(snapshot.ModelBaseURL, "/") {
		failure = "ai_model_endpoint_changed"
		return run, domain.ErrSummaryEndpoint
	}
	responses := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if err := ctx.Err(); err != nil {
			return run, err
		}
		if chunk.Completed {
			responses = append(responses, chunk.Raw)
			continue
		}
		// Check ownership immediately before each outbound request. Deletion can
		// still cancel an in-flight request; its eventual result cannot publish.
		if _, err := s.repository.LoadSummaryExecution(ctx, *lease); err != nil {
			return run, err
		}
		failure = "ai_request_failed"
		raw, err := s.provider.Complete(ctx, domain.SummaryRequest{BaseURL: snapshot.ModelBaseURL, Model: snapshot.ModelName, SystemPrompt: snapshot.SystemPrompt, UserPrompt: chunk.UserPrompt, Credential: model.Credential})
		if err != nil {
			return run, err
		}
		failure = "ai_response_invalid"
		parsed, err := domain.ParseSummaryStructured(raw)
		if err != nil {
			return run, err
		}
		structured, err := domain.SummaryJSON(parsed)
		if err != nil {
			return run, domain.ErrSummaryExecution
		}
		failure = "ai_execution_failed"
		if err := s.repository.SaveSummaryChunk(ctx, *lease, domain.SummaryChunk{Index: chunk.Index, SHA256: chunk.SHA256, Raw: raw, Structured: string(structured)}, s.timestamp()); err != nil {
			return run, err
		}
		responses = append(responses, raw)
	}
	failure = "ai_response_invalid"
	merged, err := domain.MergeSummaryResponses(responses, snapshot.FallbackTitle, snapshot.RequiredSpeakers)
	if err != nil {
		return run, err
	}
	result, err := domain.ParseSummaryResponse(merged, snapshot.FallbackTitle)
	if err != nil {
		return run, err
	}
	failure = "minutes_payload_invalid"
	minutes, err := domain.ReconstructMinutes(snapshot.Job.Result(), result, snapshot.Members, snapshot.TemplateID, &run.ID)
	if err != nil {
		return run, err
	}
	missing, unmatched := []string{}, []string{}
	for _, report := range minutes.SpeakerReports {
		if report.MatchStatus == "missing_from_ai" {
			missing = append(missing, report.SpeakerLabel)
		}
		if report.MatchStatus == "unmatched" {
			unmatched = append(unmatched, report.SpeakerLabel)
		}
	}
	diagnostics, err := json.Marshal(map[string]any{"schemaVersion": 1, "transcriptRevision": execution.TranscriptRevision, "transcriptSha256": execution.TranscriptSHA256, "chunkCount": execution.ChunkCount, "attemptId": lease.AttemptID, "leaseToken": lease.Token, "minutesSchemaVersion": minutes.SchemaVersion, "missingFromAi": missing, "unmatchedSpeakers": unmatched})
	if err != nil {
		return run, domain.ErrSummaryExecution
	}
	failure = "ai_execution_failed"
	if err := s.repository.CompleteSummary(ctx, *lease, domain.SummaryCompletion{Raw: merged, Result: result, Minutes: minutes, Diagnostics: string(diagnostics), CompletedAt: s.timestamp()}); err != nil {
		return run, err
	}
	recordPetWorkflow(ctx, s.pet, "ai_summary_completed", run.ID)
	return s.repository.SummaryRun(ctx, run.JobID, run.ID)
}

func (s *SummaryService) timestamp() string { return domain.PersistedUTC(s.now()) }
func stringPtr(value string) *string        { return &value }
