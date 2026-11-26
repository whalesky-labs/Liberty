package application_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
)

type liveCaptureFixture struct {
	frames  chan []byte
	started chan struct{}
	active  atomic.Int32
}

func (c *liveCaptureFixture) Devices(context.Context) ([]domain.AudioDevice, error) {
	return []domain.AudioDevice{{ID: "synthetic", Name: "Synthetic input"}}, nil
}
func (c *liveCaptureFixture) Capture(ctx context.Context, _ string, emit func([]byte) error) error {
	c.active.Add(1)
	defer c.active.Add(-1)
	c.started <- struct{}{}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case pcm := <-c.frames:
			if err := emit(pcm); err != nil {
				return err
			}
		}
	}
}

type liveASRFixture struct {
	hold, slow bool
	gate       <-chan struct{}
	accepted   *atomic.Int32
}

func (liveASRFixture) Prepare(context.Context, func(string)) error { return nil }
func (liveASRFixture) Ready(context.Context) bool                  { return true }
func (a liveASRFixture) Run(ctx context.Context, frames <-chan domain.LiveAudio, emit func(domain.LiveRecognitionEvent) error) error {
	if a.hold {
		<-ctx.Done()
		return ctx.Err()
	}
	if err := emit(domain.LiveRecognitionEvent{Type: "ready"}); err != nil {
		return err
	}
	if a.slow {
		<-ctx.Done()
		return ctx.Err()
	}
	if a.gate != nil {
		select {
		case <-a.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	id := int64(1)
	count := 0
	var start, end int64
	final := func() error {
		if count == 0 {
			return nil
		}
		err := emit(domain.LiveRecognitionEvent{Type: "final", SegmentID: id, Revision: 2, StartMS: start, EndMS: end, Text: fmt.Sprintf("synthetic phrase %d", id)})
		id++
		count = 0
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case f := <-frames:
			switch f.Kind {
			case "audio":
				if a.accepted != nil {
					a.accepted.Add(1)
				}
				if count == 0 {
					start = f.OffsetMS
				}
				count++
				end = f.OffsetMS + int64(len(f.PCM)/32)
				if count == 1 {
					if err := emit(domain.LiveRecognitionEvent{Type: "partial", SegmentID: id, Revision: 1, StartMS: start, EndMS: end, Text: "synthetic partial"}); err != nil {
						return err
					}
				}
				if count == 2 {
					if err := final(); err != nil {
						return err
					}
				}
			case "boundary":
				if err := final(); err != nil {
					return err
				}
				if err := emit(domain.LiveRecognitionEvent{Type: "boundary"}); err != nil {
					return err
				}
			case "stop":
				if err := final(); err != nil {
					return err
				}
				return emit(domain.LiveRecognitionEvent{Type: "stopped"})
			}
		}
	}
}

func TestTemporaryASRBurstUsesBoundedCaptureQueueWithoutDroppingFrames(t *testing.T) {
	gate := make(chan struct{})
	var accepted atomic.Int32
	s, _, capture, _, budget, _ := newLiveFixture(t, liveASRFixture{gate: gate, accepted: &accepted})
	ctx := context.Background()
	request := liveRequest()
	request.Translate = false
	run, err := s.Start(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	awaitLive(t, s, func(v domain.LiveSnapshot) bool { return v.Session.State == "listening" })
	// The ASR input fits 25 frames. This burst must be retained across the
	// two bounded queues until the recognizer resumes, not fail at frame 26.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for range 35 {
		<-ticker.C
		select {
		case capture.frames <- make([]byte, domain.LiveFrameBytes):
		case <-time.After(time.Second):
			t.Fatal("capture blocked before bounded capacity")
		}
	}
	deadline := time.Now().Add(time.Second)
	for len(capture.frames) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(capture.frames) > 0 || s.Snapshot().Session.State != "listening" {
		t.Fatal("temporary ASR stall prematurely failed")
	}
	close(gate)
	awaitLive(t, s, func(v domain.LiveSnapshot) bool { return accepted.Load() == 35 })
	if err := s.Control(ctx, run.ID, "stop"); err != nil {
		t.Fatal(err)
	}
	awaitLive(t, s, func(v domain.LiveSnapshot) bool { return v.Session.State == "completed" && !budget.reserved.Load() })
	if accepted.Load() != 35 {
		t.Fatal("audio frames lost")
	}
}

type liveTranslationFixture struct{ fail atomic.Bool }

func (*liveTranslationFixture) ModelID() string { return "synthetic-local-translator-v1" }

func (p *liveTranslationFixture) Translate(_ context.Context, r domain.TranslationRequest, delta func(string)) (string, error) {
	if p.fail.Load() {
		return "", domain.ErrLiveTranslation
	}
	text := "Translated " + r.Text
	if delta != nil {
		delta(text)
	}
	return text, nil
}

type liveBudgetFixture struct{ reserved atomic.Bool }

func (b *liveBudgetFixture) ReserveLive() (func(), error) {
	if !b.reserved.CompareAndSwap(false, true) {
		return nil, domain.ErrLiveBusy
	}
	return func() { b.reserved.Store(false) }, nil
}

func awaitLive(t *testing.T, s *application.LiveService, predicate func(domain.LiveSnapshot) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if predicate(s.Snapshot()) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("live state did not settle", s.Snapshot().Session.State, s.Snapshot().Session.ErrorCode)
}
func newLiveFixture(t *testing.T, asr liveASRFixture) (*application.LiveService, *jobdb.Store, *liveCaptureFixture, *liveTranslationFixture, *liveBudgetFixture, string) {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "live.sqlite3")
	store, _, err := jobdb.OpenPrepared(context.Background(), filename, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	capture := &liveCaptureFixture{frames: make(chan []byte, 20), started: make(chan struct{}, 20)}
	translation := &liveTranslationFixture{}
	budget := &liveBudgetFixture{}
	s := application.NewLiveService(store, capture, asr, translation, budget)
	t.Cleanup(func() { s.Close(); store.Close() })
	return s, store, capture, translation, budget, filename
}
func liveRequest() domain.LiveRequest {
	return domain.LiveRequest{DeviceID: "synthetic", SourceLanguage: "zh", TargetLanguage: "en", Translate: true}
}

type directionTranslationFixture struct{ requests []domain.TranslationRequest }

func (*directionTranslationFixture) ModelID() string { return "synthetic-bilingual" }
func (f *directionTranslationFixture) Translate(_ context.Context, r domain.TranslationRequest, _ func(string)) (string, error) {
	f.requests = append(f.requests, r)
	return r.TargetLanguage + ": " + r.Text, nil
}

func TestAutomaticLiveTranslationPersistsModeAndRoutesEachPhrase(t *testing.T) {
	ctx := context.Background()
	database := filepath.Join(t.TempDir(), "auto.sqlite3")
	store, _, err := jobdb.OpenPrepared(ctx, database, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, auto := range []bool{true, false} {
		id := fmt.Sprint("session-", auto)
		v := domain.LiveSession{ID: id, State: "preparing", SourceLanguage: "zh", TargetLanguage: "en", Translate: true, AutoDetect: auto, CreatedAt: "now", UpdatedAt: "now"}
		if err := store.CreateLive(ctx, v); err != nil {
			t.Fatal(err)
		}
		if err := store.SetLiveState(ctx, id, "listening", "", "now"); err != nil {
			t.Fatal(err)
		}
		for i, text := range []string{"明天三点开会。", "The meeting starts tomorrow.", "2026"} {
			if err := store.SaveLiveSegment(ctx, domain.LiveSegment{SessionID: id, ID: int64(i + 1), Revision: 1, StartMS: int64(i * 1000), EndMS: int64((i + 1) * 1000), Text: text, TranslationState: "pending"}); err != nil {
				t.Fatal(err)
			}
		}
		for _, state := range []string{"stopping", "completed"} {
			if err := store.SetLiveState(ctx, id, state, "", "now"); err != nil {
				t.Fatal(err)
			}
		}
	}
	store.Close()
	store, err = jobdb.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	translator := &directionTranslationFixture{}
	service := application.NewLiveService(store, nil, nil, translator, nil)
	defer service.Close()
	for _, auto := range []bool{true, false} {
		id := fmt.Sprint("session-", auto)
		session, err := store.LiveSession(ctx, id)
		if err != nil || session.AutoDetect != auto {
			t.Fatal("automatic mode lost after reopen", err)
		}
		translator.requests = nil
		if err := service.RetryTranslations(ctx, id); err != nil {
			t.Fatal(err)
		}
		want := 3
		if auto {
			want = 2
		}
		if len(translator.requests) != want {
			t.Fatal("unexpected translation requests")
		}
		for i, r := range translator.requests {
			source, target := "zh", "en"
			if auto && i == 1 {
				source, target = "en", "zh"
			}
			if r.SourceLanguage != source || r.TargetLanguage != target {
				t.Fatal("incorrect direction", r.SourceLanguage, r.TargetLanguage)
			}
		}
		rows, err := store.LiveSegments(ctx, id, 0, 10)
		if err != nil || len(rows) != 3 {
			t.Fatal(err)
		}
		if auto && (rows[1].Translation != "zh: The meeting starts tomorrow." || rows[2].Translation != "2026") {
			t.Fatal("automatic translations not persisted")
		}
	}
}

func TestLiveSessionPersistsFinalsPausesCaptureAndDrainsBeforeRelease(t *testing.T) {
	s, store, capture, translation, budget, filename := newLiveFixture(t, liveASRFixture{})
	ctx := context.Background()
	translation.fail.Store(true)
	run, err := s.Start(ctx, liveRequest())
	if err != nil {
		t.Fatal(err)
	}
	awaitLive(t, s, func(v domain.LiveSnapshot) bool { return v.Session.State == "listening" })
	capture.frames <- make([]byte, domain.LiveFrameBytes)
	awaitLive(t, s, func(v domain.LiveSnapshot) bool { return v.Partial != nil })
	if rows, err := store.LiveSegments(ctx, run.ID, 0, 100); err != nil || len(rows) != 0 {
		t.Fatal("partial hypothesis persisted", err)
	}
	if err := s.Control(ctx, run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	awaitLive(t, s, func(v domain.LiveSnapshot) bool {
		return v.Session.State == "paused" && len(v.Segments) == 1 && v.Segments[0].TranslationState == "failed"
	})
	if capture.active.Load() != 0 || !budget.reserved.Load() {
		t.Fatal("pause did not release capture or lost the warm ASR reservation")
	}
	if _, err := s.Start(ctx, liveRequest()); !errors.Is(err, domain.ErrLiveBusy) {
		t.Fatal("second session admitted", err)
	}
	translation.fail.Store(false)
	if err := s.Control(ctx, run.ID, "resume"); err != nil {
		t.Fatal(err)
	}
	awaitLive(t, s, func(v domain.LiveSnapshot) bool { return v.Session.State == "listening" })
	capture.frames <- make([]byte, domain.LiveFrameBytes)
	awaitLive(t, s, func(v domain.LiveSnapshot) bool { return v.Partial != nil })
	if err := s.Control(ctx, run.ID, "stop"); err != nil {
		t.Fatal(err)
	}
	awaitLive(t, s, func(v domain.LiveSnapshot) bool { return v.Session.State == "completed" && !budget.reserved.Load() })
	if capture.active.Load() != 0 {
		t.Fatal("capture leaked")
	}
	rows, err := store.LiveSegments(ctx, run.ID, 0, 100)
	if err != nil || len(rows) != 2 || rows[1].TranslationState != "completed" {
		t.Fatal("tail/translation was lost", rows, err)
	}
	if err := s.RetryTranslations(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	edited := rows[0]
	edited.Text = "Corrected original"
	edited.Translation = "Corrected translation"
	if err := s.Edit(ctx, edited); err != nil {
		t.Fatal("finalized caption edit failed", err)
	}
	if err := s.Edit(ctx, edited); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("stale editor overwrote corrected text", err)
	}
	rows, err = store.LiveSegments(ctx, run.ID, 0, 100)
	if err != nil || rows[0].TranslationState != "completed" {
		t.Fatal("failed translation could not be retried", err)
	}
	var exported string
	if err := s.Export(ctx, run.ID, "srt", func(data []byte) error { exported = string(data); return nil }); err != nil || !strings.Contains(exported, "00:00:00,000 -->") || !strings.Contains(exported, "Translated synthetic phrase 2") {
		t.Fatal("bilingual subtitle export", err)
	}
	s.Close()
	store.Close()
	reopened, _, err := jobdb.OpenPrepared(ctx, filename, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rows, err = reopened.LiveSegments(ctx, run.ID, 0, 100)
	if err != nil || len(rows) != 2 || rows[1].Text != "synthetic phrase 2" {
		t.Fatal("finals did not survive reopening", err)
	}
}
func TestLiveStopDuringPreparationNeverOpensMicrophone(t *testing.T) {
	s, _, capture, _, budget, _ := newLiveFixture(t, liveASRFixture{hold: true})
	run, err := s.Start(context.Background(), liveRequest())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Control(context.Background(), run.ID, "stop"); err != nil {
		t.Fatal(err)
	}
	awaitLive(t, s, func(v domain.LiveSnapshot) bool { return v.Session.State == "completed" && !budget.reserved.Load() })
	if capture.active.Load() != 0 || len(capture.started) > 0 {
		t.Fatal("preparation cancellation opened microphone")
	}
}

func TestDeletingEndedLiveSessionClearsDisplayedAndPersistedCaptions(t *testing.T) {
	s, store, capture, _, budget, _ := newLiveFixture(t, liveASRFixture{})
	ctx := context.Background()
	run, err := s.Start(ctx, liveRequest())
	if err != nil {
		t.Fatal(err)
	}
	awaitLive(t, s, func(v domain.LiveSnapshot) bool { return v.Session.State == "listening" })
	capture.frames <- make([]byte, domain.LiveFrameBytes)
	awaitLive(t, s, func(v domain.LiveSnapshot) bool { return v.Partial != nil })
	if err := s.Control(ctx, run.ID, "stop"); err != nil {
		t.Fatal(err)
	}
	awaitLive(t, s, func(v domain.LiveSnapshot) bool { return v.Session.State == "completed" && !budget.reserved.Load() })
	before := s.Snapshot()
	if len(before.Segments) != 1 {
		t.Fatal("finalized caption missing")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if s.Delete(canceled, run.ID) == nil || s.Snapshot().Session.ID != run.ID {
		t.Fatal("failed deletion discarded the displayed session")
	}
	if err := s.Delete(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	after := s.Snapshot()
	if after.Session.ID != "" || len(after.Segments) != 0 || after.Partial != nil || after.Revision <= before.Revision {
		t.Fatal("deleted captions still visible")
	}
	if _, err := store.LiveSession(ctx, run.ID); !errors.Is(err, domain.ErrResourceMissing) {
		t.Fatal("session survived deletion", err)
	}
	if rows, err := store.LiveSegments(ctx, run.ID, 0, 100); err != nil || len(rows) != 0 {
		t.Fatal("captions survived deletion", err)
	}
}

func TestLiveOverloadFailsExplicitlyAndReleasesCaptureAndBudget(t *testing.T) {
	s, _, capture, _, budget, _ := newLiveFixture(t, liveASRFixture{slow: true})
	request := liveRequest()
	request.Translate = false
	if _, err := s.Start(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	awaitLive(t, s, func(v domain.LiveSnapshot) bool { return v.Session.State == "listening" })
	for index := 0; index < 100; index++ {
		select {
		case capture.frames <- make([]byte, domain.LiveFrameBytes):
		case <-time.After(5 * time.Millisecond):
		}
	}
	awaitLive(t, s, func(v domain.LiveSnapshot) bool { return v.Session.State == "failed" && !budget.reserved.Load() })
	if s.Snapshot().Session.ErrorCode != "live_audio_backpressure" || capture.active.Load() != 0 {
		t.Fatal("overload was silent or capture leaked", s.Snapshot().Session.ErrorCode)
	}
}
func TestLivePersistenceRejectsLateRevisionAndRecoversInterruptedSession(t *testing.T) {
	_, store, _, _, _, _ := newLiveFixture(t, liveASRFixture{})
	ctx := context.Background()
	stamp := domain.PersistedUTC(time.Now())
	run := domain.LiveSession{ID: "synthetic-live", State: "preparing", CreatedAt: stamp, UpdatedAt: stamp, SourceLanguage: "zh", TargetLanguage: "en", Translate: true}
	if err := store.CreateLive(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := store.SetLiveState(ctx, run.ID, "listening", "", stamp); err != nil {
		t.Fatal(err)
	}
	v := domain.LiveSegment{SessionID: run.ID, ID: 1, Revision: 2, StartMS: 0, EndMS: 500, Text: "synthetic", TranslationState: "pending"}
	if err := store.SaveLiveSegment(ctx, v); err != nil {
		t.Fatal(err)
	}
	old := v
	old.Revision = 1
	old.Translation = "stale"
	old.TranslationState = "completed"
	if !errors.Is(store.SaveLiveTranslation(ctx, old), domain.ErrConflict) {
		t.Fatal("stale translation overwrote newer text")
	}
	if err := store.RecoverLive(ctx, stamp); err != nil {
		t.Fatal(err)
	}
	if err := store.RecoverLive(ctx, stamp); err != nil {
		t.Fatal("restart recovery is not idempotent", err)
	}
	actual, err := store.LiveSession(ctx, run.ID)
	if err != nil || actual.State != "interrupted" || actual.SegmentCount != 1 {
		t.Fatal(actual, err)
	}
	v.ID = 2
	if !errors.Is(store.SaveLiveSegment(ctx, v), domain.ErrConflict) {
		t.Fatal("terminal session accepted late ASR")
	}
}
