package execution

import (
	"context"
	"sync"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/managed"
	"liberty.local/liberty/internal/infrastructure/runner"
)

type LocalTranslator struct {
	Runtime   RuntimeResolver
	Installer *managed.Installer
	Directory string
	mu        sync.Mutex
	workers   map[string]*runner.TranslationWorker
	closed    bool
}

func (t *LocalTranslator) ModelID() string {
	if t.Installer.Manifest.LiveTranslation == nil {
		return ""
	}
	model := t.Installer.Manifest.LiveTranslation
	if model.Reverse == nil || len(model.Reverse.Archive.SHA256) < 12 {
		return ""
	}
	return model.Version + "+reverse-" + model.Reverse.Archive.SHA256[:12]
}

func (t *LocalTranslator) Prepare(ctx context.Context, progress func(string)) error {
	if _, err := t.Runtime.Resolve(ctx); err != nil {
		return err
	}
	return t.Installer.PrepareTranslation(ctx, progress)
}
func (t *LocalTranslator) Ready(ctx context.Context) bool {
	for _, direction := range [][2]string{{"zh", "en"}, {"en", "zh"}} {
		if _, err := t.Installer.ResolveTranslationDirection(ctx, direction[0], direction[1]); err != nil {
			return false
		}
	}
	return true
}
func (t *LocalTranslator) EndSession() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, worker := range t.workers {
		worker.Close()
	}
	t.workers = nil
}
func (t *LocalTranslator) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	for _, worker := range t.workers {
		worker.Close()
	}
	t.workers = nil
}
func (t *LocalTranslator) Translate(ctx context.Context, input domain.TranslationRequest, delta func(string)) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return "", domain.ErrLiveClosed
	}
	if !((input.SourceLanguage == "zh" && input.TargetLanguage == "en") || (input.SourceLanguage == "en" && input.TargetLanguage == "zh")) {
		return "", domain.ErrLiveInput
	}
	key := input.SourceLanguage
	if t.workers == nil {
		t.workers = map[string]*runner.TranslationWorker{}
	}
	if t.workers[key] == nil {
		r, err := t.Runtime.Resolve(ctx)
		if err != nil {
			return "", err
		}
		paths, err := t.Installer.ResolveTranslationDirection(ctx, input.SourceLanguage, input.TargetLanguage)
		if err != nil {
			return "", domain.ErrLiveModel
		}
		script, err := t.Installer.LiveScript(ctx, "translation_runner.py")
		if err != nil {
			return "", domain.ErrLiveModel
		}
		worker, err := (runner.TranslationCommand{Python: r.Python, Script: script, Engine: paths.Engine, Model: paths.Model, Directory: t.Directory}).Start(ctx)
		if err != nil {
			return "", err
		}
		t.workers[key] = worker
	}
	text, err := t.workers[key].Translate(ctx, input.Text)
	if err != nil {
		t.workers[key].Close()
		delete(t.workers, key)
		return "", err
	}
	if delta != nil {
		delta(text)
	}
	return text, nil
}
