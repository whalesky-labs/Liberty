package client

import (
	"context"
	"strconv"
	"strings"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

type ResourceLists interface {
	Models(context.Context) ([]domain.AIModel, error)
	Templates(context.Context) ([]domain.SummaryTemplate, error)
	Members(context.Context) ([]domain.MeetingMember, error)
	DeleteModel(context.Context, string) (application.MutationOutcome, error)
	DeleteTemplate(context.Context, string) error
	DeleteMember(context.Context, string) error
}
type ResourceController struct {
	ImportResult   *domain.MemberImportResult
	Exported       bool
	Models         []domain.AIModel
	Templates      []domain.SummaryTemplate
	Members        []domain.MeetingMember
	Error          error
	Busy           bool
	CleanupPending bool
	service        ResourceLists
	dispatch       func(func())
	changed        func()
	ctx            context.Context
	cancel         context.CancelFunc
	revision       uint64
	closed         bool
	pending        application.ResourceKind
}

func NewResources(service ResourceLists, dispatch func(func()), changed func()) *ResourceController {
	ctx, cancel := context.WithCancel(context.Background())
	return &ResourceController{service: service, dispatch: dispatch, changed: changed, ctx: ctx, cancel: cancel}
}
func (c *ResourceController) Refresh(kind application.ResourceKind) {
	if c.Busy {
		c.pending = kind
		return
	}
	c.query(kind, nil, nil)
}
func (c *ResourceController) query(kind application.ResourceKind, action func(context.Context) (application.MutationOutcome, error), priorActionErr error) {
	if c.closed || c.Busy {
		return
	}
	c.revision++
	revision := c.revision
	c.Busy = true
	c.Error = priorActionErr
	c.changed()
	go func() {
		var outcome application.MutationOutcome
		var actionErr, errorRead error
		if action != nil {
			outcome, actionErr = action(c.ctx)
		}
		var models []domain.AIModel
		var templates []domain.SummaryTemplate
		var members []domain.MeetingMember
		switch kind {
		case application.Models:
			models, errorRead = c.service.Models(c.ctx)
		case application.Templates:
			templates, errorRead = c.service.Templates(c.ctx)
		case application.Members:
			members, errorRead = c.service.Members(c.ctx)
		}
		c.dispatch(func() {
			if c.closed || revision != c.revision {
				return
			}
			c.Busy = false
			c.Error = errorRead
			c.CleanupPending = outcome.CleanupPending
			visibleActionErr := priorActionErr
			if actionErr != nil {
				visibleActionErr = actionErr
			}
			if visibleActionErr != nil {
				c.Error = visibleActionErr
			}
			if errorRead == nil {
				switch kind {
				case application.Models:
					c.Models = models
				case application.Templates:
					c.Templates = templates
				case application.Members:
					c.Members = members
				}
			}
			c.finishResourceUpdate(visibleActionErr)
		})
	}()
}

func (c *ResourceController) finishResourceUpdate(actionErr error) {
	if c.pending == "" {
		c.changed()
		return
	}
	next := c.pending
	c.pending = ""
	// A queued read updates the list while keeping the action failure visible.
	c.query(next, nil, actionErr)
}

func (c *ResourceController) Delete(kind application.ResourceKind, id string, confirm func(context.Context) (bool, error)) {
	c.query(kind, func(ctx context.Context) (application.MutationOutcome, error) {
		yes, err := confirm(ctx)
		if err != nil || !yes {
			return application.MutationOutcome{}, err
		}
		switch kind {
		case application.Models:
			return c.service.DeleteModel(ctx, id)
		case application.Templates:
			return application.MutationOutcome{}, c.service.DeleteTemplate(ctx, id)
		case application.Members:
			return application.MutationOutcome{}, c.service.DeleteMember(ctx, id)
		}
		return application.MutationOutcome{}, application.ErrWindowScope
	}, nil)
}
func (c *ResourceController) Close() { c.closed = true; c.cancel() }

type EditorPort interface {
	Save(context.Context, application.ResourceDraft, domain.CredentialUpdate) (application.ResourceDraft, application.MutationOutcome, error)
	Delete(context.Context) (application.MutationOutcome, error)
	Reset() (application.ResourceDraft, error)
	Duplicate(context.Context, application.ResourceDraft, string) (application.ResourceDraft, error)
	Close()
}
type ResourceEditor struct {
	Revision        uint64
	Draft           application.ResourceDraft
	MemberSortOrder string
	InvalidField    string
	Password        string
	ClearCredential bool
	Busy            bool
	Error           error
	CleanupPending  bool
	original        application.ResourceDraft
	port            EditorPort
	dispatch        func(func())
	changed, saved  func()
	ctx             context.Context
	cancel          context.CancelFunc
	closed          bool
}

func NewResourceEditor(port EditorPort, draft application.ResourceDraft, dispatch func(func()), changed, saved func()) *ResourceEditor {
	ctx, cancel := context.WithCancel(context.Background())
	return &ResourceEditor{port: port, Draft: draft, original: draft, MemberSortOrder: strconv.FormatInt(draft.Member.SortOrder, 10), dispatch: dispatch, changed: changed, saved: saved, ctx: ctx, cancel: cancel}
}
func (c *ResourceEditor) Dirty() bool {
	return c.Draft != c.original || c.Password != "" || c.ClearCredential ||
		c.Draft.Kind == application.Members && c.MemberSortOrder != strconv.FormatInt(c.original.Member.SortOrder, 10)
}
func (c *ResourceEditor) validate() string {
	if strings.TrimSpace(c.Draft.Name()) == "" {
		return "name"
	}
	switch c.Draft.Kind {
	case application.Models:
		if strings.TrimSpace(c.Draft.Model.BaseURL) == "" {
			return "baseUrl"
		}
		if c.Draft.ID() == "" && strings.TrimSpace(c.Password) == "" {
			return "apiKey"
		}
		if strings.TrimSpace(c.Draft.Model.Model) == "" {
			return "model"
		}
	case application.Templates:
		if strings.TrimSpace(c.Draft.Template.Prompt) == "" {
			return "prompt"
		}
	case application.Members:
		if _, err := strconv.ParseInt(strings.TrimSpace(c.MemberSortOrder), 10, 64); err != nil {
			return "sortOrder"
		}
	}
	return ""
}
func (c *ResourceEditor) Save() {
	if c.closed || c.Busy {
		return
	}
	c.InvalidField = c.validate()
	if c.InvalidField != "" {
		c.Error = domain.ErrResourceInvalid
		c.changed()
		return
	}
	if c.Draft.Kind == application.Members {
		c.Draft.Member.SortOrder, _ = strconv.ParseInt(strings.TrimSpace(c.MemberSortOrder), 10, 64)
	}
	update := domain.CredentialUpdate{Action: domain.KeepCredential}
	if strings.TrimSpace(c.Password) != "" {
		update.Action = domain.SetCredential
		update.Value = domain.NewSecret(c.Password)
	} else if c.ClearCredential {
		update.Action = domain.ClearCredential
	}
	draft := c.Draft
	c.Busy = true
	c.Error = nil
	c.changed()
	go func() {
		next, outcome, err := c.port.Save(c.ctx, draft, update)
		c.dispatch(func() {
			if c.closed {
				return
			}
			c.Busy = false
			c.Error = err
			c.CleanupPending = outcome.CleanupPending
			if err == nil {
				c.Revision++
				c.Draft = next
				c.original = next
				c.MemberSortOrder = strconv.FormatInt(next.Member.SortOrder, 10)
				c.Password = ""
				c.ClearCredential = false
				c.saved()
			}
			c.changed()
		})
	}()
}
func (c *ResourceEditor) Reset(confirm func(context.Context) (bool, error)) {
	c.changeDraft(confirm, false, func() (application.ResourceDraft, error) { return c.port.Reset() })
}
func (c *ResourceEditor) Duplicate(suffix string) {
	draft := c.Draft
	c.changeDraft(nil, true, func() (application.ResourceDraft, error) { return c.port.Duplicate(c.ctx, draft, suffix) })
}
func (c *ResourceEditor) changeDraft(confirm func(context.Context) (bool, error), notifySaved bool, action func() (application.ResourceDraft, error)) {
	if c.closed || c.Busy {
		return
	}
	needsConfirm := c.Dirty() && confirm != nil
	c.Busy = true
	c.Error = nil
	c.InvalidField = ""
	c.changed()
	go func() {
		yes := true
		var err error
		if needsConfirm {
			yes, err = confirm(c.ctx)
		}
		var draft application.ResourceDraft
		if yes && err == nil {
			draft, err = action()
		}
		c.dispatch(func() {
			if c.closed {
				return
			}
			c.Busy = false
			c.Error = err
			if yes && err == nil {
				c.Revision++
				c.Draft = draft
				c.original = draft
				c.MemberSortOrder = strconv.FormatInt(draft.Member.SortOrder, 10)
				c.Password = ""
				c.ClearCredential = false
				if notifySaved {
					c.saved()
				}
			}
			c.changed()
		})
	}()
}
func (c *ResourceEditor) Delete(confirm func(context.Context) (bool, error)) {
	if c.closed || c.Busy || c.Draft.ID() == "" {
		return
	}
	c.Busy = true
	c.Error = nil
	c.InvalidField = ""
	c.changed()
	go func() {
		yes, err := confirm(c.ctx)
		var outcome application.MutationOutcome
		var draft application.ResourceDraft
		if yes && err == nil {
			outcome, err = c.port.Delete(c.ctx)
			if err == nil {
				draft, err = c.port.Reset()
			}
		}
		c.dispatch(func() {
			if c.closed {
				return
			}
			c.Busy = false
			c.Error = err
			c.CleanupPending = outcome.CleanupPending
			if yes && err == nil {
				c.Revision++
				c.Draft = draft
				c.original = draft
				c.MemberSortOrder = strconv.FormatInt(draft.Member.SortOrder, 10)
				c.Password = ""
				c.ClearCredential = false
				c.saved()
			}
			c.changed()
		})
	}()
}
func (c *ResourceEditor) Close() {
	c.closed = true
	c.cancel()
	c.Password = ""
	// Revocation joins any in-flight publish off the UI thread.
	go c.port.Close()
}
