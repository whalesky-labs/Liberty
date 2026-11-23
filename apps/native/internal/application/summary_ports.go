package application

import (
	"context"
	"liberty.local/liberty/internal/domain"
)

type SummaryRepository interface {
	ListSummaries(context.Context, string) ([]domain.SummaryRun, error)
	SummaryRun(context.Context, string, string) (domain.SummaryRun, error)
	RunningSummary(context.Context, string) (*domain.SummaryRun, error)
	CreateSummary(context.Context, domain.SummarySeed) (domain.SummaryRun, error)
	ClaimSummary(context.Context, string, string, string) (*domain.SummaryLease, error)
	LoadSummaryExecution(context.Context, domain.SummaryLease) (domain.SummaryExecution, error)
	SummaryChunks(context.Context, domain.SummaryLease) ([]domain.SummaryChunk, error)
	SaveSummaryChunk(context.Context, domain.SummaryLease, domain.SummaryChunk, string) error
	CompleteSummary(context.Context, domain.SummaryLease, domain.SummaryCompletion) error
	FailSummary(context.Context, domain.SummaryLease, string, string) error
	RecoverableSummaries(context.Context) ([]domain.SummaryRun, error)
	SelectSummary(context.Context, string, string) error
	DeleteSummary(context.Context, string, string) error
}
type SummaryProvider interface {
	Complete(context.Context, domain.SummaryRequest) (string, error)
}
type SummaryModel struct {
	Metadata   domain.AIModel
	Credential domain.Secret
}
type SummaryResources interface {
	SummaryModel(context.Context, string) (SummaryModel, error)
	SummaryOptions(context.Context) (SummaryOptions, error)
	Members(context.Context) ([]domain.MeetingMember, error)
}
type SummaryModelOption struct {
	ID, Name, Model    string
	Enabled, IsDefault bool
}
type SummaryOptions struct {
	Models    []SummaryModelOption
	Templates []domain.SummaryTemplate
}
type SummaryForm struct {
	RunID, ModelID, TemplateID, Extra            string
	IncludeSpeaker, IncludeTimestamp, UseMembers bool
}

func (s *SummaryService) Options(ctx context.Context) (SummaryOptions, error) {
	return s.resources.SummaryOptions(ctx)
}
func (s *SummaryService) Runs(ctx context.Context, jobID string) ([]domain.SummaryRun, error) {
	return s.repository.ListSummaries(ctx, jobID)
}
func (s *SummaryService) Select(ctx context.Context, jobID, runID string) error {
	return s.repository.SelectSummary(ctx, jobID, runID)
}
func (s *SummaryService) Delete(ctx context.Context, jobID, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return domain.ErrSummaryClosed
	}
	if err := s.repository.DeleteSummary(ctx, jobID, runID); err != nil {
		return err
	}
	if worker := s.active[jobID]; worker != nil && worker.run.ID == runID {
		worker.cancel()
	}
	return nil
}

// Resolve credentials under the same lock used for replacement/retirement.
// Child-window options contain no secret, reference or provider address.
func (s *Resources) SummaryModel(ctx context.Context, id string) (SummaryModel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return SummaryModel{}, domain.ErrSummaryClosed
	}
	r, err := s.repository.Model(ctx, id)
	if err != nil {
		return SummaryModel{}, err
	}
	if !r.Metadata.Enabled {
		return SummaryModel{}, domain.ErrResourceMissing
	}
	if r.LegacyPresent {
		return SummaryModel{}, domain.ErrCredentialMigration
	}
	if r.Reference == "" {
		return SummaryModel{}, domain.ErrCredentialRequired
	}
	secret, err := s.credentials.Get(ctx, r.Reference)
	if err != nil {
		return SummaryModel{}, err
	}
	if secret.Empty() {
		return SummaryModel{}, domain.ErrCredentialRequired
	}
	return SummaryModel{Metadata: r.Metadata, Credential: secret}, nil
}
func (s *Resources) SummaryOptions(ctx context.Context) (SummaryOptions, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	options := SummaryOptions{Models: []SummaryModelOption{}, Templates: []domain.SummaryTemplate{}}
	if s.closed {
		return options, domain.ErrSummaryClosed
	}
	records, err := s.repository.Models(ctx)
	if err != nil {
		return options, err
	}
	for _, r := range records {
		m := r.Metadata
		options.Models = append(options.Models, SummaryModelOption{ID: m.ID, Name: m.Name, Model: m.Model, Enabled: m.Enabled, IsDefault: m.IsDefault})
	}
	options.Templates, err = s.repository.Templates(ctx)
	return options, err
}
