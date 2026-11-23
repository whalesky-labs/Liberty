package application

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"liberty.local/liberty/internal/domain"
)

var ErrJobInput = errors.New("job_input_invalid")

type JobDefaults struct{ Hotwords, SummaryTemplate string }
type SelectedMedia struct{ Token, Name, Kind string }
type MediaInput struct {
	Source domain.SourceFile
	Path   string
}
type MediaSelections interface {
	Resolve(context.Context, string) (MediaInput, error)
	Release(string)
}
type SubmissionStore interface {
	Create(context.Context, domain.JobInput) error
	Input(context.Context, string) (domain.JobInput, error)
	Retry(context.Context, string, string, string) error
	JobDefaults(context.Context) (JobDefaults, error)
}
type JobDirectories interface{ CreateJob(string) error }
type RuntimeReadiness interface {
	Status(context.Context) (domain.RuntimeOverview, error)
}
type CreateMeeting struct {
	Selection, Title, Language, Hotwords, SummaryTemplate, SizeLabel string
	Speaker                                                          bool
}
type Submission struct {
	mu        sync.Mutex
	store     SubmissionStore
	media     MediaSelections
	runtime   RuntimeReadiness
	files     JobDirectories
	scheduler *Scheduler
	runner    string
	clock     func() time.Time
	pet       PetWorkflow
}

func NewSubmission(s SubmissionStore, m MediaSelections, r RuntimeReadiness, f JobDirectories, q *Scheduler, script string, pet ...PetWorkflow) *Submission {
	return &Submission{store: s, media: m, runtime: r, files: f, scheduler: q, runner: script, clock: time.Now, pet: optionalPetWorkflow(pet)}
}
func (s *Submission) Defaults(ctx context.Context) (JobDefaults, error) {
	return s.store.JobDefaults(ctx)
}
func (s *Submission) Create(ctx context.Context, form CreateMeeting) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defaults, err := s.store.JobDefaults(ctx)
	if err != nil {
		return "", err
	}
	if mode, ok := s.store.(interface {
		ProcessingMode(context.Context) (string, error)
	}); ok {
		processingMode, err := mode.ProcessingMode(ctx)
		if err != nil {
			return "", err
		}
		if processingMode == "remote" {
			return "", domain.ErrRemoteUnsupported
		}
	}
	if strings.TrimSpace(form.Title) == "" || form.Selection == "" {
		return "", ErrJobInput
	}
	switch form.Language {
	case "zh-CN", "en-US", "ja-JP":
	default:
		return "", ErrJobInput
	}
	media, err := s.media.Resolve(ctx, form.Selection)
	if err != nil {
		return "", err
	}
	status, err := s.runtime.Status(ctx)
	if err != nil {
		return "", err
	}
	if !status.ShellReady {
		return "", domain.ErrRuntime
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", ErrJobInput
	}
	now := s.clock()
	id := fmt.Sprintf("job-%013d-%d", now.UnixMilli(), binary.BigEndian.Uint64(nonce[:]))
	if err := domain.ValidateJobID(id); err != nil {
		return "", err
	}
	words := []string{}
	for _, word := range strings.Split(form.Hotwords, ",") {
		if word = strings.TrimSpace(word); word != "" {
			words = append(words, word)
		}
	}
	template := strings.TrimSpace(form.SummaryTemplate)
	if template == "" {
		template = defaults.SummaryTemplate
	}
	media.Source.ID = "source-" + id
	media.Source.SizeLabel = form.SizeLabel
	input := domain.JobInput{Job: domain.JobSummary{ID: id, Source: "local", Title: strings.TrimSpace(form.Title), Lang: form.Language, CreatedAt: now.UTC().Format("2006-01-02T15:04:05.000Z"), EnableSpeaker: form.Speaker, Hotwords: words, SummaryTemplate: template, SourceFiles: []domain.SourceFile{media.Source}}, SourcePath: media.Path, RunnerPath: s.runner}
	if err := s.files.CreateJob(id); err != nil {
		return "", err
	}
	if err := s.store.Create(ctx, input); err != nil {
		return "", err
	}
	s.media.Release(form.Selection)
	recordPetWorkflow(ctx, s.pet, "job_created", id)
	// Once durable, return the created ID even if shutdown has stopped launches.
	// The queued snapshot is recovered on the next startup, avoiding duplicates.
	_ = s.scheduler.Enqueue(id)
	return id, nil
}
func (s *Submission) Retry(ctx context.Context, id string) error {
	reservation, err := s.scheduler.Reserve(ctx, id)
	if err != nil {
		return err
	}
	defer reservation.Restore()
	input, err := s.store.Input(ctx, id)
	if err != nil {
		return err
	}
	if strings.TrimSpace(input.SourcePath) == "" {
		return ErrJobInput
	}
	if err := s.files.CreateJob(id); err != nil {
		return err
	}
	if err := s.store.Retry(ctx, id, "", s.runner); err != nil {
		return err
	}
	reservation.Release(true)
	return nil
}
