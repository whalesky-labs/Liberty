package client

import (
	"context"
	"liberty.local/liberty/internal/domain"
)

type RemoteCapabilitiesService interface {
	Capabilities(context.Context) (domain.RemoteCapabilities, error)
}
type RemoteController struct {
	Capabilities  *domain.RemoteCapabilities
	Error         error
	Checking      bool
	service       RemoteCapabilitiesService
	dispatch      func(func())
	changed       func()
	ctx           context.Context
	cancel        context.CancelFunc
	closed        bool
	requestCancel context.CancelFunc
	revision      uint64
}

func NewRemote(service RemoteCapabilitiesService, dispatch func(func()), changed func()) *RemoteController {
	ctx, cancel := context.WithCancel(context.Background())
	return &RemoteController{service: service, dispatch: dispatch, changed: changed, ctx: ctx, cancel: cancel}
}
func (c *RemoteController) Check() { c.check(false) }

// Refresh retains the last verified snapshot while a background check runs.
// Any failed response clears it so unavailable operations fail closed.
func (c *RemoteController) Refresh() { c.check(true) }

func (c *RemoteController) check(background bool) {
	if c.closed || c.Checking {
		return
	}
	c.Checking = true
	c.Error = nil
	if !background {
		c.Capabilities = nil
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.requestCancel = cancel
	c.revision++
	revision := c.revision
	c.changed()
	go func() {
		result, err := c.service.Capabilities(ctx)
		cancel()
		c.dispatch(func() {
			if c.closed || revision != c.revision {
				return
			}
			c.Checking = false
			c.Error = err
			c.Capabilities = nil
			if err == nil {
				c.Capabilities = &result
			}
			c.changed()
		})
	}()
}
func (c *RemoteController) Reset() {
	if !c.closed {
		c.revision++
		if c.requestCancel != nil {
			c.requestCancel()
		}
		c.Checking = false
		c.Capabilities = nil
		c.Error = nil
		c.changed()
	}
}
func (c *RemoteController) Close() { c.closed = true; c.cancel(); c.Capabilities = nil }
