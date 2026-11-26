package client

import (
	"context"
	"math"
	"time"
)

// CompanionPreview serializes scene loads and owns the visibility/playback
// clock. Callbacks and public methods belong to the UI dispatcher. No ticker
// runs while hidden or paused, and stale loads never reach the renderer.
type CompanionPreview[T any] struct {
	Selected                 string
	Loading, Playing, Active bool
	Seconds                  float64
	load                     func(context.Context, string) (T, error)
	dispatch                 func(func())
	ready                    func(T, error)
	changed                  func()
	ctx                      context.Context
	cancel                   context.CancelFunc
	clock                    time.Time
	busy, closed             bool
	revision                 uint64
}

func NewCompanionPreview[T any](load func(context.Context, string) (T, error), dispatch func(func()), ready func(T, error), changed func()) *CompanionPreview[T] {
	ctx, cancel := context.WithCancel(context.Background())
	return &CompanionPreview[T]{Active: true, load: load, dispatch: dispatch, ready: ready, changed: changed, ctx: ctx, cancel: cancel}
}
func (c *CompanionPreview[T]) Select(name string) {
	if c.closed || name == "" {
		return
	}
	c.Selected = name
	c.revision++
	c.Loading = true
	c.Seconds = 0
	c.clock = time.Time{}
	c.changed()
	c.start()
}
func (c *CompanionPreview[T]) start() {
	if c.closed || c.busy || !c.Loading {
		return
	}
	c.busy = true
	name, revision := c.Selected, c.revision
	go func() {
		value, err := c.load(c.ctx, name)
		c.dispatch(func() {
			c.busy = false
			if c.closed {
				return
			}
			if revision != c.revision {
				c.start()
				return
			}
			c.Loading = false
			c.clock = time.Time{}
			c.ready(value, err)
			c.changed()
		})
	}()
}
func (c *CompanionPreview[T]) SetActive(active bool, now time.Time) {
	if c.closed {
		return
	}
	c.advance(now)
	c.Active = active
	c.clock = now
	c.changed()
}
func (c *CompanionPreview[T]) SetPlaying(playing bool, now time.Time) {
	if c.closed {
		return
	}
	c.advance(now)
	c.Playing = playing
	c.clock = now
	c.changed()
}
func (c *CompanionPreview[T]) Seek(pose int, now time.Time) {
	if c.closed {
		return
	}
	c.Seconds = float64(max(0, min(8, pose))) * .3
	c.clock = now
	c.changed()
}
func (c *CompanionPreview[T]) advance(now time.Time) {
	if !c.clock.IsZero() && c.Active && c.Playing && !c.Loading {
		elapsed := now.Sub(c.clock).Seconds()
		if elapsed > 0 {
			c.Seconds = math.Mod(c.Seconds+elapsed, 2.7)
		}
	}
	c.clock = now
}
func (c *CompanionPreview[T]) Tick(now time.Time) (int, time.Duration) {
	if c.closed {
		return 0, 0
	}
	c.advance(now)
	pose := min(8, int(math.Floor((c.Seconds+1e-7)/.3)))
	if !c.Active || !c.Playing || c.Loading {
		return pose, 0
	}
	delay := time.Duration((float64(pose+1)*.3 - c.Seconds) * float64(time.Second))
	return pose, max(time.Millisecond, delay)
}
func (c *CompanionPreview[T]) Close() {
	if c.closed {
		return
	}
	c.closed = true
	c.cancel()
	c.Playing = false
	c.Active = false
	c.revision++
}
