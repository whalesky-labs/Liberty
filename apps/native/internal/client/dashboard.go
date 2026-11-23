package client

import (
	"context"

	"liberty.local/liberty/internal/domain"
)

type Dashboard interface {
	Overview(context.Context, string) (domain.DashboardOverview, error)
}

// DashboardController shares the same UI-thread ownership as Controller.
type DashboardController struct {
	Overview *domain.DashboardOverview
	Loading  bool
	Error    error
	service  Dashboard
	dispatch func(func())
	changed  func()
	cancel   context.CancelFunc
	revision uint64
	closed   bool
}

func NewDashboard(service Dashboard, dispatch func(func()), changed func()) *DashboardController {
	return &DashboardController{service: service, dispatch: dispatch, changed: changed}
}

func (c *DashboardController) Refresh(period string) {
	if c.closed {
		return
	}
	if c.cancel != nil {
		c.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.revision++
	revision := c.revision
	c.Loading, c.Error = true, nil
	c.changed()
	go func() {
		overview, err := c.service.Overview(ctx, period)
		c.dispatch(func() {
			if c.closed || revision != c.revision {
				return
			}
			c.Loading, c.Error = false, err
			if err == nil {
				c.Overview = &overview
			}
			c.changed()
		})
	}()
}

func (c *DashboardController) Close() {
	c.closed = true
	if c.cancel != nil {
		c.cancel()
	}
}
