package client

import (
	"context"
	"testing"
	"time"
)

func TestCompanionPreviewClockPausesWithoutWakeups(t *testing.T) {
	c := NewCompanionPreview(func(context.Context, string) (int, error) { return 0, nil }, func(fn func()) { fn() }, func(int, error) {}, func() {})
	now := time.Unix(100, 0)
	c.SetPlaying(true, now)
	pose, delay := c.Tick(now.Add(350 * time.Millisecond))
	if pose != 1 || delay != 250*time.Millisecond {
		t.Fatal(pose, delay)
	}
	c.SetActive(false, now.Add(350*time.Millisecond))
	pose, delay = c.Tick(now.Add(time.Hour))
	if pose != 1 || delay != 0 {
		t.Fatal("hidden animation advanced")
	}
	c.SetActive(true, now.Add(time.Hour))
	pose, _ = c.Tick(now.Add(time.Hour + 300*time.Millisecond))
	if pose != 2 {
		t.Fatal("resume lost held pose")
	}
	c.SetPlaying(false, now.Add(time.Hour+300*time.Millisecond))
	pose, delay = c.Tick(now.Add(2 * time.Hour))
	if pose != 2 || delay != 0 {
		t.Fatal("pause woke renderer")
	}
	c.Seek(8, now)
	pose, delay = c.Tick(now)
	if pose != 8 || delay != 0 {
		t.Fatal("manual pose")
	}
	c.Close()
	c.SetPlaying(true, now)
	_, delay = c.Tick(now)
	if delay != 0 {
		t.Fatal("closed wakeup")
	}
}

func TestCompanionPreviewSerialLoadDropsStaleAndClosedResults(t *testing.T) {
	dispatch := make(chan func(), 5)
	started := make(chan string, 5)
	release := make(chan struct{}, 5)
	loaded := []string{}
	c := NewCompanionPreview(func(ctx context.Context, name string) (string, error) {
		started <- name
		select {
		case <-release:
			return name, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}, func(fn func()) { dispatch <- fn }, func(value string, err error) {
		if err == nil {
			loaded = append(loaded, value)
		}
	}, func() {})
	wait := func() func() {
		t.Helper()
		select {
		case fn := <-dispatch:
			return fn
		case <-time.After(time.Second):
			t.Fatal("missing dispatch")
			return nil
		}
	}
	c.Select("crush")
	if <-started != "crush" {
		t.Fatal("first load")
	}
	c.Select("work")
	c.Select("rope")
	select {
	case name := <-started:
		t.Fatal("concurrent decode", name)
	default:
	}
	release <- struct{}{}
	wait()()
	if len(loaded) != 0 || <-started != "rope" {
		t.Fatal("stale model or missing latest selection")
	}
	release <- struct{}{}
	wait()()
	if len(loaded) != 1 || loaded[0] != "rope" || c.Loading {
		t.Fatal(loaded)
	}
	c.Select("sleep")
	<-started
	c.Close()
	wait()()
	if len(loaded) != 1 {
		t.Fatal("late scene reached closed renderer")
	}
}
