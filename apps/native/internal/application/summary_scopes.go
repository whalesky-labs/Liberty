package application

import (
	"context"

	"liberty.local/liberty/internal/domain"
)

// SummaryWindowState contains public metadata and results only. Provider
// endpoints, credential references and secret values never cross this port.
type SummaryWindowState struct {
	Result  domain.Result
	Options SummaryOptions
	Runs    []domain.SummaryRun
}

func (s *WindowScopes) EnableSummary(service *SummaryService) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.summary = service
}
func (g *JobWindow) withSummary(ctx context.Context, call func(*SummaryService) error) error {
	if g == nil || g.owner == nil {
		return ErrWindowScope
	}
	g.owner.mu.Lock()
	defer g.owner.mu.Unlock()
	if !g.authorized() || g.role != AISummary || g.source != "local" || g.owner.summary == nil {
		return ErrWindowScope
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return call(g.owner.summary)
}
func (g *JobWindow) SummaryState(ctx context.Context) (SummaryWindowState, error) {
	var state SummaryWindowState
	err := g.withSummary(ctx, func(s *SummaryService) error {
		r, err := g.owner.readResult(ctx, g.source, g.id)
		if err != nil {
			return err
		}
		if r.Job.ID != g.id || r.Job.Source != "local" {
			return ErrWindowScope
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		state.Result = r
		state.Options, err = s.Options(ctx)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		state.Runs, err = s.Runs(ctx, g.id)
		if err != nil {
			return err
		}
		return ctx.Err()
	})
	if err != nil {
		return SummaryWindowState{}, err
	}
	return state, err
}
func (g *JobWindow) GenerateSummary(ctx context.Context, form SummaryForm) error {
	return g.withSummary(ctx, func(s *SummaryService) error { _, err := s.Start(ctx, g.id, form); return err })
}
func (g *JobWindow) SelectSummary(ctx context.Context, id string) error {
	return g.withSummary(ctx, func(s *SummaryService) error { return s.Select(ctx, g.id, id) })
}
func (g *JobWindow) DeleteSummary(ctx context.Context, id string) error {
	return g.withSummary(ctx, func(s *SummaryService) error { return s.Delete(ctx, g.id, id) })
}
