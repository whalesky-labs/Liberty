package application

import (
	"context"
	"liberty.local/liberty/internal/domain"
)

type SegmentPlayer interface {
	Play(context.Context, string, domain.Segment, float64) error
}

func (s *WindowScopes) EnablePlayback(player SegmentPlayer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.player = player
}

func (g *JobWindow) PlaySegment(ctx context.Context, segmentID string, speed float64) error {
	if g == nil || g.owner == nil {
		return ErrWindowScope
	}
	if speed != .75 && speed != 1 && speed != 1.25 && speed != 1.5 && speed != 2 {
		return domain.ErrTranscriptEdit
	}
	release, err := g.lockMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	g.owner.mu.Lock()
	allowed := g.authorized() && g.role == Workbench && g.source == "local" && g.owner.player != nil
	player := g.owner.player
	g.owner.mu.Unlock()
	if !allowed {
		return ErrWindowScope
	}
	r, err := g.readResult(ctx)
	if err != nil {
		return err
	}
	for _, segment := range r.PrimarySegments() {
		if segment.ID == segmentID && segment.EndMS > segment.StartMS {
			return player.Play(ctx, g.id, segment, speed)
		}
	}
	return domain.ErrTranscriptEdit
}
