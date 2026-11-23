package application

import (
	"context"
	"sync"

	"liberty.local/liberty/internal/domain"
)

type ResourceKind string

const (
	Models    ResourceKind = "models"
	Templates ResourceKind = "templates"
	Members   ResourceKind = "members"
)

type ResourceDraft struct {
	Kind     ResourceKind
	Model    domain.AIModel
	Template domain.SummaryTemplate
	Member   domain.MeetingMember
}

func (d ResourceDraft) ID() string {
	switch d.Kind {
	case Models:
		return d.Model.ID
	case Templates:
		return d.Template.ID
	case Members:
		return d.Member.ID
	}
	return ""
}
func (d ResourceDraft) Name() string {
	switch d.Kind {
	case Models:
		return d.Model.Name
	case Templates:
		return d.Template.Name
	case Members:
		return d.Member.Name
	}
	return ""
}
func NewResourceDraft(kind ResourceKind) ResourceDraft {
	return ResourceDraft{Kind: kind, Model: domain.AIModel{Enabled: true}, Template: domain.SummaryTemplate{IncludeSpeaker: true, IncludeTimestamp: true}}
}

// ResourceEditor is a process-local capability for one selected resource. It
// exposes neither settings nor credentials, other entities, or raw repositories.
// Reset/new and template duplication are explicit actions of that editor.
type ResourceEditor struct {
	mu      sync.Mutex
	service *Resources
	kind    ResourceKind
	id      string
	closed  bool
}

func (s *Resources) OpenEditor(ctx context.Context, kind ResourceKind, id string) (*ResourceEditor, ResourceDraft, error) {
	d := NewResourceDraft(kind)
	if kind != Models && kind != Templates && kind != Members {
		return nil, d, ErrWindowScope
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, d, ErrWindowScope
	}
	if err := ctx.Err(); err != nil {
		return nil, d, err
	}
	if id == "" {
		return s.grantEditor(ctx, d)
	}
	switch kind {
	case Models:
		values, err := s.Models(ctx)
		if err != nil {
			return nil, d, err
		}
		for _, v := range values {
			if v.ID == id {
				d.Model = v
				return s.grantEditor(ctx, d)
			}
		}
	case Templates:
		values, err := s.Templates(ctx)
		if err != nil {
			return nil, d, err
		}
		for _, v := range values {
			if v.ID == id {
				d.Template = v
				return s.grantEditor(ctx, d)
			}
		}
	case Members:
		values, err := s.Members(ctx)
		if err != nil {
			return nil, d, err
		}
		for _, v := range values {
			if v.ID == id {
				d.Member = v
				return s.grantEditor(ctx, d)
			}
		}
	}
	return nil, d, domain.ErrResourceMissing
}
func (s *Resources) grantEditor(ctx context.Context, draft ResourceDraft) (*ResourceEditor, ResourceDraft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, NewResourceDraft(draft.Kind), ErrWindowScope
	}
	if err := ctx.Err(); err != nil {
		return nil, NewResourceDraft(draft.Kind), err
	}
	return &ResourceEditor{service: s, kind: draft.Kind, id: draft.ID()}, draft, nil
}
func (g *ResourceEditor) Save(ctx context.Context, d ResourceDraft, credential domain.CredentialUpdate) (ResourceDraft, MutationOutcome, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || d.Kind != g.kind || d.ID() != g.id {
		return d, MutationOutcome{}, ErrWindowScope
	}
	var err error
	var outcome MutationOutcome
	switch g.kind {
	case Models:
		d.Model, outcome, err = g.service.SaveModel(ctx, d.Model, credential)
	case Templates:
		d.Template, err = g.service.SaveTemplate(ctx, d.Template)
	case Members:
		d.Member, err = g.service.SaveMember(ctx, d.Member)
	}
	if err == nil {
		g.id = d.ID()
	}
	return d, outcome, err
}
func (g *ResourceEditor) Delete(ctx context.Context) (MutationOutcome, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.id == "" {
		return MutationOutcome{}, ErrWindowScope
	}
	var err error
	var outcome MutationOutcome
	switch g.kind {
	case Models:
		outcome, err = g.service.DeleteModel(ctx, g.id)
	case Templates:
		err = g.service.DeleteTemplate(ctx, g.id)
	case Members:
		err = g.service.DeleteMember(ctx, g.id)
	}
	if err == nil {
		g.id = ""
	}
	return outcome, err
}
func (g *ResourceEditor) Reset() (ResourceDraft, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.service.mu.Lock()
	defer g.service.mu.Unlock()
	if g.closed || g.service.closed {
		return ResourceDraft{}, ErrWindowScope
	}
	g.id = ""
	return NewResourceDraft(g.kind), nil
}
func (g *ResourceEditor) Duplicate(ctx context.Context, d ResourceDraft, suffix string) (ResourceDraft, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.kind != Templates || d.Kind != Templates || d.ID() != g.id || g.id == "" {
		return ResourceDraft{}, ErrWindowScope
	}
	value, err := g.service.duplicateTemplate(ctx, g.id, suffix)
	if err != nil {
		return ResourceDraft{}, err
	}
	d.Template = value
	g.id = value.ID
	return d, nil
}
func (g *ResourceEditor) Close() { g.mu.Lock(); defer g.mu.Unlock(); g.closed = true }
