// Package client coordinates asynchronous queries and rejects stale replies.
// The supplied dispatcher owns view state; workers never touch UI widgets.
package client

import (
	"context"
	"errors"
	"time"

	"liberty.local/liberty/internal/domain"
)

var ErrResultSourceMismatch = errors.New("result_source_mismatch")
var ErrQueueSourceMismatch = errors.New("queue_source_mismatch")

type Meetings interface {
	Queue(context.Context, domain.QueueQuery) (domain.JobPage, error)
	ResultReader
}

type ResultReader interface {
	Result(context.Context, string) (domain.Result, error)
}

type SourceResultReader interface {
	ResultForSource(context.Context, string, string) (domain.Result, error)
}

type SourceJobReader interface {
	JobForSource(context.Context, string, string) (domain.Result, error)
}

// The main window reads job details, while independent workbenches receive a
// result grant. Keeping these ports separate preserves remote capability scope.
type mainDetailReader struct{ Meetings }

func (r mainDetailReader) ResultForSource(ctx context.Context, source, id string) (domain.Result, error) {
	if service, ok := r.Meetings.(SourceJobReader); ok {
		return service.JobForSource(ctx, source, id)
	}
	if service, ok := r.Meetings.(SourceResultReader); ok {
		return service.ResultForSource(ctx, source, id)
	}
	return r.Meetings.Result(ctx, id)
}

type State struct {
	Source      string
	Jobs        []domain.JobSummary
	Page        domain.JobPage
	LoadingJobs bool
	ListError   error
}

// Controller methods and callbacks must run on the dispatcher's owner thread.
type Controller struct {
	State
	*ResultController
	service      Meetings
	dispatch     func(func())
	changed      func()
	ctx          context.Context
	cancel       context.CancelFunc
	listCancel   context.CancelFunc
	listRevision uint64
	closed       bool
	query        domain.QueueQuery
	polling      bool
}

func New(service Meetings, dispatch func(func()), changed func()) *Controller {
	ctx, cancel := context.WithCancel(context.Background())
	return &Controller{ResultController: NewResult(mainDetailReader{service}, dispatch, changed), service: service, dispatch: dispatch, changed: changed, ctx: ctx, cancel: cancel}
}

func (c *Controller) Refresh(query domain.QueueQuery) {
	c.refresh(query, false)
}
func (c *Controller) StartPolling() {
	if c.closed || c.polling {
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
				c.dispatch(c.Poll)
			}
		}
	}()
}
func (c *Controller) Poll() {
	if !c.closed && !c.LoadingJobs {
		c.refresh(c.query, true)
	}
}
func (c *Controller) refresh(query domain.QueueQuery, background bool) {
	if c.closed {
		return
	}
	previousSource := c.query.Source
	if previousSource == "" {
		previousSource = c.Source
	}
	sourceChanged := previousSource != "" && query.Source != "" && previousSource != query.Source
	c.query = query
	c.ResultController.source = query.Source
	if query.Source != "" {
		c.Source = query.Source
	}
	if sourceChanged {
		// A source switch revokes both the old page and its selected detail
		// before the new source has returned. IDs are not globally unique.
		c.Page, c.Jobs = domain.JobPage{Source: query.Source}, nil
		c.ResultController.Select("")
	}
	if c.listCancel != nil {
		c.listCancel()
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.listCancel = cancel
	c.listRevision++
	revision := c.listRevision
	c.LoadingJobs, c.ListError = true, nil
	c.changed()
	go func() {
		page, err := c.service.Queue(ctx, query)
		if err == nil {
			if page.Source != "" && query.Source != "" && page.Source != query.Source {
				err = ErrQueueSourceMismatch
			} else if page.Source == "" {
				page.Source = query.Source
			}
		}
		c.dispatch(func() {
			if c.closed || revision != c.listRevision {
				return
			}
			if err == nil && query.Source == "" && page.Source != "" {
				// Some compatibility services choose the source when the query is
				// unscoped. Bind later detail reads to that verified snapshot.
				c.query.Source = page.Source
				c.Source = page.Source
				c.ResultController.source = page.Source
			}
			c.LoadingJobs, c.ListError = false, err
			if err == nil {
				c.Page, c.Jobs = page, page.Items
				if c.SelectedID != "" {
					visible := false
					for _, job := range page.Items {
						visible = visible || job.ID == c.SelectedID
					}
					if background {
						c.ResultController.Refresh()
					} else if visible {
						c.Select(c.SelectedID)
					} else {
						c.Select("")
					}
				}
			}
			c.changed()
		})
	}()
}

type ResultController struct {
	Result         *domain.Result
	SelectedID     string
	LoadingResult  bool
	ResultError    error
	service        ResultReader
	dispatch       func(func())
	changed        func()
	ctx            context.Context
	cancel         context.CancelFunc
	resultCancel   context.CancelFunc
	resultRevision uint64
	closed         bool
	querying       bool
	polling        bool
	source         string
}

// NewResult accepts only the bound result port. A child window never receives
// the main window's queue, settings, credential or filesystem capabilities.
func NewResult(service ResultReader, dispatch func(func()), changed func()) *ResultController {
	ctx, cancel := context.WithCancel(context.Background())
	return &ResultController{service: service, dispatch: dispatch, changed: changed, ctx: ctx, cancel: cancel}
}

func (c *ResultController) Select(id string) {
	c.selectResult(id, false)
}
func (c *ResultController) Refresh() {
	if !c.querying && c.SelectedID != "" {
		c.selectResult(c.SelectedID, true)
	}
}
func (c *ResultController) StartPolling() {
	if c.closed || c.polling {
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
				c.dispatch(c.Refresh)
			}
		}
	}()
}
func (c *ResultController) selectResult(id string, background bool) {
	if c.closed {
		return
	}
	if c.resultCancel != nil {
		c.resultCancel()
	}
	c.resultRevision++
	revision := c.resultRevision
	c.SelectedID, c.ResultError = id, nil
	if !background {
		c.Result = nil
	}
	c.LoadingResult = id != "" && !background
	c.querying = id != ""
	c.changed()
	if id == "" {
		return
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.resultCancel = cancel
	source := c.source
	go func() {
		var result domain.Result
		var err error
		if source != "" {
			if reader, ok := c.service.(SourceResultReader); ok {
				result, err = reader.ResultForSource(ctx, source, id)
			} else {
				result, err = c.service.Result(ctx, id)
			}
		} else {
			result, err = c.service.Result(ctx, id)
		}
		c.dispatch(func() {
			if c.closed || revision != c.resultRevision {
				return
			}
			c.LoadingResult, c.ResultError = false, err
			c.querying = false
			if err == nil && (source == "" || result.Job.Source == source) && result.Job.ID == id {
				c.Result = &result
			} else if err == nil {
				c.ResultError = ErrResultSourceMismatch
			}
			c.changed()
		})
	}()
}

func (c *Controller) Close() {
	c.closed = true
	c.cancel()
	c.ResultController.Close()
}

func (c *ResultController) Close() {
	c.closed = true
	c.cancel()
}
