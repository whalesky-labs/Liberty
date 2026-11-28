package client

import (
	"context"
	"errors"
	"strings"

	"liberty.local/liberty/internal/application"
)

type Submission interface {
	Defaults(context.Context) (application.JobDefaults, error)
	Create(context.Context, application.CreateMeeting) (string, error)
}
type MediaPicker interface {
	Pick(context.Context) (application.SelectedMedia, error)
	Release(string)
}
type SubmissionController struct {
	Form              application.CreateMeeting
	Media             *application.SelectedMedia
	Batch             []BatchItem
	Busy, Picking     bool
	Unavailable       bool
	Error             error
	service           Submission
	picker            MediaPicker
	dispatch          func(func())
	changed           func()
	created           func(string)
	ctx               context.Context
	cancel            context.CancelFunc
	closed            bool
	selectionRevision uint64
	defaultsRevision  uint64
	defaults          application.JobDefaults
	hotwordsEdited    bool
}

func NewSubmission(service Submission, picker MediaPicker, dispatch func(func()), changed func(), created func(string)) *SubmissionController {
	ctx, cancel := context.WithCancel(context.Background())
	c := &SubmissionController{service: service, picker: picker, dispatch: dispatch, changed: changed, created: created, ctx: ctx, cancel: cancel, Form: application.CreateMeeting{Language: "zh-CN", Speaker: true}}
	c.RefreshDefaults()
	return c
}
func (c *SubmissionController) RefreshDefaults() {
	if c.closed {
		return
	}
	c.defaultsRevision++
	revision := c.defaultsRevision
	go func() {
		defaults, err := c.service.Defaults(c.ctx)
		c.dispatch(func() {
			if c.closed || revision != c.defaultsRevision {
				return
			}
			var defaultsError *JobDefaultsError
			if err != nil {
				if c.Error == nil || errors.As(c.Error, &defaultsError) {
					c.Error = &JobDefaultsError{Cause: err}
				}
			} else {
				if errors.As(c.Error, &defaultsError) {
					c.Error = nil
				}
				if !c.hotwordsEdited && c.Form.Hotwords == c.defaults.Hotwords {
					c.Form.Hotwords = defaults.Hotwords
				}
				if c.Form.SummaryTemplate == "" || c.Form.SummaryTemplate == c.defaults.SummaryTemplate {
					c.Form.SummaryTemplate = defaults.SummaryTemplate
				}
				c.defaults = defaults
			}
			c.changed()
		})
	}()
}
func (c *SubmissionController) Pick() {
	if c.closed || c.Picking || c.Busy {
		return
	}
	c.Picking = true
	c.selectionRevision++
	revision := c.selectionRevision
	c.changed()
	go func() {
		var selections []application.SelectedMedia
		var err error
		if picker, ok := c.picker.(batchMediaPicker); ok {
			selections, err = picker.PickMany(c.ctx)
		} else {
			var selected application.SelectedMedia
			selected, err = c.picker.Pick(c.ctx)
			if selected.Token != "" {
				selections = []application.SelectedMedia{selected}
			}
		}
		c.dispatch(func() {
			if c.closed || revision != c.selectionRevision {
				for _, selected := range selections {
					c.picker.Release(selected.Token)
				}
				return
			}
			c.Picking = false
			if err != nil {
				c.Error = &MediaPickError{Cause: err}
			} else if len(selections) > 0 {
				c.Error = nil
				c.clear()
				selected := selections[0]
				c.Media = &selected
				c.Form.Selection = selected.Token
				if strings.TrimSpace(c.Form.Title) == "" {
					c.Form.Title = mediaTitle(selected)
				}
				if len(selections) > 1 {
					for _, media := range selections {
						c.Batch = append(c.Batch, BatchItem{Media: media, Title: mediaTitle(media)})
					}
				}
			}
			c.changed()
		})
	}()
}
func (c *SubmissionController) clear() {
	if len(c.Batch) > 0 {
		for _, item := range c.Batch {
			c.picker.Release(item.Media.Token)
		}
	} else if c.Media != nil {
		c.picker.Release(c.Media.Token)
	}
	c.Batch = nil
	c.Media = nil
	c.Form.Selection = ""
}
func (c *SubmissionController) Clear() {
	if c.closed || c.Busy {
		return
	}
	c.selectionRevision++
	c.Picking = false
	c.clear()
	c.changed()
}
func (c *SubmissionController) Submit() {
	if !c.CanSubmit() {
		return
	}
	c.Busy = true
	c.Error = nil
	form := c.Form
	c.changed()
	if len(c.Batch) > 0 {
		c.submitBatch(form)
		return
	}
	go func() {
		id, err := c.service.Create(c.ctx, form)
		c.dispatch(func() {
			if c.closed {
				return
			}
			c.Busy = false
			c.Error = err
			if err == nil {
				c.clear()
				c.Form.Title = ""
				c.created(id)
			}
			c.changed()
		})
	}()
}
func (c *SubmissionController) Close() { c.closed = true; c.cancel(); c.clear() }
