package client

import (
	"context"
	"path/filepath"
	"strings"

	"liberty.local/liberty/internal/application"
)

// BatchItem records admission to the durable queue, not ASR completion. A
// successful item is never submitted again when another item needs a retry.
type BatchItem struct {
	Media application.SelectedMedia
	Title string
	JobID string
	Error error
}

type batchMediaPicker interface {
	PickMany(context.Context) ([]application.SelectedMedia, error)
}

func mediaTitle(media application.SelectedMedia) string {
	return strings.TrimSpace(strings.TrimSuffix(media.Name, filepath.Ext(media.Name)))
}

func (c *SubmissionController) SelectionCount() int {
	if len(c.Batch) > 0 {
		return len(c.Batch)
	}
	if c.Media != nil {
		return 1
	}
	return 0
}

func (c *SubmissionController) PendingCount() int {
	if len(c.Batch) == 0 {
		return c.SelectionCount()
	}
	count := 0
	for _, item := range c.Batch {
		if item.JobID == "" {
			count++
		}
	}
	return count
}

func (c *SubmissionController) CanSubmit() bool {
	if c.closed || c.Unavailable || c.Busy || c.Picking || c.PendingCount() == 0 {
		return false
	}
	if len(c.Batch) == 0 {
		return c.Media != nil && strings.TrimSpace(c.Form.Title) != ""
	}
	for _, item := range c.Batch {
		if item.JobID == "" && strings.TrimSpace(item.Title) == "" {
			return false
		}
	}
	return true
}

func (c *SubmissionController) RemoveBatchItem(index int) {
	if c.closed || c.Busy || c.Picking || index < 0 || index >= len(c.Batch) {
		return
	}
	c.picker.Release(c.Batch[index].Media.Token)
	c.Batch = append(c.Batch[:index], c.Batch[index+1:]...)
	if len(c.Batch) == 0 {
		c.Media, c.Form.Selection = nil, ""
	} else {
		c.Media = &c.Batch[0].Media
	}
	c.changed()
}

func (c *SubmissionController) submitBatch(form application.CreateMeeting) {
	items := append([]BatchItem(nil), c.Batch...)
	go func() {
		for index, item := range items {
			if item.JobID != "" || c.ctx.Err() != nil {
				continue
			}
			next := form
			next.Selection, next.Title = item.Media.Token, strings.TrimSpace(item.Title)
			id, err := c.service.Create(c.ctx, next)
			c.dispatch(func() {
				if c.closed {
					return
				}
				c.Batch[index].JobID, c.Batch[index].Error = id, err
				if err == nil {
					c.picker.Release(item.Media.Token)
				}
				c.changed()
			})
		}
		c.dispatch(func() {
			if !c.closed {
				c.Busy = false
				c.changed()
			}
		})
	}()
}
