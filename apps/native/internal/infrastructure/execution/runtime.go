package execution

import (
	"context"
	"path/filepath"
	"runtime"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/runner"
)

// ManagedResolver resolves the currently selected, reconciled components at
// launch time. It cannot fall back to a missing or unvalidated system tool.
type ManagedResolver struct {
	Manager interface {
		Status(context.Context) (domain.RuntimeOverview, error)
	}
	ModelPaths interface {
		ResolveModels(context.Context, domain.RuntimeArtifact) (runner.ModelPaths, error)
	}
	Settings interface {
		RuntimeSettings(context.Context) (domain.RuntimeSettings, error)
	}
	Resources, Backend string
	MemoryBytes        func() uint64
}

func (r *ManagedResolver) Resolve(ctx context.Context) (Runtime, error) {
	status, err := r.Manager.Status(ctx)
	if err != nil {
		return Runtime{}, err
	}
	if status.Status == "unsupported" {
		return Runtime{}, domain.ErrRuntimeUnsupported
	}
	if !status.ShellReady || status.PythonPath == nil || status.FFmpegPath == nil || status.ModelsRoot == nil {
		return Runtime{}, domain.ErrRuntime
	}
	var models runner.ModelPaths
	if r.Backend == "funasr" {
		if r.ModelPaths == nil || status.Models.Artifact == nil || status.Models.Artifact.Path != *status.ModelsRoot {
			return Runtime{}, domain.ErrRuntime
		}
		models, err = r.ModelPaths.ResolveModels(ctx, *status.Models.Artifact)
		if err != nil {
			return Runtime{}, err
		}
	}
	settings, err := r.Settings.RuntimeSettings(ctx)
	if err != nil {
		return Runtime{}, err
	}
	memory := uint64(0)
	if r.MemoryBytes != nil {
		memory = r.MemoryBytes()
	}
	budget := domain.ResolveASRResourceBudget(settings.Concurrency, settings.Threads, uint32(runtime.NumCPU()), memory)
	return Runtime{Python: *status.PythonPath, FFmpeg: *status.FFmpegPath, Models: *status.ModelsRoot, ModelPaths: models, Script: filepath.Join(r.Resources, "runner.py"), Backend: r.Backend, Device: settings.Device, Threads: budget.ThreadsPerRunner, BatchSeconds: settings.BatchSeconds}, nil
}
