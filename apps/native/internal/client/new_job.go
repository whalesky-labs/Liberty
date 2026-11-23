package client

import (
	"strings"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

type JobDefaultsError struct{ Cause error }

func (err *JobDefaultsError) Error() string { return "job_defaults_unavailable" }
func (err *JobDefaultsError) Unwrap() error { return err.Cause }

type MediaPickError struct{ Cause error }

func (err *MediaPickError) Error() string { return "media_pick_failed" }
func (err *MediaPickError) Unwrap() error { return err.Cause }

func (c *SubmissionController) SetHotwords(value string) {
	if c.closed || c.Busy {
		return
	}
	c.Form.Hotwords, c.hotwordsEdited = value, true
}

func (c *SubmissionController) SubmitWithDefaults(fallbackTemplate string) {
	if c.closed || c.Unavailable || c.Busy || c.Picking || c.Media == nil || strings.TrimSpace(c.Form.Title) == "" {
		return
	}
	form, previous := c.Form, c.defaults
	form.Title = strings.TrimSpace(form.Title)
	c.Busy, c.Error = true, nil
	c.defaultsRevision++
	revision := c.defaultsRevision
	c.changed()
	go func() {
		defaults, err := c.service.Defaults(c.ctx)
		c.dispatch(func() {
			if c.closed {
				return
			}
			if c.Unavailable {
				c.Busy, c.Error = false, domain.ErrRemoteUnsupported
				c.changed()
				return
			}
			if revision != c.defaultsRevision {
				c.Busy = false
				c.SubmitWithDefaults(fallbackTemplate)
				return
			}
			if err != nil {
				c.Busy, c.Error = false, &JobDefaultsError{Cause: err}
				c.changed()
				return
			}
			if !c.hotwordsEdited && form.Hotwords == previous.Hotwords {
				form.Hotwords = defaults.Hotwords
			}
			if strings.TrimSpace(defaults.SummaryTemplate) == "" {
				defaults.SummaryTemplate = fallbackTemplate
			}
			form.SummaryTemplate = defaults.SummaryTemplate
			c.Form, c.defaults = form, defaults
			c.changed()
			go func() {
				id, err := c.service.Create(c.ctx, form)
				c.dispatch(func() {
					if c.closed {
						return
					}
					c.Busy, c.Error = false, err
					if err == nil {
						c.clear()
						c.hotwordsEdited = false
						c.Form = application.CreateMeeting{Language: "zh-CN", Speaker: true, Hotwords: c.defaults.Hotwords, SummaryTemplate: c.defaults.SummaryTemplate}
						c.created(id)
					}
					c.changed()
				})
			}()
		})
	}()
}
