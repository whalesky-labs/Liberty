package client

import (
	"context"

	"liberty.local/liberty/internal/application"
)

type DeviceStorageService interface {
	Usage(context.Context) (application.DeviceStorageReport, error)
}

type DeviceStorageController struct {
	Report       *application.DeviceStorageReport
	Error        error
	Busy, Loaded bool
	service      DeviceStorageService
	dispatch     func(func())
	changed      func()
	ctx          context.Context
	cancel       context.CancelFunc
	queryCancel  context.CancelFunc
	revision     uint64
	closed       bool
}

func NewDeviceStorage(service DeviceStorageService, dispatch func(func()), changed func()) *DeviceStorageController {
	ctx, cancel := context.WithCancel(context.Background())
	return &DeviceStorageController{service: service, dispatch: dispatch, changed: changed, ctx: ctx, cancel: cancel}
}

func (c *DeviceStorageController) EnsureLoaded() {
	if c == nil || c.Loaded || c.Busy || c.service == nil {
		return
	}
	c.Refresh()
}

func (c *DeviceStorageController) Refresh() {
	if c == nil || c.closed || c.Busy || c.service == nil {
		return
	}
	if c.queryCancel != nil {
		c.queryCancel()
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.queryCancel = cancel
	c.revision++
	revision := c.revision
	c.Busy = true
	c.Error = nil
	if c.changed != nil {
		c.changed()
	}
	go func() {
		report, err := c.service.Usage(ctx)
		if c.dispatch == nil {
			return
		}
		c.dispatch(func() {
			if c.closed || revision != c.revision {
				return
			}
			c.Busy = false
			c.Error = err
			if err == nil {
				c.Report = &report
				c.Loaded = true
			}
			if c.changed != nil {
				c.changed()
			}
		})
	}()
}

func (c *DeviceStorageController) Close() {
	if c == nil || c.closed {
		return
	}
	c.closed = true
	c.revision++
	c.cancel()
	if c.queryCancel != nil {
		c.queryCancel()
	}
}
