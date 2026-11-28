package application

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"liberty.local/liberty/internal/domain"
)

type LiveOptions struct {
	Devices          []domain.AudioDevice
	TranslationReady bool
	ModelReady       bool
}
type LiveModelStatus struct {
	TranslationReady bool
	ModelReady       bool
}
type liveRun struct {
	session domain.LiveSession
	request domain.LiveRequest
	ctx     context.Context
	cancel  context.CancelFunc
	control chan string
	done    chan struct{}
	release func()
}

type LiveService struct {
	mu          sync.Mutex
	repository  LiveRepository
	capture     LiveCapture
	asr         StreamingASR
	translation TranslationProvider
	budget      LiveBudget
	active      *liveRun
	snapshot    domain.LiveSnapshot
	ctx         context.Context
	cancel      context.CancelFunc
	operations  sync.WaitGroup
	retrying    bool
	preparing   bool
	closed      bool
}

func NewLiveService(repository LiveRepository, capture LiveCapture, asr StreamingASR, translation TranslationProvider, budget LiveBudget) *LiveService {
	ctx, cancel := context.WithCancel(context.Background())
	return &LiveService{repository: repository, capture: capture, asr: asr, translation: translation, budget: budget, ctx: ctx, cancel: cancel}
}
func (s *LiveService) Recover(ctx context.Context) error {
	return s.repository.RecoverLive(ctx, domain.PersistedUTC(time.Now()))
}
func (s *LiveService) Options(ctx context.Context) (LiveOptions, error) {
	status := s.ModelStatus(ctx)
	o := LiveOptions{ModelReady: status.ModelReady, TranslationReady: status.TranslationReady}
	devices, err := s.capture.Devices(ctx)
	o.Devices = devices
	return o, err
}

func (s *LiveService) ModelStatus(ctx context.Context) LiveModelStatus {
	status := LiveModelStatus{ModelReady: s.asr.Ready(ctx)}
	if local, ok := s.translation.(interface{ Ready(context.Context) bool }); ok {
		status.TranslationReady = local.Ready(ctx)
	} else {
		status.TranslationReady = true
	}
	return status
}
func (s *LiveService) operation(ctx context.Context) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil, domain.ErrLiveClosed
	}
	s.operations.Add(1)
	child, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	return child, func() { stop(); cancel(); s.operations.Done() }, nil
}
func (s *LiveService) Prepare(ctx context.Context, progress func(string)) error {
	ctx, done, err := s.operation(ctx)
	if err != nil {
		return err
	}
	defer done()
	s.mu.Lock()
	if s.preparing || s.retrying || s.active != nil {
		s.mu.Unlock()
		return domain.ErrLiveBusy
	}
	s.preparing = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.preparing = false; s.mu.Unlock() }()
	if err := s.asr.Prepare(ctx, progress); err != nil {
		return err
	}
	if local, ok := s.translation.(interface {
		Prepare(context.Context, func(string)) error
	}); ok {
		return local.Prepare(ctx, progress)
	}
	return nil
}
func (s *LiveService) Start(ctx context.Context, request domain.LiveRequest) (domain.LiveSession, error) {
	if err := request.Validate(); err != nil {
		return domain.LiveSession{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return domain.LiveSession{}, domain.ErrLiveClosed
	}
	if s.active != nil || s.retrying || s.preparing {
		return domain.LiveSession{}, domain.ErrLiveBusy
	}
	if !s.asr.Ready(ctx) {
		return domain.LiveSession{}, domain.ErrLiveModel
	}
	if request.Translate {
		if s.translation == nil || s.translation.ModelID() == "" {
			return domain.LiveSession{}, domain.ErrLiveModel
		}
		if local, ok := s.translation.(interface{ Ready(context.Context) bool }); ok && !local.Ready(ctx) {
			return domain.LiveSession{}, domain.ErrLiveModel
		}
	}
	release, err := s.budget.ReserveLive()
	if err != nil {
		return domain.LiveSession{}, err
	}
	stamp := domain.PersistedUTC(time.Now())
	modelID := ""
	if request.Translate {
		modelID = s.translation.ModelID()
	}
	v := domain.LiveSession{ID: resourceID("live"), State: "preparing", CreatedAt: stamp, UpdatedAt: stamp, SourceLanguage: request.SourceLanguage, TargetLanguage: request.TargetLanguage, Translate: request.Translate, AutoDetect: request.AutoDetect, ModelID: modelID}
	if err = s.repository.CreateLive(ctx, v); err != nil {
		release()
		return domain.LiveSession{}, err
	}
	runCtx, cancel := context.WithCancel(s.ctx)
	w := &liveRun{session: v, request: request, ctx: runCtx, cancel: cancel, control: make(chan string, 4), done: make(chan struct{}), release: release}
	s.active = w
	s.snapshot = domain.LiveSnapshot{Session: v, Segments: []domain.LiveSegment{}, Revision: s.snapshot.Revision + 1}
	go s.run(w)
	return v, nil
}
func (s *LiveService) Control(ctx context.Context, id, command string) error {
	if command != "pause" && command != "resume" && command != "stop" {
		return domain.ErrLiveInput
	}
	s.mu.Lock()
	w := s.active
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return domain.ErrLiveClosed
	}
	if w == nil || w.session.ID != id {
		return domain.ErrConflict
	}
	select {
	case w.control <- command:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-w.done:
		return domain.ErrConflict
	default:
		return domain.ErrLiveBusy
	}
}
func (s *LiveService) Snapshot() domain.LiveSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.snapshot
	v.Segments = append([]domain.LiveSegment(nil), v.Segments...)
	if v.Partial != nil {
		copy := *v.Partial
		v.Partial = &copy
	}
	return v
}
func (s *LiveService) History(ctx context.Context, offset int) ([]domain.LiveSession, error) {
	return s.repository.LiveSessions(ctx, offset)
}
func (s *LiveService) Segments(ctx context.Context, id string, after int64) ([]domain.LiveSegment, error) {
	return s.repository.LiveSegments(ctx, id, after, domain.LiveVisibleSegments)
}
func (s *LiveService) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return domain.ErrLiveClosed
	}
	if s.active != nil && s.active.session.ID == id || s.retrying {
		return domain.ErrLiveBusy
	}
	if err := s.repository.DeleteLive(ctx, id); err != nil {
		return err
	}
	if s.snapshot.Session.ID == id {
		s.snapshot = domain.LiveSnapshot{Revision: s.snapshot.Revision + 1, Segments: []domain.LiveSegment{}}
	}
	return nil
}
func (s *LiveService) Edit(ctx context.Context, v domain.LiveSegment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return domain.ErrLiveClosed
	}
	if s.retrying || (s.active != nil && s.active.session.ID == v.SessionID) {
		return domain.ErrLiveBusy
	}
	session, err := s.repository.LiveSession(ctx, v.SessionID)
	if err != nil {
		return err
	}
	if domain.LiveActive(session.State) {
		return domain.ErrLiveBusy
	}
	v.Text = strings.TrimSpace(v.Text)
	v.Translation = strings.TrimSpace(v.Translation)
	v.TranslationState = "disabled"
	if session.Translate {
		v.TranslationState = "pending"
		if v.Translation != "" {
			v.TranslationState = "completed"
		}
	} else {
		v.Translation = ""
	}
	if v.Validate() != nil {
		return domain.ErrLiveInput
	}
	if err := s.repository.EditLiveSegment(ctx, v); err != nil {
		return err
	}
	if s.snapshot.Session.ID == v.SessionID {
		for index, old := range s.snapshot.Segments {
			if old.ID == v.ID {
				v.Revision++
				s.snapshot.Segments[index] = v
				s.snapshot.Revision++
				break
			}
		}
	}
	return nil
}

func (s *LiveService) setState(w *liveRun, state, code string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stamp := domain.PersistedUTC(time.Now())
	if err := s.repository.SetLiveState(ctx, w.session.ID, state, code, stamp); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == w {
		s.snapshot.Session.State = state
		s.snapshot.Session.UpdatedAt = stamp
		s.snapshot.Session.ErrorCode = code
		s.snapshot.Revision++
	}
	return nil
}

func (s *LiveService) run(w *liveRun) {
	defer func() {
		w.cancel()
		w.release()
		s.mu.Lock()
		if s.active == w {
			s.active = nil
			s.snapshot.Level = 0
			s.snapshot.Partial = nil
			s.snapshot.Revision++
		}
		s.mu.Unlock()
		close(w.done)
	}()
	frames := make(chan domain.LiveAudio, 25)
	events := make(chan domain.LiveRecognitionEvent, 64)
	asrDone := make(chan error, 1)
	go func() {
		asrDone <- s.asr.Run(w.ctx, frames, func(e domain.LiveRecognitionEvent) error {
			select {
			case events <- e:
				return nil
			case <-w.ctx.Done():
				return w.ctx.Err()
			}
		})
	}()
	wake := make(chan struct{}, 1)
	recognitionDone := make(chan struct{})
	translated := make(chan struct{})
	translationResult := make(chan error, 1)
	translationCtx, cancelTranslation := context.WithCancel(w.ctx)
	go func() {
		defer close(translated)
		if w.request.Translate {
			translationResult <- s.translatePending(translationCtx, w.session, wake, recognitionDone)
		} else {
			translationResult <- nil
		}
	}()
	var captureCancel context.CancelFunc
	var captureDone chan error
	var audio chan []byte
	var offset int64
	var apply func(domain.LiveRecognitionEvent) error
	startCapture := func() {
		captureCtx, cancel := context.WithCancel(w.ctx)
		captureCancel = cancel
		audio = make(chan []byte, 25)
		captureDone = make(chan error, 1)
		currentAudio, currentDone := audio, captureDone
		go func() {
			currentDone <- s.capture.Capture(captureCtx, w.request.DeviceID, func(pcm []byte) error {
				if len(pcm) == 0 || len(pcm) > domain.LiveFrameBytes || len(pcm)%32 != 0 {
					return domain.ErrLiveProtocol
				}
				select {
				case currentAudio <- append([]byte(nil), pcm...):
					return nil
				case <-captureCtx.Done():
					return captureCtx.Err()
				default:
					return domain.ErrLiveBackpressure
				}
			})
		}()
	}
	stopCapture := func(drain bool) error {
		if captureCancel != nil {
			captureCancel()
			captureErr := <-captureDone
			captureCancel = nil
			captureDone = nil
			buffered := audio
			audio = nil
			if drain {
				ctx, cancel := context.WithTimeout(w.ctx, 5*time.Second)
				defer cancel()
				for len(buffered) > 0 {
					pcm := <-buffered
					for sent := false; !sent; {
						select {
						case frames <- domain.LiveAudio{Kind: "audio", OffsetMS: offset, PCM: pcm}:
							offset += int64(len(pcm) / 32)
							sent = true
						case event := <-events:
							if err := apply(event); err != nil {
								return err
							}
						case <-ctx.Done():
							return domain.ErrLiveBackpressure
						}
					}
				}
			}
			if captureErr != nil && !errors.Is(captureErr, context.Canceled) {
				return captureErr
			}
		}
		return nil
	}
	var started time.Time
	ready, paused, stopping, asrJoined := false, false, false, false
	var failure error
	var flushTimer *time.Timer
	var flushTimeout <-chan time.Time
	send := func(kind string, pcm []byte) error {
		// A temporarily full ASR input is not necessarily an overload: the
		// capture queue still has bounded capacity. Keep draining ASR events
		// while waiting, otherwise a burst can deadlock the two directions.
		ctx, cancel := context.WithTimeout(w.ctx, 5*time.Second)
		defer cancel()
		for {
			select {
			case frames <- domain.LiveAudio{Kind: kind, OffsetMS: offset, PCM: pcm}:
				return nil
			case event := <-events:
				if err := apply(event); err != nil {
					return err
				}
			case err := <-captureDone:
				captureDone, captureCancel, audio = nil, nil, nil
				if err == nil || errors.Is(err, context.Canceled) {
					return domain.ErrLiveCapture
				}
				return err
			case <-ctx.Done():
				if w.ctx.Err() != nil {
					return w.ctx.Err()
				}
				return domain.ErrLiveBackpressure
			}
		}
	}
	apply = func(e domain.LiveRecognitionEvent) error {
		switch e.Type {
		case "ready":
			ready = true
			if stopping {
				return nil
			}
			started = time.Now()
			if err := s.setState(w, "listening", ""); err != nil {
				return err
			}
			startCapture()
		case "meter":
			s.mu.Lock()
			if !paused && !stopping {
				s.snapshot.Level = e.Level
			}
			s.snapshot.ProcessingMS = e.ProcessingMS
			s.snapshot.Revision++
			s.mu.Unlock()
		case "partial", "final":
			v := domain.LiveSegment{SessionID: w.session.ID, ID: e.SegmentID, Revision: e.Revision, StartMS: e.StartMS, EndMS: e.EndMS, Text: e.Text, TranslationState: "disabled"}
			if v.Validate() != nil {
				return domain.ErrLiveProtocol
			}
			if w.request.Translate {
				v.TranslationState = "pending"
			}
			s.mu.Lock()
			lastID := int64(0)
			if n := len(s.snapshot.Segments); n > 0 {
				lastID = s.snapshot.Segments[n-1].ID
			}
			partial := s.snapshot.Partial
			invalid := v.ID <= lastID || (partial != nil && (v.ID != partial.ID || v.Revision <= partial.Revision))
			s.mu.Unlock()
			if invalid {
				return domain.ErrLiveProtocol
			}
			if e.Type == "final" {
				if err := s.repository.SaveLiveSegment(w.ctx, v); err != nil {
					return err
				}
			}
			s.mu.Lock()
			if e.Type == "partial" {
				s.snapshot.Partial = &v
			} else {
				s.snapshot.Partial = nil
				s.snapshot.Session.SegmentCount++
				s.snapshot.Segments = append(s.snapshot.Segments, v)
				if len(s.snapshot.Segments) > domain.LiveVisibleSegments {
					s.snapshot.Segments = append([]domain.LiveSegment(nil), s.snapshot.Segments[len(s.snapshot.Segments)-domain.LiveVisibleSegments:]...)
				}
				if w.request.Translate {
					s.snapshot.TranslationPending++
				}
			}
			s.snapshot.Revision++
			s.mu.Unlock()
			if e.Type == "final" && w.request.Translate {
				select {
				case wake <- struct{}{}:
				default:
				}
			}
		}
		return nil
	}
loop:
	for {
		select {
		case <-w.ctx.Done():
			failure = w.ctx.Err()
			break loop
		case <-flushTimeout:
			failure = domain.ErrLiveProtocol
			break loop
		case command := <-w.control:
			if stopping {
				continue
			}
			switch command {
			case "pause":
				if ready && !paused {
					failure = stopCapture(true)
					if failure == nil {
						failure = send("boundary", nil)
					}
					if failure == nil {
						failure = s.setState(w, "paused", "")
					}
					paused = true
					s.mu.Lock()
					s.snapshot.Level = 0
					s.mu.Unlock()
				}
			case "resume":
				if ready && paused {
					offset = max(offset, time.Since(started).Milliseconds())
					failure = s.setState(w, "listening", "")
					if failure == nil {
						startCapture()
						paused = false
					}
				}
			case "stop":
				stopping = true
				failure = stopCapture(true)
				if err := s.setState(w, "stopping", ""); failure == nil {
					failure = err
				}
				if !ready {
					w.cancel()
					break loop
				}
				if failure == nil {
					failure = send("stop", nil)
				}
				flushTimer = time.NewTimer(10 * time.Second)
				flushTimeout = flushTimer.C
			}
			if failure != nil {
				break loop
			}
		case pcm := <-audio:
			failure = send("audio", pcm)
			if failure != nil {
				break loop
			}
			offset += int64(len(pcm) / 32)
		case err := <-captureDone:
			captureDone = nil
			captureCancel = nil
			audio = nil
			if err == nil || errors.Is(err, context.Canceled) {
				err = domain.ErrLiveCapture
			}
			failure = err
			break loop
		case event := <-events:
			if failure = apply(event); failure != nil {
				break loop
			}
		case err := <-asrDone:
			asrJoined = true
			failure = err
			for len(events) > 0 {
				if e := apply(<-events); e != nil && failure == nil {
					failure = e
				}
			}
			if !stopping && failure == nil {
				failure = domain.ErrLiveProtocol
			}
			break loop
		}
	}
	if flushTimer != nil {
		flushTimer.Stop()
	}
	_ = stopCapture(false)
	if !asrJoined {
		w.cancel()
		<-asrDone
	}
	close(recognitionDone)
	if failure != nil {
		cancelTranslation()
	}
	select {
	case <-translated:
	case <-time.After(20 * time.Second):
		cancelTranslation()
		<-translated
	}
	cancelTranslation()
	state, code := "completed", ""
	if err := <-translationResult; err != nil {
		code = "live_translation_incomplete"
	}
	if failure != nil && !(stopping && errors.Is(failure, context.Canceled)) {
		state, code = "failed", liveErrorCode(failure)
		if errors.Is(failure, context.Canceled) {
			state, code = "interrupted", "live_session_interrupted"
		}
	}
	if stopping && failure != nil && errors.Is(failure, context.Canceled) && ready {
		state, code = "interrupted", "live_session_interrupted"
	}
	if !stopping && failure == nil {
		state, code = "failed", "live_asr_failed"
	}
	if err := s.setState(w, state, code); err != nil {
		s.mu.Lock()
		s.snapshot.Session.State = "failed"
		s.snapshot.Session.ErrorCode = "live_persistence_failed"
		s.snapshot.Revision++
		s.mu.Unlock()
	}
}

func liveErrorCode(err error) string {
	switch {
	case errors.Is(err, domain.ErrLiveBackpressure):
		return "live_audio_backpressure"
	case errors.Is(err, domain.ErrLiveCapture), errors.Is(err, domain.ErrLiveInput):
		return "live_audio_capture_failed"
	case errors.Is(err, domain.ErrLiveModel):
		return "live_model_not_ready"
	case errors.Is(err, domain.ErrLiveProtocol):
		return "live_protocol_invalid"
	}
	return "live_asr_failed"
}
func (s *LiveService) publishTranslation(v domain.LiveSegment, finished bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot.Session.ID != v.SessionID {
		return
	}
	for index, old := range s.snapshot.Segments {
		if old.ID == v.ID && old.Revision == v.Revision {
			s.snapshot.Segments[index] = v
			break
		}
	}
	if finished {
		s.snapshot.TranslationPending = max(0, s.snapshot.TranslationPending-1)
	}
	s.snapshot.Revision++
}
func (s *LiveService) translatePending(ctx context.Context, session domain.LiveSession, wake <-chan struct{}, finished <-chan struct{}) error {
	if local, ok := s.translation.(interface{ EndSession() }); ok {
		defer local.EndSession()
	}
	var after int64
	complete := false
	for {
		segments, err := s.repository.PendingLiveSegments(ctx, session.ID, after, 16)
		if err != nil {
			return err
		}
		if len(segments) == 0 {
			if complete {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-wake:
				continue
			case <-finished:
				complete = true
				continue
			}
		}
		for _, segment := range segments {
			started := time.Now()
			request := domain.TranslationRequest{SourceLanguage: session.SourceLanguage, TargetLanguage: session.TargetLanguage, Text: segment.Text}
			if session.AutoDetect {
				request.SourceLanguage, request.TargetLanguage = domain.LiveTranslationDirection(segment.Text)
			}
			text := segment.Text
			var translateErr error
			if request.SourceLanguage != "" {
				text, translateErr = s.translation.Translate(ctx, request, func(text string) { v := segment; v.Translation = text; s.publishTranslation(v, false) })
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			segment.TranslationMS = time.Since(started).Milliseconds()
			segment.TranslationState = "completed"
			segment.Translation = strings.TrimSpace(text)
			if translateErr != nil || segment.Translation == "" {
				segment.TranslationState = "failed"
				segment.Translation = ""
			}
			if err := s.repository.SaveLiveTranslation(ctx, segment); err != nil {
				return err
			}
			s.publishTranslation(segment, true)
			after = segment.ID

		}
	}
}
func (s *LiveService) RetryTranslations(ctx context.Context, id string) error {
	ctx, done, err := s.operation(ctx)
	if err != nil {
		return err
	}
	defer done()
	s.mu.Lock()
	if s.active != nil || s.retrying || s.preparing {
		s.mu.Unlock()
		return domain.ErrLiveBusy
	}
	s.retrying = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.retrying = false; s.mu.Unlock() }()
	v, err := s.repository.LiveSession(ctx, id)
	if err != nil {
		return err
	}
	if !v.Translate || domain.LiveActive(v.State) {
		return domain.ErrLiveInput
	}
	finished := make(chan struct{})
	close(finished)
	return s.translatePending(ctx, v, nil, finished)
}
func (s *LiveService) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	w := s.active
	s.mu.Unlock()
	if w != nil {
		select {
		case w.control <- "stop":
		default:
			w.cancel()
		}
		select {
		case <-w.done:
		case <-time.After(15 * time.Second):
			w.cancel()
			<-w.done
		}
	}
	s.cancel()
	s.operations.Wait()
	if closer, ok := s.translation.(interface{ Close() }); ok {
		closer.Close()
	}
}
