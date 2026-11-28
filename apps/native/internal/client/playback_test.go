package client

import (
	"context"
	"testing"
)

type playbackCall struct {
	ctx  context.Context
	id   string
	done chan struct{}
}
type playbackFixture struct{ calls chan playbackCall }

func (f playbackFixture) PlaySegment(ctx context.Context, id string, _ float64) error {
	call := playbackCall{ctx: ctx, id: id, done: make(chan struct{})}
	f.calls <- call
	<-call.done
	return ctx.Err()
}
func TestPlaybackSwitchCancelsOldAudioAndIgnoresLateCompletion(t *testing.T) {
	f := playbackFixture{calls: make(chan playbackCall, 2)}
	callbacks := make(chan func(), 2)
	c := NewPlayback(f, func(fn func()) { callbacks <- fn }, func() {})
	c.Play("first")
	first := receive(t, f.calls)
	c.Play("second")
	second := receive(t, f.calls)
	if first.ctx.Err() == nil || c.SegmentID != "second" {
		t.Fatal("old audio was not cancelled")
	}
	close(first.done)
	receive(t, callbacks)()
	if c.SegmentID != "second" {
		t.Fatal("late completion replaced current playback")
	}
	c.Close()
	if second.ctx.Err() == nil {
		t.Fatal("close retained audio")
	}
	close(second.done)
	receive(t, callbacks)()
}
