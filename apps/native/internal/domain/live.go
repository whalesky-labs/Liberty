package domain

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrLiveBusy         = errors.New("live_session_busy")
	ErrLiveClosed       = errors.New("live_session_closed")
	ErrLiveInput        = errors.New("live_input_invalid")
	ErrLiveProtocol     = errors.New("live_protocol_invalid")
	ErrLiveBackpressure = errors.New("live_audio_backpressure")
	ErrLiveCapture      = errors.New("live_audio_capture_failed")
	ErrLiveModel        = errors.New("live_model_not_ready")
	ErrLiveTranslation  = errors.New("live_translation_failed")
)

const (
	LiveSampleRate      = 16000
	LiveFrameBytes      = 3200 // 100 ms, mono signed 16-bit little-endian PCM.
	LiveVisibleSegments = 100
	LiveMaxTextBytes    = 8192
)

type AudioDevice struct{ ID, Name string }
type LiveRequest struct {
	DeviceID       string
	SourceLanguage string
	TargetLanguage string
	Translate      bool
	AutoDetect     bool
}

func (r LiveRequest) Validate() error {
	if r.DeviceID == "" || len(r.DeviceID) > 512 || strings.ContainsAny(r.DeviceID, "\x00\r\n") ||
		(r.SourceLanguage != "zh" && r.SourceLanguage != "en") ||
		(r.TargetLanguage != "zh" && r.TargetLanguage != "en") ||
		(r.Translate && r.SourceLanguage == r.TargetLanguage) {
		return ErrLiveInput
	}
	return nil
}

type LiveSession struct {
	ID, State, CreatedAt, UpdatedAt, ErrorCode string
	SourceLanguage, TargetLanguage, ModelID    string
	Translate                                  bool
	AutoDetect                                 bool
	SegmentCount                               int64
}

func LiveActive(state string) bool {
	switch state {
	case "preparing", "listening", "paused", "stopping":
		return true
	}
	return false
}

type LiveSegment struct {
	SessionID                           string
	ID                                  int64
	Revision                            uint64
	StartMS, EndMS                      int64
	Text, Translation, TranslationState string
	TranslationMS                       int64
}

func (s LiveSegment) Validate() error {
	if s.SessionID == "" || s.ID < 1 || s.Revision < 1 || s.StartMS < 0 || s.EndMS < s.StartMS ||
		s.EndMS > 24*60*60*1000 || len(s.Text) == 0 || len(s.Text) > LiveMaxTextBytes ||
		len(s.Translation) > LiveMaxTextBytes || !utf8.ValidString(s.Text) || !utf8.ValidString(s.Translation) {
		return ErrLiveProtocol
	}
	return nil
}

type LiveAudio struct {
	Kind     string // audio, boundary, stop
	OffsetMS int64
	PCM      []byte
}

type LiveRecognitionEvent struct {
	Protocol     string  `json:"protocol"`
	Version      int     `json:"version"`
	Sequence     uint64  `json:"sequence"`
	Type         string  `json:"type"`
	SegmentID    int64   `json:"segmentId,omitempty"`
	Revision     uint64  `json:"revision,omitempty"`
	StartMS      int64   `json:"startMs,omitempty"`
	EndMS        int64   `json:"endMs,omitempty"`
	Text         string  `json:"text,omitempty"`
	Level        float64 `json:"level,omitempty"`
	ProcessingMS int64   `json:"processingMs,omitempty"`
	Code         string  `json:"code,omitempty"`
}

type LiveSnapshot struct {
	Session            LiveSession
	Segments           []LiveSegment
	Partial            *LiveSegment
	Level              float64
	ProcessingMS       int64
	TranslationPending int
	Revision           uint64
}

type TranslationRequest struct {
	SourceLanguage, TargetLanguage string
	Text                           string
}

// LiveTranslationDirection routes bilingual ASR output without a language
// selector. Han text (including Chinese with English terms) goes to English;
// Latin-only speech goes to Chinese. Other scripts and nonlinguistic fragments
// are not guessed: callers retain the original instead of mistranslating it.
func LiveTranslationDirection(text string) (source, target string) {
	han, latin := false, false
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			han = true
		} else if unicode.Is(unicode.Latin, r) {
			latin = true
		} else if unicode.IsLetter(r) {
			return "", ""
		}
	}
	if han {
		return "zh", "en"
	}
	if latin {
		return "en", "zh"
	}
	return "", ""
}
