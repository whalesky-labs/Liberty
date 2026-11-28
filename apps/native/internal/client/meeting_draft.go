package client

import (
	"context"
	"strings"

	"liberty.local/liberty/internal/domain"
)

type MeetingDraftWriter interface {
	SaveMeetingDraft(context.Context, domain.MeetingDraft) error
}

type NotesController struct {
	Editing, Busy                                              bool
	Error                                                      error
	Overview, Topics, Decisions, ActionItems, Risks, FollowUps string
	Sources                                                    map[string][]string
	draft                                                      domain.MeetingDraft
	service                                                    MeetingDraftWriter
	dispatch                                                   func(func())
	changed, saved                                             func()
	ctx                                                        context.Context
	cancel                                                     context.CancelFunc
	closed                                                     bool
}

func NewNotes(service MeetingDraftWriter, dispatch func(func()), changed, saved func()) *NotesController {
	ctx, cancel := context.WithCancel(context.Background())
	return &NotesController{service: service, dispatch: dispatch, changed: changed, saved: saved, ctx: ctx, cancel: cancel}
}

func (c *NotesController) Begin(result domain.Result) {
	if c.closed || c.Busy || c.Editing {
		return
	}
	data, err := domain.SummaryJSON(result.PrimarySegments())
	if err != nil {
		c.Error = err
		return
	}
	c.draft = domain.MeetingDraft{Summary: result.Summary, TranscriptSHA256: domain.SummaryHash(data)}
	if result.Draft != nil {
		c.draft = *result.Draft
	} else if result.Job.ActiveSummaryRunID != nil {
		c.draft.SourceRunID = *result.Job.ActiveSummaryRunID
	}
	c.draft.TranscriptSHA256 = domain.SummaryHash(data)
	c.Sources = make(map[string][]string)
	for section, ids := range c.draft.Sources {
		c.Sources[section] = append([]string(nil), ids...)
	}
	s := c.draft.Summary
	c.Overview, c.Topics, c.Decisions = s.Overview, strings.Join(s.Topics, "\n"), strings.Join(s.Decisions, "\n")
	c.ActionItems, c.Risks, c.FollowUps = strings.Join(s.ActionItems, "\n"), strings.Join(s.Risks, "\n"), strings.Join(s.FollowUps, "\n")
	c.Editing, c.Error = true, nil
	c.changed()
}

func noteLines(text string) []string {
	values := []string{}
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			values = append(values, line)
		}
	}
	return values
}

func (c *NotesController) Save() {
	if c.closed || c.Busy || !c.Editing {
		return
	}
	draft := c.draft
	draft.Sources = make(map[string][]string)
	for section, ids := range c.Sources {
		draft.Sources[section] = append([]string(nil), ids...)
	}
	draft.Summary = domain.Summary{Overview: c.Overview, Topics: noteLines(c.Topics), Decisions: noteLines(c.Decisions), ActionItems: noteLines(c.ActionItems), Risks: noteLines(c.Risks), FollowUps: noteLines(c.FollowUps)}
	if err := draft.Validate(); err != nil {
		c.Error = err
		c.changed()
		return
	}
	c.Busy, c.Error = true, nil
	c.changed()
	go func() {
		err := c.service.SaveMeetingDraft(c.ctx, draft)
		c.dispatch(func() {
			if c.closed {
				return
			}
			c.Busy, c.Error = false, err
			if err == nil {
				c.Editing = false
				c.saved()
			}
			c.changed()
		})
	}()
}

func (c *NotesController) Cancel() {
	if !c.Busy && !c.closed {
		c.Editing, c.Error = false, nil
		c.changed()
	}
}

func (c *NotesController) Close() { c.closed = true; c.cancel() }
