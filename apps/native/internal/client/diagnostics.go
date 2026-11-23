package client

import (
	"context"
	"liberty.local/liberty/internal/domain"
)

type DiagnosticService interface {
	Report(context.Context) (domain.DiagnosticReport, error)
}
type DiagnosticFiles interface {
	Export(context.Context) (bool, error)
}
type DiagnosticsController struct {
	Report         *domain.DiagnosticReport
	Error          error
	Busy, Exported bool
	service        DiagnosticService
	dispatch       func(func())
	changed        func()
	ctx            context.Context
	cancel         context.CancelFunc
	closed         bool
}

func NewDiagnostics(service DiagnosticService, dispatch func(func()), changed func()) *DiagnosticsController {
	ctx, cancel := context.WithCancel(context.Background())
	return &DiagnosticsController{service: service, dispatch: dispatch, changed: changed, ctx: ctx, cancel: cancel}
}
func (c *DiagnosticsController) Refresh() {
	if c.closed || c.Busy {
		return
	}
	c.Busy = true
	c.Error = nil
	c.Exported = false
	c.changed()
	go func() {
		report, err := c.service.Report(c.ctx)
		c.dispatch(func() {
			if c.closed {
				return
			}
			c.Busy = false
			c.Error = err
			if err == nil {
				c.Report = &report
			}
			c.changed()
		})
	}()
}
func (c *DiagnosticsController) Export(files DiagnosticFiles) {
	if c.closed || c.Busy || files == nil {
		return
	}
	c.Busy = true
	c.Error = nil
	c.Exported = false
	c.changed()
	go func() {
		exported, err := files.Export(c.ctx)
		c.dispatch(func() {
			if c.closed {
				return
			}
			c.Busy = false
			c.Error = err
			c.Exported = exported && err == nil
			c.changed()
		})
	}()
}
func (c *DiagnosticsController) Close() { c.closed = true; c.cancel() }
