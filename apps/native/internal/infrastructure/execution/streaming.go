package execution

import (
	"context"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/runner"
)

type LiveModels interface {
	PrepareLiveModel(context.Context, string, func(string)) error
	ResolveLiveModel(context.Context) (string, error)
	LiveScript(context.Context, string) (string, error)
}
type Streaming struct {
	Runtime   RuntimeResolver
	Models    LiveModels
	Directory string
	Settings  interface {
		RuntimeSettings(context.Context) (domain.RuntimeSettings, error)
	}
}

func (s *Streaming) Prepare(ctx context.Context, progress func(string)) error {
	if _, err := s.Runtime.Resolve(ctx); err != nil {
		return err
	}
	settings, err := s.Settings.RuntimeSettings(ctx)
	if err != nil {
		return err
	}
	return s.Models.PrepareLiveModel(ctx, settings.DownloadSource, progress)
}
func (s *Streaming) Ready(ctx context.Context) bool {
	_, err := s.Models.ResolveLiveModel(ctx)
	return err == nil
}
func (s *Streaming) Run(ctx context.Context, frames <-chan domain.LiveAudio, emit func(domain.LiveRecognitionEvent) error) error {
	r, err := s.Runtime.Resolve(ctx)
	if err != nil {
		return err
	}
	model, err := s.Models.ResolveLiveModel(ctx)
	if err != nil {
		return domain.ErrLiveModel
	}
	script, err := s.Models.LiveScript(ctx, "live_runner.py")
	if err != nil {
		return domain.ErrLiveModel
	}
	return (runner.LiveCommand{Python: r.Python, Script: script, Model: model, VAD: r.ModelPaths.VAD, Directory: s.Directory, Threads: min(r.Threads, 2)}).Run(ctx, frames, emit)
}
