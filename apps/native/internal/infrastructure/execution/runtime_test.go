package execution

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/runner"
)

type runtimeStatusStub struct{ status domain.RuntimeOverview }

func (stub runtimeStatusStub) Status(context.Context) (domain.RuntimeOverview, error) {
	return stub.status, nil
}

type runtimeSettingsStub struct{ settings domain.RuntimeSettings }

func (stub runtimeSettingsStub) RuntimeSettings(context.Context) (domain.RuntimeSettings, error) {
	return stub.settings, nil
}

type runtimeModelsStub struct {
	selected domain.RuntimeArtifact
	paths    runner.ModelPaths
	err      error
	calls    int
}

func (stub *runtimeModelsStub) ResolveModels(_ context.Context, artifact domain.RuntimeArtifact) (runner.ModelPaths, error) {
	stub.selected = artifact
	stub.calls++
	return stub.paths, stub.err
}

func TestManagedResolverCarriesSelectedModelPaths(t *testing.T) {
	directory := t.TempDir()
	python := filepath.Join(directory, "python")
	ffmpeg := filepath.Join(directory, "ffmpeg")
	modelRoot := filepath.Join(directory, "selected-models")
	artifact := domain.RuntimeArtifact{GenerationID: "7", Version: "synthetic-pinned-models", Path: modelRoot}
	status := domain.RuntimeOverview{
		ShellReady: true, PythonPath: &python, FFmpegPath: &ffmpeg, ModelsRoot: &modelRoot,
		Models: domain.RuntimeComponent{Component: "model", Availability: "ready", Artifact: &artifact},
	}
	paths := runner.ModelPaths{Profile: "paraformer", ASR: "asr", VAD: "vad", Punctuation: "punc", Speaker: "speaker"}
	models := &runtimeModelsStub{paths: paths}
	resolver := ManagedResolver{
		Manager: runtimeStatusStub{status}, ModelPaths: models,
		Settings: runtimeSettingsStub{domain.DefaultRuntimeSettings()}, Resources: directory, Backend: "funasr",
	}
	resolved, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if models.calls != 1 || models.selected != artifact || resolved.ModelPaths != paths || resolved.Models != modelRoot || resolved.Script != filepath.Join(directory, "runner.py") {
		t.Fatal("selected model artifact was not carried into runner runtime")
	}
	injected := errors.New("synthetic_model_resolution_failed")
	models.err = injected
	if _, err := resolver.Resolve(context.Background()); !errors.Is(err, injected) {
		t.Fatal("model resolution failure fell back to aliases", err)
	}
	resolver.ModelPaths = nil
	if _, err := resolver.Resolve(context.Background()); !errors.Is(err, domain.ErrRuntime) {
		t.Fatal("missing model resolver was accepted", err)
	}
	resolver.ModelPaths = models
	other := artifact
	other.Path = filepath.Join(directory, "different-models")
	status.Models.Artifact = &other
	resolver.Manager = runtimeStatusStub{status}
	if _, err := resolver.Resolve(context.Background()); !errors.Is(err, domain.ErrRuntime) {
		t.Fatal("inconsistent selected model root was accepted", err)
	}
}
