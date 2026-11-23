package client

import (
	"context"
	"strings"

	"liberty.local/liberty/internal/domain"
)

// ResultActions is a bound workbench port, never an arbitrary job/path writer.
type ResultActions interface {
	RenameSpeaker(context.Context, string, string) error
}
type ResultFiles interface {
	Export(context.Context, domain.TextExportKind, string, string) (bool, error)
}
type ResultActionController struct {
	Busy      bool
	Error     error
	Exported  bool
	Exporting bool
	service   ResultActions
	files     ResultFiles
	confirm   func(context.Context, string, string, string) (bool, error)
	dispatch  func(func())
	changed   func()
	renamed   func(string, string)
	ctx       context.Context
	cancel    context.CancelFunc
	closed    bool
}

func NewResultActions(service ResultActions, files ResultFiles, confirm func(context.Context, string, string, string) (bool, error), dispatch func(func()), changed func(), renamed func(string, string)) *ResultActionController {
	ctx, cancel := context.WithCancel(context.Background())
	return &ResultActionController{service: service, files: files, confirm: confirm, dispatch: dispatch, changed: changed, renamed: renamed, ctx: ctx, cancel: cancel}
}
func (c *ResultActionController) Rename(from, to, locale string) {
	to = strings.TrimSpace(to)
	if c.closed || c.Busy || to == "" || c.service == nil || c.confirm == nil {
		return
	}
	c.Busy, c.Error, c.Exported, c.Exporting = true, nil, false, false
	c.changed()
	go func() {
		confirmed, err := false, c.ctx.Err()
		if err == nil {
			confirmed, err = c.confirm(c.ctx, from, to, locale)
		}
		if err == nil && confirmed && c.ctx.Err() == nil {
			err = c.service.RenameSpeaker(c.ctx, from, to)
		}
		c.dispatch(func() {
			if c.closed {
				return
			}
			c.Busy, c.Error = false, err
			if err == nil && confirmed {
				c.renamed(from, to)
			}
			c.changed()
		})
	}()
}
func (c *ResultActionController) Export(kind domain.TextExportKind, title, locale string) {
	if c.closed || c.Busy || c.files == nil {
		return
	}
	c.Busy, c.Error, c.Exported, c.Exporting = true, nil, false, true
	c.changed()
	go func() {
		selected, err := false, c.ctx.Err()
		if err == nil {
			selected, err = c.files.Export(c.ctx, kind, title, locale)
		}
		c.dispatch(func() {
			if c.closed {
				return
			}
			c.Busy, c.Error, c.Exported, c.Exporting = false, err, selected && err == nil, false
			c.changed()
		})
	}()
}
func (c *ResultActionController) Close() { c.closed = true; c.cancel() }
