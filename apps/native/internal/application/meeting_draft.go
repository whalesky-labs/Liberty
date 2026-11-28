package application

import (
	"context"
	"liberty.local/liberty/internal/domain"
)

type MeetingDraftWriter interface {
	SaveMeetingDraft(context.Context, string, domain.MeetingDraft) error
}

func (m *ModeMeetings) SaveMeetingDraft(ctx context.Context, id string, draft domain.MeetingDraft) error {
	writer, ok := m.speaker.(MeetingDraftWriter)
	if !ok {
		return ErrWindowScope
	}
	return writer.SaveMeetingDraft(ctx, id, draft)
}

func (g *JobWindow) SaveMeetingDraft(ctx context.Context, draft domain.MeetingDraft) error {
	if g == nil || g.owner == nil {
		return ErrWindowScope
	}
	release, err := g.lockMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	g.owner.mu.Lock()
	writer, ok := g.owner.writer.(MeetingDraftWriter)
	allowed := g.authorized() && g.role == MeetingNotes && g.source == "local" && ok
	g.owner.mu.Unlock()
	if !allowed {
		return ErrWindowScope
	}
	if err := draft.Validate(); err != nil {
		return err
	}
	r, err := g.readResult(ctx)
	if err != nil {
		return err
	}
	data, err := domain.SummaryJSON(r.PrimarySegments())
	if err != nil {
		return err
	}
	if draft.TranscriptSHA256 != domain.SummaryHash(data) {
		return domain.ErrMeetingDraftConflict
	}
	if draft.SourceRunID != "" {
		found := false
		for _, run := range r.SummaryRuns {
			found = found || run.ID == draft.SourceRunID
		}
		if !found {
			return domain.ErrMeetingDraftConflict
		}
	}
	ids := map[string]bool{}
	for _, segment := range r.PrimarySegments() {
		ids[segment.ID] = true
	}
	for _, sources := range draft.Sources {
		for _, id := range sources {
			if !ids[id] {
				return domain.ErrMeetingDraftConflict
			}
		}
	}
	return writer.SaveMeetingDraft(ctx, g.id, draft)
}
