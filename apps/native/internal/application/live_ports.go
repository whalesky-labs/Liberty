package application

import (
	"context"
	"liberty.local/liberty/internal/domain"
)

type LiveRepository interface {
	CreateLive(context.Context, domain.LiveSession) error
	SetLiveState(context.Context, string, string, string, string) error
	SaveLiveSegment(context.Context, domain.LiveSegment) error
	SaveLiveTranslation(context.Context, domain.LiveSegment) error
	EditLiveSegment(context.Context, domain.LiveSegment) error
	LiveSession(context.Context, string) (domain.LiveSession, error)
	LiveSessions(context.Context, int) ([]domain.LiveSession, error)
	LiveSegments(context.Context, string, int64, int) ([]domain.LiveSegment, error)
	PendingLiveSegments(context.Context, string, int64, int) ([]domain.LiveSegment, error)
	RecoverLive(context.Context, string) error
	DeleteLive(context.Context, string) error
}

type LiveCapture interface {
	Devices(context.Context) ([]domain.AudioDevice, error)
	Capture(context.Context, string, func([]byte) error) error
}

type StreamingASR interface {
	Prepare(context.Context, func(string)) error
	Ready(context.Context) bool
	Run(context.Context, <-chan domain.LiveAudio, func(domain.LiveRecognitionEvent) error) error
}

type TranslationProvider interface {
	ModelID() string
	Translate(context.Context, domain.TranslationRequest, func(string)) (string, error)
}

type LiveBudget interface {
	ReserveLive() (func(), error)
}
