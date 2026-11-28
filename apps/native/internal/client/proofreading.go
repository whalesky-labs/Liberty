package client

import (
	"context"
	"liberty.local/liberty/internal/domain"
)

type transcriptActions interface {
	EditSegment(context.Context, domain.SegmentEdit) error
}

func (c *ResultActionController) BeginCorrection(segment domain.Segment) {
	if c.closed || c.Busy || c.Correction != nil {
		return
	}
	copy := segment
	c.Correction = &copy
	c.ResetCorrection()
}
func (c *ResultActionController) ResetCorrection() {
	if c.closed || c.Busy || c.Correction == nil {
		return
	}
	c.CorrectionText, c.CorrectionSpeaker = c.Correction.Text, ""
	if c.Correction.Speaker != nil {
		c.CorrectionSpeaker = *c.Correction.Speaker
	}
	if c.changed != nil {
		c.changed()
	}
}
func (c *ResultActionController) CancelCorrection() {
	if !c.closed && !c.Busy {
		c.Correction = nil
		if c.changed != nil {
			c.changed()
		}
	}
}

func (c *ResultActionController) EditSegment(edit domain.SegmentEdit, done func()) {
	writer, ok := c.service.(transcriptActions)
	if c.closed || c.Busy || !ok || edit.Validate() != nil {
		return
	}
	c.Busy, c.Error, c.Exported = true, nil, false
	c.changed()
	go func() {
		err := writer.EditSegment(c.ctx, edit)
		c.dispatch(func() {
			if c.closed {
				return
			}
			c.Busy, c.Error = false, err
			if err == nil && done != nil {
				done()
			}
			c.changed()
		})
	}()
}
