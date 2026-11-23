package client

import (
	"context"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

type SummaryWindow interface {
	SummaryState(context.Context) (application.SummaryWindowState, error)
	GenerateSummary(context.Context, application.SummaryForm) error
	SelectSummary(context.Context, string) error
	DeleteSummary(context.Context, string) error
}

// All public state belongs to the UI dispatcher. Workers only capture immutable
// arguments and post one result; closed/replaced windows ignore late callbacks.
type SummaryController struct {
	Runs         []domain.SummaryRun
	Options      application.SummaryOptions
	Result       *domain.Result
	SelectedID   string
	Busy         bool
	Loading      bool
	Error        error
	service      SummaryWindow
	dispatch     func(func())
	changed      func()
	ctx          context.Context
	cancel       context.CancelFunc
	closed       bool
	polling      bool
	querying     bool
	revision     uint64
	queryCancel  context.CancelFunc
	readTimeout  time.Duration
	actionError  error
	afterRead    func()
	retryAction  func(context.Context) error
	retryApplied func()
}

func NewSummaryController(service SummaryWindow, dispatch func(func()), changed func()) *SummaryController {
	ctx, cancel := context.WithCancel(context.Background())
	return &SummaryController{service: service, dispatch: dispatch, changed: changed, ctx: ctx, cancel: cancel, readTimeout: 10 * time.Second}
}
func (c *SummaryController) Refresh() { c.query(nil, nil) }
func (c *SummaryController) Retry() {
	if c.closed || c.Busy {
		return
	}
	if c.actionError != nil && c.retryAction != nil {
		c.query(c.retryAction, c.retryApplied)
		return
	}
	c.Refresh()
}
func (c *SummaryController) query(action func(context.Context) error, applied func()) {
	if c.closed || c.Busy || c.service == nil || (action == nil && c.querying) {
		return
	}
	if c.queryCancel != nil {
		c.queryCancel()
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.queryCancel = cancel
	c.revision++
	revision := c.revision
	initialLoad := c.Result == nil
	c.querying = true
	if action != nil {
		c.Busy = true
	} else {
		c.Loading = c.Result == nil
		if initialLoad {
			c.Busy = true
		}
	}
	if action != nil {
		c.Error = nil
		c.actionError = nil
		c.afterRead = nil
		c.retryAction, c.retryApplied = nil, nil
	}
	c.changed()
	go func() {
		defer cancel()
		var actionErr error
		if action != nil {
			actionErr = ctx.Err()
			if actionErr == nil {
				actionErr = action(ctx)
			}
		}
		var state application.SummaryWindowState
		err := ctx.Err()
		if err == nil {
			readCtx, stopRead := context.WithTimeout(ctx, c.readTimeout)
			state, err = c.service.SummaryState(readCtx)
			stopRead()
		}
		c.dispatch(func() {
			if c.closed || revision != c.revision {
				return
			}
			c.Loading, c.querying = false, false
			if action != nil || err != nil || initialLoad {
				c.Busy = false
			}
			if action != nil {
				c.actionError = actionErr
				if actionErr == nil {
					c.afterRead = applied
				} else {
					c.retryAction, c.retryApplied = action, applied
				}
			}
			c.Error = err
			if c.actionError != nil {
				c.Error = c.actionError
			}
			if err == nil {
				c.Runs, c.Options, c.Result = state.Runs, state.Options, &state.Result
				if c.afterRead != nil {
					c.afterRead()
					c.afterRead = nil
				}
				found := false
				for _, run := range c.Runs {
					found = found || run.ID == c.SelectedID
				}
				if !found {
					c.SelectedID = ""
					if len(c.Runs) > 0 {
						c.SelectedID = c.Runs[0].ID
					}
					if active := c.Result.Job.ActiveSummaryRunID; active != nil {
						for _, run := range c.Runs {
							if run.ID == *active {
								c.SelectedID = run.ID
							}
						}
					}
				}
			}
			c.changed()
		})
	}()
}
func (c *SummaryController) Generate(form application.SummaryForm) {
	selected := c.SelectedID
	c.query(func(ctx context.Context) error { return c.service.GenerateSummary(ctx, form) }, func() {
		if c.SelectedID != selected {
			return
		}
		if form.RunID != "" {
			c.SelectedID = form.RunID
		} else if len(c.Runs) > 0 {
			c.SelectedID = c.Runs[0].ID
		}
	})
}
func (c *SummaryController) Select(id string) {
	c.query(func(ctx context.Context) error { return c.service.SelectSummary(ctx, id) }, nil)
}
func (c *SummaryController) Delete(id string, confirm func(context.Context) (bool, error)) {
	c.query(func(ctx context.Context) error {
		yes, err := confirm(ctx)
		if err != nil || !yes {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return c.service.DeleteSummary(ctx, id)
	}, nil)
}
func (c *SummaryController) Running() bool {
	for _, run := range c.Runs {
		if run.Status == "running" {
			return true
		}
	}
	return false
}
func (c *SummaryController) Selected() *domain.SummaryRun {
	for i := range c.Runs {
		if c.Runs[i].ID == c.SelectedID {
			return &c.Runs[i]
		}
	}
	return nil
}
func (c *SummaryController) StartPolling() {
	if c.polling || c.closed {
		return
	}
	c.polling = true
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-ticker.C:
				c.dispatch(func() {
					if !c.closed {
						c.Refresh()
					}
				})
			}
		}
	}()
}
func (c *SummaryController) Close() {
	c.closed = true
	c.afterRead, c.retryAction, c.retryApplied = nil, nil, nil
	c.cancel()
}
