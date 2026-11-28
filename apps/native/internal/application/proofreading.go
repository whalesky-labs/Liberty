package application

import (
	"context"
	"liberty.local/liberty/internal/domain"
)

type TranscriptWriter interface {
	EditSegment(context.Context, string, domain.SegmentEdit) error
}

func (m *ModeMeetings) EditSegment(ctx context.Context, id string, edit domain.SegmentEdit) error {
	writer, ok := m.speaker.(TranscriptWriter)
	if !ok {
		return ErrWindowScope
	}
	// A window is already bound to its original local source. Changing the
	// main window's mode must not retarget this mutation to a remote job.
	return writer.EditSegment(ctx, id, edit)
}

func (g *JobWindow) EditSegment(ctx context.Context, edit domain.SegmentEdit) error {
	if g == nil || g.owner == nil {
		return ErrWindowScope
	}
	release, err := g.lockMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	g.owner.mu.Lock()
	writer, ok := g.owner.writer.(TranscriptWriter)
	allowed := g.authorized() && g.role == Workbench && g.source == "local" && ok
	g.owner.mu.Unlock()
	if !allowed {
		return ErrWindowScope
	}
	result, err := g.readResult(ctx)
	if err != nil {
		return err
	}
	if result.Job.ID != g.id || result.Job.Source != g.source || !result.CanEditTranscript() {
		return ErrWindowScope
	}
	if err := edit.Validate(); err != nil {
		return err
	}
	return writer.EditSegment(ctx, g.id, edit)
}
