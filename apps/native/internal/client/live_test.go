package client

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

type liveClientFixture struct {
	snapshot   domain.LiveSnapshot
	started    chan struct{}
	release    chan struct{}
	controlled []string
	optionsErr error
	history    []domain.LiveSession
	segments   chan liveSegmentsCall
}

type liveSegmentsCall struct {
	id    string
	after int64
	reply chan liveSegmentsReply
}

type liveSegmentsReply struct {
	values []domain.LiveSegment
	err    error
}

func (f *liveClientFixture) Options(context.Context) (application.LiveOptions, error) {
	return application.LiveOptions{}, f.optionsErr
}
func (f *liveClientFixture) Prepare(ctx context.Context, _ func(string)) error {
	<-ctx.Done()
	return ctx.Err()
}
func (f *liveClientFixture) Start(context.Context, domain.LiveRequest) (domain.LiveSession, error) {
	return domain.LiveSession{ID: "synthetic", State: "preparing"}, nil
}
func (f *liveClientFixture) Control(_ context.Context, _ string, command string) error {
	f.controlled = append(f.controlled, command)
	return nil
}
func (f *liveClientFixture) Snapshot() domain.LiveSnapshot {
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.release != nil {
		<-f.release
	}
	return f.snapshot
}
func (f *liveClientFixture) History(context.Context, int) ([]domain.LiveSession, error) {
	return f.history, nil
}

func TestSavedLiveHistoryLoadsWhenMicrophoneRuntimeIsUnavailable(t *testing.T) {
	fixture := &liveClientFixture{optionsErr: domain.ErrRuntime, history: []domain.LiveSession{{ID: "saved", State: "interrupted"}}}
	callbacks := make(chan func(), 4)
	c := NewLive(fixture, nil, func(f func()) { callbacks <- f }, func() {})
	defer c.Close()
	c.RefreshOptions()
	select {
	case f := <-callbacks:
		f()
	case <-time.After(time.Second):
		t.Fatal("options did not finish")
	}
	if c.Busy || len(c.History) != 1 || c.History[0].ID != "saved" || c.Error != domain.ErrRuntime {
		t.Fatal("runtime error hid persisted history")
	}
}
func (f *liveClientFixture) Segments(ctx context.Context, id string, after int64) ([]domain.LiveSegment, error) {
	if f.segments == nil {
		return nil, nil
	}
	call := liveSegmentsCall{id: id, after: after, reply: make(chan liveSegmentsReply, 1)}
	select {
	case f.segments <- call:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case result := <-call.reply:
		return result.values, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (f *liveClientFixture) RetryTranslations(context.Context, string) error { return nil }
func (f *liveClientFixture) Delete(context.Context, string) error            { return nil }
func (f *liveClientFixture) Edit(context.Context, domain.LiveSegment) error  { return nil }

func TestLiveControllerHiddenDoesNotStopCaptureAndIgnoresLateSnapshot(t *testing.T) {
	fixture := &liveClientFixture{snapshot: domain.LiveSnapshot{Session: domain.LiveSession{ID: "synthetic", State: "listening"}, Revision: 2}, started: make(chan struct{}, 2), release: make(chan struct{}, 2)}
	callbacks := make(chan func(), 8)
	changes := 0
	c := NewLive(fixture, nil, func(f func()) { callbacks <- f }, func() { changes++ })
	c.initialized = true
	c.visible = true
	c.Snapshot = domain.LiveSnapshot{Session: domain.LiveSession{ID: "synthetic", State: "listening"}, Revision: 1}
	c.Poll()
	select {
	case <-fixture.started:
	case <-time.After(time.Second):
		t.Fatal("snapshot not requested")
	}
	c.Poll()
	select {
	case <-fixture.started:
		t.Fatal("overlapping snapshot")
	default:
	}
	c.SetVisible(false)
	if len(fixture.controlled) != 0 {
		t.Fatal("hiding page controlled capture")
	}
	before := changes
	c.Close()
	fixture.release <- struct{}{}
	select {
	case f := <-callbacks:
		f()
	case <-time.After(time.Second):
		t.Fatal("snapshot not returned")
	}
	if c.Snapshot.Revision != 1 || changes != before {
		t.Fatal("closed controller accepted late response")
	}
}

func TestNewLiveSessionClearsOldCaptionsAndRejectsPriorPoll(t *testing.T) {
	previous := domain.LiveSnapshot{Session: domain.LiveSession{ID: "previous", State: "completed"}, Segments: []domain.LiveSegment{{ID: 1, Text: "Previous caption"}}, Revision: 5}
	fixture := &liveClientFixture{snapshot: previous, started: make(chan struct{}, 2), release: make(chan struct{}, 2)}
	callbacks := make(chan func(), 8)
	c := NewLive(fixture, nil, func(f func()) { callbacks <- f }, func() {})
	defer c.Close()
	c.visible = true
	c.initialized = true
	c.Snapshot = previous
	callback := func() {
		t.Helper()
		select {
		case f := <-callbacks:
			f()
		case <-time.After(time.Second):
			t.Fatal("UI callback missing")
		}
	}
	c.Poll()
	select {
	case <-fixture.started:
	case <-time.After(time.Second):
		t.Fatal("prior poll did not start")
	}
	c.Begin()
	callback()
	if c.Snapshot.Session.ID != "synthetic" || len(c.Snapshot.Segments) != 0 {
		t.Fatal("previous captions leaked into new session")
	}
	fixture.release <- struct{}{}
	callback()
	if c.Snapshot.Session.ID != "synthetic" || len(c.Snapshot.Segments) != 0 {
		t.Fatal("old poll replaced new session")
	}
}

func TestSelectedLiveSessionFollowsSnapshotAndKeepsSubtitlePage(t *testing.T) {
	for _, state := range []string{"listening", "paused", "completed", "failed", "interrupted"} {
		t.Run(state, func(t *testing.T) {
			active := domain.LiveSession{ID: "synthetic", State: "listening", Translate: true, SegmentCount: 201}
			updated := active
			updated.State = state
			updated.SegmentCount = 202
			page := []domain.LiveSegment{{SessionID: active.ID, ID: 101, Revision: 1, Text: "Earlier caption", Translation: "Earlier translation"}}
			fixture := &liveClientFixture{
				snapshot: domain.LiveSnapshot{Session: updated, Revision: 2, Segments: []domain.LiveSegment{{ID: 202, Text: "Latest caption"}}},
				segments: make(chan liveSegmentsCall, 1),
			}
			callbacks := make(chan func(), 8)
			c := NewLive(fixture, nil, func(f func()) { callbacks <- f }, func() {})
			defer c.Close()
			c.visible = true
			c.Busy = state != "completed" // A concurrent action must not prevent the selected page from catching up.
			c.Snapshot = domain.LiveSnapshot{Session: active, Revision: 1}
			c.Selected = &active
			c.SegmentAfter = 100
			c.Poll()
			call := receive(t, fixture.segments)
			if call.id != active.ID || call.after != 100 {
				t.Fatal("poll did not request the selected subtitle page")
			}
			call.reply <- liveSegmentsReply{values: page}
			receive(t, callbacks)()
			if c.Snapshot.Session != updated || c.Selected == nil || *c.Selected != updated {
				t.Fatal("selected history session did not follow the current session")
			}
			if c.SegmentAfter != 100 || !reflect.DeepEqual(c.Segments, page) {
				t.Fatal("poll replaced the selected page with the rolling live caption window")
			}
		})
	}
}

func TestSelectedLiveSessionRetriesFailedFinalPageRead(t *testing.T) {
	active := domain.LiveSession{ID: "synthetic", State: "listening"}
	ended := active
	ended.State = "completed"
	fixture := &liveClientFixture{snapshot: domain.LiveSnapshot{Session: ended, Revision: 2}, segments: make(chan liveSegmentsCall, 1)}
	callbacks := make(chan func(), 8)
	c := NewLive(fixture, nil, func(f func()) { callbacks <- f }, func() {})
	defer c.Close()
	c.visible = true
	c.Busy = true
	c.Snapshot = domain.LiveSnapshot{Session: active, Revision: 1}
	c.Selected = &active
	readErr := errors.New("synthetic subtitle read failure")
	c.Poll()
	receive(t, fixture.segments).reply <- liveSegmentsReply{err: readErr}
	receive(t, callbacks)()
	if c.Snapshot.Session != ended || !errors.Is(c.Error, readErr) {
		t.Fatal("subtitle read failure hid the completed snapshot or its error")
	}
	c.Poll()
	page := []domain.LiveSegment{{SessionID: active.ID, ID: 1, Text: "Final caption"}}
	receive(t, fixture.segments).reply <- liveSegmentsReply{values: page}
	receive(t, callbacks)()
	if c.Selected == nil || *c.Selected != ended || !reflect.DeepEqual(c.Segments, page) || c.Error != nil {
		t.Fatal("selected page did not recover when the snapshot revision stayed unchanged")
	}
}

func TestUnchangedSelectedLiveSessionDoesNotReloadOrClearActionError(t *testing.T) {
	for _, state := range []string{"listening", "paused", "completed"} {
		t.Run(state, func(t *testing.T) {
			session := domain.LiveSession{ID: "synthetic", State: state}
			fixture := &liveClientFixture{snapshot: domain.LiveSnapshot{Session: session, Revision: 2}, segments: make(chan liveSegmentsCall, 1)}
			callbacks := make(chan func(), 1)
			changes := 0
			c := NewLive(fixture, nil, func(f func()) { callbacks <- f }, func() { changes++ })
			defer c.Close()
			c.visible = true
			c.Snapshot = fixture.snapshot
			c.Selected = &session
			actionErr := errors.New("synthetic export failure")
			c.Error = actionErr
			c.Poll()
			receive(t, callbacks)()
			if changes != 0 || !errors.Is(c.Error, actionErr) {
				t.Fatal("unchanged polling redrew the page or cleared an unrelated action error")
			}
			select {
			case <-fixture.segments:
				t.Fatal("unchanged selected session reloaded the subtitle page")
			default:
			}
		})
	}
}

func TestLivePollDiscardsPageReadAfterHistorySelectionChanges(t *testing.T) {
	for _, selection := range []string{"another session", "another page", "current captions"} {
		t.Run(selection, func(t *testing.T) {
			active := domain.LiveSession{ID: "synthetic", State: "listening"}
			fixture := &liveClientFixture{snapshot: domain.LiveSnapshot{Session: active, Revision: 2}, segments: make(chan liveSegmentsCall, 2)}
			callbacks := make(chan func(), 8)
			c := NewLive(fixture, nil, func(f func()) { callbacks <- f }, func() {})
			defer c.Close()
			c.visible = true
			c.Snapshot = domain.LiveSnapshot{Session: active, Revision: 1}
			c.Selected = &active
			c.Poll()
			stale := receive(t, fixture.segments)
			other := domain.LiveSession{ID: "previous", State: "completed"}
			if selection == "another page" {
				other = active
			}
			page := []domain.LiveSegment{{SessionID: other.ID, ID: 101, Text: "Historical caption"}}
			if selection == "current captions" {
				c.ShowCurrent()
			} else {
				c.Select(other, 100)
				receive(t, fixture.segments).reply <- liveSegmentsReply{values: page}
				receive(t, callbacks)()
				receive(t, callbacks)()
			}
			stale.reply <- liveSegmentsReply{values: []domain.LiveSegment{{ID: 1, Text: "Stale caption"}}}
			receive(t, callbacks)()
			if selection == "current captions" {
				if c.Selected != nil || len(c.Segments) != 0 {
					t.Fatal("late page read restored the previous history selection")
				}
			} else if c.Selected == nil || *c.Selected != other || c.SegmentAfter != 100 || !reflect.DeepEqual(c.Segments, page) {
				t.Fatal("late page read overwrote a newer history selection")
			}
		})
	}
}
