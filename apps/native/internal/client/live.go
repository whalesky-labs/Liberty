package client

import (
	"context"
	"errors"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

type Live interface {
	Options(context.Context) (application.LiveOptions, error)
	ModelStatus(context.Context) application.LiveModelStatus
	Prepare(context.Context, func(string)) error
	Start(context.Context, domain.LiveRequest) (domain.LiveSession, error)
	Control(context.Context, string, string) error
	Snapshot() domain.LiveSnapshot
	History(context.Context, int) ([]domain.LiveSession, error)
	Segments(context.Context, string, int64) ([]domain.LiveSegment, error)
	RetryTranslations(context.Context, string) error
	Delete(context.Context, string) error
	Edit(context.Context, domain.LiveSegment) error
}
type LiveFiles interface {
	Export(context.Context, string, string) (bool, error)
}

// Public state belongs to the UI dispatcher. Capture is owned by the service,
// so changing tabs or minimizing the main window never pauses a recording.
type LiveController struct {
	Options                               application.LiveOptions
	Request                               domain.LiveRequest
	Snapshot                              domain.LiveSnapshot
	History                               []domain.LiveSession
	Selected                              *domain.LiveSession
	Segments                              []domain.LiveSegment
	HistoryOffset                         int
	SegmentAfter                          int64
	Busy, Preparing                       bool
	ModelStatusBusy                       bool
	Error                                 error
	Exported                              bool
	service                               Live
	files                                 LiveFiles
	dispatch                              func(func())
	changed                               func()
	ctx                                   context.Context
	cancel                                context.CancelFunc
	prepareCancel                         context.CancelFunc
	closed, visible, polling, initialized bool
	modelStatusLoaded                     bool
	revision                              uint64
	lastPoll                              time.Time
	selectionError                        error
}

func NewLive(service Live, files LiveFiles, dispatch func(func()), changed func()) *LiveController {
	ctx, cancel := context.WithCancel(context.Background())
	return &LiveController{service: service, files: files, dispatch: dispatch, changed: changed, ctx: ctx, cancel: cancel, Request: domain.LiveRequest{SourceLanguage: "zh", TargetLanguage: "en", Translate: true, AutoDetect: true}}
}
func (c *LiveController) StartPolling() {
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
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
func (c *LiveController) SetVisible(visible bool) {
	c.visible = visible
	if visible {
		c.Poll()
		wasInitialized := c.initialized
		c.EnsureOptions()
		if wasInitialized && len(c.History) == 0 {
			c.RefreshHistory(0)
		}
	}
}

func (c *LiveController) EnsureOptions() {
	if c.closed || c.initialized || c.service == nil {
		return
	}
	c.initialized = true
	c.RefreshOptions()
}

func (c *LiveController) OptionsLoaded() bool {
	return c.initialized && !c.Busy
}

func (c *LiveController) EnsureModelStatus() {
	if c.closed || c.service == nil || c.ModelStatusBusy || c.modelStatusLoaded {
		return
	}
	c.ModelStatusBusy = true
	c.changed()
	go func() {
		ctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
		defer cancel()
		status := c.service.ModelStatus(ctx)
		c.dispatch(func() {
			if c.closed {
				return
			}
			c.Options.ModelReady = status.ModelReady
			c.Options.TranslationReady = status.TranslationReady
			c.modelStatusLoaded = true
			c.ModelStatusBusy = false
			c.changed()
		})
	}()
}

func (c *LiveController) Poll() {
	if c.closed || c.polling || (!c.visible && (!domain.LiveActive(c.Snapshot.Session.State) || time.Since(c.lastPoll) < time.Second)) {
		return
	}
	c.lastPoll = time.Now()
	c.polling = true
	revision := c.revision
	snapshotRevision := c.Snapshot.Revision
	selected, after := c.Selected, c.SegmentAfter
	retrySelection := c.selectionError != nil
	go func() {
		snapshot := c.service.Snapshot()
		refreshSelection := selected != nil && selected.ID == snapshot.Session.ID &&
			(snapshot.Revision > snapshotRevision || *selected != snapshot.Session || retrySelection)
		var segments []domain.LiveSegment
		var segmentsErr error
		if refreshSelection {
			segments, segmentsErr = c.service.Segments(c.ctx, selected.ID, after)
		}
		c.dispatch(func() {
			c.polling = false
			if c.closed || revision != c.revision {
				return
			}
			selectionChanged := refreshSelection && c.Selected == selected && c.SegmentAfter == after
			if selectionChanged {
				if c.selectionError != nil && errors.Is(c.Error, c.selectionError) {
					c.Error = nil
				}
				c.selectionError = segmentsErr
				if segmentsErr != nil {
					c.Error = segmentsErr
				} else {
					c.Selected = &snapshot.Session
					c.Segments = segments
				}
			}
			snapshotChanged := snapshot.Revision > c.Snapshot.Revision
			wasActive := domain.LiveActive(c.Snapshot.Session.State)
			if snapshotChanged {
				c.Snapshot = snapshot
			}
			if snapshotChanged || selectionChanged {
				c.changed()
			}
			if snapshotChanged && wasActive && !domain.LiveActive(snapshot.Session.State) {
				c.RefreshHistory(0)
			}
		})
	}()
}
func (c *LiveController) RefreshOptions() {
	if c.closed || c.Busy || c.Preparing {
		return
	}
	c.Busy = true
	c.Error = nil
	c.changed()
	readHistory := len(c.History) == 0
	historyOffset := c.HistoryOffset
	go func() {
		ctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
		defer cancel()
		options, err := c.service.Options(ctx)
		var history []domain.LiveSession
		var historyErr error
		if readHistory {
			history, historyErr = c.service.History(c.ctx, historyOffset)
		}
		c.dispatch(func() {
			if c.closed {
				return
			}
			c.Busy = false
			c.Options = options
			c.Error = err
			if readHistory && historyErr == nil {
				c.History = history
			} else if c.Error == nil {
				c.Error = historyErr
			}
			found := false
			for _, d := range options.Devices {
				found = found || d.ID == c.Request.DeviceID
			}
			if !found {
				c.Request.DeviceID = ""
			}

			c.changed()
		})
	}()
}
func (c *LiveController) Prepare() {
	if c.closed || c.Busy || c.Preparing {
		return
	}
	c.Preparing = true
	c.Error = nil
	c.changed()
	ctx, cancel := context.WithTimeout(c.ctx, 30*time.Minute)
	c.prepareCancel = cancel
	go func() {
		defer cancel()
		err := c.service.Prepare(ctx, nil)
		c.dispatch(func() {
			if c.closed {
				return
			}
			c.Preparing = false
			c.prepareCancel = nil
			c.Error = err
			c.changed()
			if err == nil {
				c.RefreshOptions()
			}
		})
	}()
}
func (c *LiveController) CancelPrepare() {
	if c.prepareCancel != nil {
		c.prepareCancel()
	}
}
func (c *LiveController) action(action func(context.Context) error, applied func()) {
	if c.closed || c.Busy || c.Preparing {
		return
	}
	c.Busy = true
	c.Error = nil
	c.Exported = false
	c.changed()
	go func() {
		err := action(c.ctx)
		c.dispatch(func() {
			if c.closed {
				return
			}
			c.Busy = false
			c.Error = err
			if err == nil && applied != nil {
				applied()
			}
			c.changed()
			c.Poll()
		})
	}()
}
func (c *LiveController) Begin() {
	request := c.Request
	var session domain.LiveSession
	c.action(func(ctx context.Context) error {
		var err error
		session, err = c.service.Start(ctx, request)
		return err
	}, func() {
		c.revision++
		c.Selected = nil
		c.selectionError = nil
		c.Segments = nil
		c.Snapshot = domain.LiveSnapshot{Session: session}
	})
}
func (c *LiveController) Control(command string) {
	id := c.Snapshot.Session.ID
	c.action(func(ctx context.Context) error { return c.service.Control(ctx, id, command) }, nil)
}
func (c *LiveController) RefreshHistory(offset int) {
	if c.closed || c.Busy || c.Preparing {
		return
	}
	c.action(func(ctx context.Context) error {
		values, err := c.service.History(ctx, offset)
		if err != nil {
			return err
		}
		c.dispatch(func() {
			if !c.closed {
				c.History = values
				c.HistoryOffset = offset
				c.changed()
			}
		})
		return nil
	}, nil)
}
func (c *LiveController) Select(session domain.LiveSession, after int64) {
	c.action(func(ctx context.Context) error {
		values, err := c.service.Segments(ctx, session.ID, after)
		if err != nil {
			return err
		}
		c.dispatch(func() {
			if !c.closed {
				c.Selected = &session
				c.selectionError = nil
				c.Segments = values
				c.SegmentAfter = after
				c.changed()
			}
		})
		return nil
	}, nil)
}
func (c *LiveController) ShowCurrent() {
	c.Selected = nil
	c.selectionError = nil
	c.Segments = nil
	c.changed()
}
func (c *LiveController) Retry() {
	if c.Selected == nil {
		return
	}
	selected := *c.Selected
	c.action(func(ctx context.Context) error { return c.service.RetryTranslations(ctx, selected.ID) }, func() { c.Select(selected, c.SegmentAfter) })
}
func (c *LiveController) DeleteSelected() {
	if c.Selected == nil {
		return
	}
	id := c.Selected.ID
	c.action(func(ctx context.Context) error { return c.service.Delete(ctx, id) }, func() {
		c.Selected = nil
		c.selectionError = nil
		c.Segments = nil
		if c.Snapshot.Session.ID == id {
			c.revision++
			c.Snapshot = domain.LiveSnapshot{}
		}
		c.RefreshHistory(c.HistoryOffset)
	})
}
func (c *LiveController) Edit(v domain.LiveSegment, saved func()) {
	if c.Selected == nil || c.Selected.ID != v.SessionID {
		return
	}
	selected := *c.Selected
	c.action(func(ctx context.Context) error { return c.service.Edit(ctx, v) }, func() {
		if saved != nil {
			saved()
		}
		c.Select(selected, c.SegmentAfter)
	})
}

func (c *LiveController) Export(format string) {
	if c.files == nil {
		return
	}
	id := c.Snapshot.Session.ID
	if c.Selected != nil {
		id = c.Selected.ID
	}
	var published bool
	c.action(func(ctx context.Context) error {
		var err error
		published, err = c.files.Export(ctx, id, format)
		return err
	}, func() { c.Exported = published })
}
func (c *LiveController) Close() {
	if c.closed {
		return
	}
	c.closed = true
	c.cancel()
}
