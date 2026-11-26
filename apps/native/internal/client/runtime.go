package client

import (
	"context"
	"reflect"
	"time"

	"liberty.local/liberty/internal/domain"
)

type Runtime interface {
	Panel(context.Context) (domain.RuntimePanel, error)
	Install(context.Context, string) error
	InstallAll(context.Context) error
	Detect(context.Context, string) error
	SwitchSource(context.Context, string, string) error
	SelectDownloadSource(context.Context, string) error
}

// RuntimeController keeps polling and command fencing out of the view. All
// exposed state and calls are owned by the supplied UI dispatcher.
type RuntimeController struct {
	Panel                    *domain.RuntimePanel
	Error                    error
	Busy                     bool
	service                  Runtime
	dispatch                 func(func())
	changed                  func()
	ctx                      context.Context
	cancel                   context.CancelFunc
	queryCancel              context.CancelFunc
	revision                 uint64
	loading, closed, polling bool
	ReadError, actionError   error
	visible                  bool
	lastPoll                 time.Time
}

func NewRuntime(service Runtime, dispatch func(func()), changed func()) *RuntimeController {
	ctx, cancel := context.WithCancel(context.Background())
	return &RuntimeController{service: service, dispatch: dispatch, changed: changed, ctx: ctx, cancel: cancel, visible: true}
}

func (c *RuntimeController) SetVisible(visible bool) {
	if c == nil || c.closed || c.visible == visible {
		return
	}
	c.visible = visible
	if visible && c.service != nil {
		c.poll()
	}
}
func (c *RuntimeController) Start() {
	if c.closed || c.polling {
		return
	}
	c.polling = true
	c.Refresh()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-ticker.C:
				c.dispatch(c.pollDue)
			}
		}
	}()
}
func (c *RuntimeController) Refresh() {
	if c.closed || c.Busy || c.loading {
		return
	}
	c.actionError = nil
	c.Error = c.ReadError
	c.query(nil, true)
}
func (c *RuntimeController) pollDue() {
	if c.closed || c.loading || c.Busy {
		return
	}
	interval := 30 * time.Second
	if c.visible {
		interval = 5 * time.Second
	}
	if c.Panel != nil {
		o := c.Panel.Overview
		if o.Python.Operation.Active() || o.FFmpeg.Operation.Active() || o.Models.Operation.Active() {
			interval = time.Second
		}
	}
	if time.Since(c.lastPoll) < interval {
		return
	}
	c.poll()
}
func (c *RuntimeController) poll() {
	if !c.closed && !c.loading && !c.Busy {
		c.query(nil, false)
	}
}
func (c *RuntimeController) query(action func(context.Context) error, clearActionError bool) {
	if c.queryCancel != nil {
		c.queryCancel()
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.queryCancel = cancel
	c.revision++
	revision := c.revision
	c.loading = true
	c.lastPoll = time.Now()
	c.Busy = action != nil
	if action != nil {
		c.actionError = nil
		c.Error = c.ReadError
	}
	foreground := action != nil || clearActionError || c.Panel == nil
	if foreground {
		c.changed()
	}
	go func() {
		var actionErr error
		if action != nil {
			actionErr = action(ctx)
		}
		panel, err := c.service.Panel(ctx)
		c.dispatch(func() {
			if c.closed || revision != c.revision {
				return
			}
			c.loading, c.Busy = false, false
			previousPanel, previousError := c.Panel, c.Error
			if err == nil {
				if !reflect.DeepEqual(c.Panel, &panel) {
					c.Panel = &panel
				}
				c.ReadError = nil
				if clearActionError {
					c.actionError = nil
				}
			} else {
				c.Panel = nil
				c.ReadError = err
			}
			if actionErr != nil {
				c.actionError = actionErr
			} else if action != nil {
				c.actionError = nil
			}
			c.Error = c.ReadError
			if c.Error == nil {
				c.Error = c.actionError
			}
			if foreground || previousPanel != c.Panel || !sameError(previousError, c.Error) {
				c.changed()
			}
		})
	}()
}
func (c *RuntimeController) act(action func(context.Context) error) {
	if c.closed || c.Busy {
		return
	}
	c.query(action, false)
}
func (c *RuntimeController) Install(component string) {
	c.act(func(ctx context.Context) error { return c.service.Install(ctx, component) })
}
func (c *RuntimeController) InstallAll() { c.act(c.service.InstallAll) }
func (c *RuntimeController) Detect(component string) {
	c.act(func(ctx context.Context) error { return c.service.Detect(ctx, component) })
}
func (c *RuntimeController) SwitchSource(component, source string) {
	c.act(func(ctx context.Context) error { return c.service.SwitchSource(ctx, component, source) })
}
func (c *RuntimeController) SelectDownloadSource(source string) {
	c.act(func(ctx context.Context) error { return c.service.SelectDownloadSource(ctx, source) })
}
func (c *RuntimeController) Close() { c.closed = true; c.cancel() }

func (c *RuntimeController) PanelAvailable() bool {
	return c.Panel != nil && c.ReadError == nil
}

func (c *RuntimeController) Refreshing() bool { return c.loading && !c.Busy }
