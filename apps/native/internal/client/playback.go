package client

import (
	"context"
	"errors"
)

type SegmentPlayer interface {
	PlaySegment(context.Context, string, float64) error
}
type PlaybackController struct {
	SegmentID string
	Speed     float64
	Error     error
	player    SegmentPlayer
	dispatch  func(func())
	changed   func()
	cancel    context.CancelFunc
	revision  uint64
	closed    bool
}

func NewPlayback(player SegmentPlayer, dispatch func(func()), changed func()) *PlaybackController {
	return &PlaybackController{player: player, dispatch: dispatch, changed: changed, Speed: 1}
}
func (c *PlaybackController) Play(id string) {
	if c.closed {
		return
	}
	c.Stop()
	c.revision++
	revision := c.revision
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel, c.SegmentID, c.Error = cancel, id, nil
	speed := c.Speed
	c.changed()
	go func() {
		err := c.player.PlaySegment(ctx, id, speed)
		cancel()
		c.dispatch(func() {
			if c.closed || revision != c.revision {
				return
			}
			c.SegmentID, c.cancel = "", nil
			if !errors.Is(err, context.Canceled) {
				c.Error = err
			}
			c.changed()
		})
	}()
}
func (c *PlaybackController) Stop() {
	c.revision++
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	c.SegmentID = ""
}
func (c *PlaybackController) Close() { c.closed = true; c.Stop() }
