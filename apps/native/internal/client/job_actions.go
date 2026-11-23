package client

import (
	"context"

	"liberty.local/liberty/internal/domain"
)

type JobRetry interface {
	Retry(context.Context, string) error
}
type JobDelete interface {
	Delete(context.Context, string) error
}
type SourceJobRetry interface {
	RetryForSource(context.Context, string, string) error
}
type SourceJobDelete interface {
	DeleteForSource(context.Context, string, string) error
}
type JobActions struct {
	Busy      bool
	Error     error
	retry     JobRetry
	deleter   JobDelete
	confirm   func(context.Context, string) (bool, error)
	dispatch  func(func())
	changed   func()
	completed func(string, string, bool)
	ctx       context.Context
	cancel    context.CancelFunc
	closed    bool
	Source    string
}

func NewJobActions(retry JobRetry, deleter JobDelete, confirm func(context.Context, string) (bool, error), dispatch func(func()), changed func(), completed func(string, string, bool)) *JobActions {
	ctx, cancel := context.WithCancel(context.Background())
	return &JobActions{retry: retry, deleter: deleter, confirm: confirm, dispatch: dispatch, changed: changed, completed: completed, ctx: ctx, cancel: cancel}
}
func (c *JobActions) Retry(id string) {
	source := c.Source
	if source == "" {
		source = "local"
	}
	c.RetryForSource(source, id)
}

func (c *JobActions) RetryForSource(source, id string) {
	c.run(source, id, false, func() (bool, error) {
		if source != "local" && source != "remote" {
			return false, domain.ErrRemoteUnsupported
		}
		if service, ok := c.retry.(SourceJobRetry); ok {
			return true, service.RetryForSource(c.ctx, source, id)
		}
		if source != "local" || c.retry == nil {
			return false, domain.ErrRemoteUnsupported
		}
		return true, c.retry.Retry(c.ctx, id)
	})
}
func (c *JobActions) Delete(id, title string) {
	source := c.Source
	if source == "" {
		source = "local"
	}
	c.run(source, id, true, func() (bool, error) {
		service, scoped := c.deleter.(SourceJobDelete)
		if source != "local" && source != "remote" || source == "remote" && !scoped || c.deleter == nil || c.confirm == nil {
			return false, domain.ErrRemoteUnsupported
		}
		ok, err := c.confirm(c.ctx, title)
		if err != nil || !ok {
			return false, err
		}
		if scoped {
			return true, service.DeleteForSource(c.ctx, source, id)
		}
		return true, c.deleter.Delete(c.ctx, id)
	})
}
func (c *JobActions) run(source, id string, deleted bool, action func() (bool, error)) {
	if c.closed || c.Busy {
		return
	}
	c.Busy = true
	c.Error = nil
	c.changed()
	go func() {
		applied, err := action()
		c.dispatch(func() {
			if c.closed {
				return
			}
			c.Busy = false
			c.Error = err
			if applied && err == nil {
				c.completed(source, id, deleted)
			}
			c.changed()
		})
	}()
}
func (c *JobActions) Close() { c.closed = true; c.cancel() }
