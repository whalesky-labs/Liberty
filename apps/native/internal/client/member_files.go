package client

import (
	"context"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

type MemberFiles interface {
	Import(context.Context) (domain.MemberImportResult, bool, error)
	Export(context.Context) (bool, error)
}

func (c *ResourceController) Import(files MemberFiles) {
	if c.closed || c.Busy {
		return
	}
	c.Busy = true
	c.Error = nil
	c.ImportResult = nil
	c.Exported = false
	c.changed()
	go func() {
		result, selected, err := files.Import(c.ctx)
		c.dispatch(func() {
			if c.closed {
				return
			}
			c.Busy = false
			c.Error = err
			if err == nil && selected {
				c.ImportResult = &result
				c.Refresh(application.Members)
				return
			}
			c.finishResourceUpdate(err)
		})
	}()
}
func (c *ResourceController) Export(files MemberFiles) {
	if c.closed || c.Busy {
		return
	}
	c.Busy = true
	c.Error = nil
	c.ImportResult = nil
	c.Exported = false
	c.changed()
	go func() {
		selected, err := files.Export(c.ctx)
		c.dispatch(func() {
			if c.closed {
				return
			}
			c.Busy = false
			c.Error = err
			c.Exported = err == nil && selected
			c.finishResourceUpdate(err)
		})
	}()
}
