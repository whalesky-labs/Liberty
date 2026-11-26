package client

import (
	"context"
	"time"

	"liberty.local/liberty/internal/domain"
)

const ProcessMetricsInterval = time.Second

type MetricsController struct {
	Value                         *domain.ProcessMetrics
	sample                        func(context.Context) (domain.ProcessMetrics, error)
	dispatch                      func(func())
	changed                       func()
	ctx                           context.Context
	cancel                        context.CancelFunc
	active, busy, closed, started bool
	revision                      uint64
}

// Callbacks and public methods belong to the UI thread. Sampling is serial and
// runs outside that thread; hidden windows stop requesting samples.
func NewMetrics(sample func(context.Context) (domain.ProcessMetrics, error), dispatch func(func()), changed func()) *MetricsController {
	ctx, cancel := context.WithCancel(context.Background())
	return &MetricsController{sample: sample, dispatch: dispatch, changed: changed, ctx: ctx, cancel: cancel}
}

func (c *MetricsController) Start() {
	if c.closed || c.started {
		return
	}
	c.started = true
	c.SetActive(true)
	go func() {
		ticker := time.NewTicker(ProcessMetricsInterval)
		defer ticker.Stop()
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-ticker.C:
				c.dispatch(c.Refresh)
			}
		}
	}()
}

func (c *MetricsController) SetActive(active bool) {
	if c.closed || c.active == active {
		return
	}
	c.active = active
	c.revision++
	c.Value = nil
	c.changed()
	if active {
		c.Refresh()
	}
}

func (c *MetricsController) Refresh() {
	if c.closed || !c.active {
		return
	}
	if c.Value != nil && time.Since(c.Value.SampledAt) > 3*time.Second {
		c.Value = nil
		c.changed()
	}
	if c.busy {
		return
	}
	c.busy = true
	revision := c.revision
	go func() {
		ctx, cancel := context.WithTimeout(c.ctx, ProcessMetricsInterval)
		value, err := c.sample(ctx)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		cancel()
		c.dispatch(func() {
			c.busy = false
			if c.closed || !c.active || c.revision != revision {
				return
			}
			c.Value = nil
			if err == nil {
				c.Value = &value
			}
			c.changed()
		})
	}()
}

func (c *MetricsController) Close() {
	if c.closed {
		return
	}
	c.closed = true
	c.Value = nil
	c.cancel()
}
