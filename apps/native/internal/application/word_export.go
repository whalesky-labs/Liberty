package application

import (
	"context"
	"liberty.local/liberty/internal/domain"
)

type MinutesRepository interface {
	Members(context.Context) ([]domain.MeetingMember, error)
	BackfillMinutes(context.Context, string, string, domain.MinutesPayload) (bool, error)
}
type WordExport struct {
	reader     resultReader
	repository MinutesRepository
}

func NewWordExport(reader resultReader, repository MinutesRepository) *WordExport {
	return &WordExport{reader, repository}
}
func (e *WordExport) Prepare(ctx context.Context, id, requested string) (domain.MinutesDocument, error) {
	members, err := e.repository.Members(ctx)
	if err != nil {
		return domain.MinutesDocument{}, err
	}
	r, err := e.reader.Result(ctx, id)
	if err != nil {
		return domain.MinutesDocument{}, err
	}
	if r.Job.ID != id || r.Job.Source != "local" {
		return domain.MinutesDocument{}, ErrWindowScope
	}
	source, err := domain.ResolveSummaryExport(r, members, requested)
	if err != nil {
		return domain.MinutesDocument{}, err
	}
	if source.ReconstructedPayload {
		if source.RunID == nil {
			return domain.MinutesDocument{}, domain.ErrMinutesSource
		}
		updated, saveErr := e.repository.BackfillMinutes(ctx, id, *source.RunID, source.MinutesPayload)
		// Like the original exporter, a backfill failure doesn't discard a valid
		// reconstructed export. A concurrent valid payload takes precedence.
		if saveErr == nil && !updated {
			r, err = e.reader.Result(ctx, id)
			if err != nil {
				return domain.MinutesDocument{}, err
			}
			source, err = domain.ResolveSummaryExport(r, members, requested)
			if err != nil {
				return domain.MinutesDocument{}, err
			}
		}
	}
	if ctx.Err() != nil {
		return domain.MinutesDocument{}, ctx.Err()
	}
	return domain.BuildMinutesDocument(r, source, members), nil
}

func (s *WindowScopes) EnableWordExport(exporter *WordExport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.word = exporter
}
func (g *JobWindow) ExportWord(ctx context.Context, run string, publish func(domain.MinutesDocument) error) error {
	if g == nil || g.owner == nil {
		return ErrWindowScope
	}
	g.owner.mu.Lock()
	defer g.owner.mu.Unlock()
	if !g.authorized() || g.role != Workbench || g.source != "local" || g.owner.word == nil || publish == nil {
		return ErrWindowScope
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	document, err := g.owner.word.Prepare(ctx, g.id, run)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return g.finishExport(ctx, publish(document))
}
