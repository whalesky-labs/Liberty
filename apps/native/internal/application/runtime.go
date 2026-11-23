package application

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"sync"
	"time"

	"liberty.local/liberty/internal/domain"
)

type RuntimeDefinition struct{ Platform, Version, PythonVersion, ModelVersion, InstallRoot, UnsupportedReason string }
type RuntimeStore interface {
	RuntimeSettings(context.Context) (domain.RuntimeSettings, error)
	RuntimeSource(context.Context, string, string) error
	RuntimeDownloadSource(context.Context, string) error
	RuntimeComponent(context.Context, string, string, string) (domain.RuntimeComponent, error)
	BeginRuntime(context.Context, string, string, string, string, string, bool) (domain.RuntimeComponent, error)
	UpdateRuntime(context.Context, string, domain.RuntimeComponent) error
	LegacyRuntime(context.Context, string) (domain.RuntimeOverview, error)
	SaveRuntimeOverview(context.Context, domain.RuntimeOverview) error
}
type RuntimeBackend interface {
	Definition() RuntimeDefinition
	Sources() []domain.RuntimeSourceOption
	Log(string) (string, error)
	CheckSource(string) error
	Reconcile(domain.RuntimeComponent, domain.RuntimeOverview, domain.RuntimeSettings) (domain.RuntimeComponent, error)
	InstallTool(context.Context, string, int64, string, func(string) error) (domain.RuntimeArtifact, error)
	AcquireModels(context.Context, int64, string, func(string) error) error
	ValidateModels(context.Context, int64, string, func(string) error) (domain.RuntimeArtifact, error)
	ModelsAcquired(int64) bool
	ModelRetryable(int64) bool
	Detect(context.Context, string, domain.RuntimeSettings, func(string) error) (string, error)
	AppendLog(string, string) error
}

// RuntimeManager owns component operations independently of any view. All state
// transitions are serialized; the long download/process work runs outside mu.
type RuntimeManager struct {
	mu      sync.Mutex
	store   RuntimeStore
	backend RuntimeBackend
	ctx     context.Context
	cancel  context.CancelFunc
	active  map[string]bool
	closed  bool
	wg      sync.WaitGroup
}

func NewRuntimeManager(store RuntimeStore, backend RuntimeBackend) *RuntimeManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &RuntimeManager{store: store, backend: backend, ctx: ctx, cancel: cancel, active: map[string]bool{}}
}
func (r *RuntimeManager) Close() {
	r.mu.Lock()
	r.closed = true
	r.cancel()
	r.mu.Unlock()
	r.wg.Wait()
}
func runtimeNow() string { return strconv.FormatInt(time.Now().UnixMilli(), 10) }
func (r *RuntimeManager) Panel(ctx context.Context) (domain.RuntimePanel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	panel := domain.RuntimePanel{Sources: r.backend.Sources(), Logs: map[string]string{}}
	status, err := r.statusLocked(ctx)
	if err != nil {
		return panel, err
	}
	panel.Overview = status
	settings, err := r.store.RuntimeSettings(ctx)
	if err != nil {
		return panel, err
	}
	panel.DownloadSource = settings.DownloadSource
	for _, component := range []string{"python", "ffmpeg", "model"} {
		text, err := r.backend.Log(component)
		if err != nil {
			return panel, err
		}
		panel.Logs[component] = text
	}
	return panel, nil
}
func (r *RuntimeManager) Status(ctx context.Context) (domain.RuntimeOverview, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.statusLocked(ctx)
}
func (r *RuntimeManager) statusLocked(ctx context.Context) (domain.RuntimeOverview, error) {
	definition := r.backend.Definition()
	settings, err := r.store.RuntimeSettings(ctx)
	if err != nil {
		return domain.RuntimeOverview{}, err
	}
	legacy, err := r.store.LegacyRuntime(ctx, definition.Platform)
	if err != nil {
		return domain.RuntimeOverview{}, err
	}
	state := legacy
	state.PlatformID = definition.Platform
	state.RuntimeVersion = definition.Version
	state.PythonVersion = definition.PythonVersion
	state.InstallRoot = &definition.InstallRoot
	for _, component := range []string{"python", "ffmpeg", "model"} {
		value, err := r.store.RuntimeComponent(ctx, definition.Platform, component, settings.Source(component))
		if err != nil {
			return state, err
		}
		if definition.UnsupportedReason != "" {
			value.Availability = "unsupported"
			value.Artifact = nil
			value.Operation = domain.RuntimeOperation{Kind: "idle", Phase: "idle"}
		} else if !r.active[component] {
			previous := value
			value, err = r.backend.Reconcile(value, legacy, settings)
			if err != nil {
				return state, err
			}
			if value.Operation.Active() && value.Operation.Kind != "waiting_for_python" {
				reason := "runtime_operation_interrupted"
				value.Operation.Kind = "failed"
				value.Operation.Phase = "interrupted"
				value.Operation.Progress = nil
				value.Operation.LastError = &reason
			}
			if !reflect.DeepEqual(previous, value) {
				value.UpdatedAt = runtimeNow()
				if err := r.store.UpdateRuntime(ctx, definition.Platform, value); err != nil {
					return state, err
				}
			}
		}
		switch component {
		case "python":
			state.Python = value
		case "ffmpeg":
			state.FFmpeg = value
		case "model":
			state.Models = value
		}
	}
	state.Aggregate()
	state.UpdatedAt = runtimeNow()
	if definition.UnsupportedReason != "" {
		state.LastError = &definition.UnsupportedReason
		state.InstallRoot = nil
		state.InstalledAt = nil
		state.LastLogPath = nil
	}
	if err := r.store.SaveRuntimeOverview(ctx, state); err != nil {
		return state, err
	}
	if state.Models.Operation.Kind == "waiting_for_python" && state.Python.Ready() && !r.closed && !r.active["model"] {
		if err := r.beginLocked(ctx, "model", false); err != nil {
			return state, err
		}
	}
	return state, nil
}
func (r *RuntimeManager) SelectDownloadSource(ctx context.Context, source string) error {
	if err := r.backend.CheckSource(source); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.store.RuntimeDownloadSource(ctx, source)
}
func (r *RuntimeManager) SwitchSource(ctx context.Context, component, source string) error {
	if err := domain.RuntimeKey(component, source); err != nil {
		return err
	}
	if component == "model" {
		return domain.ErrRuntimeSource
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrSchedulerClosed
	}
	if err := r.store.RuntimeSource(ctx, component, source); err != nil {
		return err
	}
	if source == "system" {
		return r.beginLocked(ctx, component, true)
	}
	_, err := r.statusLocked(ctx)
	return err
}
func (r *RuntimeManager) Install(ctx context.Context, component string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.beginLocked(ctx, component, false)
}
func (r *RuntimeManager) Detect(ctx context.Context, component string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.beginLocked(ctx, component, true)
}
func (r *RuntimeManager) InstallAll(ctx context.Context) error {
	settings, err := r.store.RuntimeSettings(ctx)
	if err != nil {
		return err
	}
	for _, component := range []string{"python", "model", "ffmpeg"} {
		if settings.Source(component) == "managed" {
			if err := r.Install(ctx, component); err != nil {
				return err
			}
		}
	}
	return nil
}
func (r *RuntimeManager) beginLocked(ctx context.Context, component string, detect bool) error {
	if r.closed {
		return ErrSchedulerClosed
	}
	source := "managed"
	if detect {
		source = "system"
	}
	if err := domain.RuntimeKey(component, source); err != nil {
		return err
	}
	definition := r.backend.Definition()
	if definition.UnsupportedReason != "" {
		return domain.ErrRuntimeUnsupported
	}
	if r.active[component] {
		return nil
	}
	settings, err := r.store.RuntimeSettings(ctx)
	if err != nil {
		return err
	}
	if !detect && settings.Source(component) != "managed" {
		return domain.ErrRuntimeSource
	}
	current, err := r.store.RuntimeComponent(ctx, definition.Platform, component, source)
	if err != nil {
		return err
	}
	reuse, acquired := false, false
	if component == "model" {
		reuse = current.Operation.Kind == "waiting_for_python" || (current.Operation.Kind == "failed" && r.backend.ModelRetryable(current.Operation.Generation))
		if reuse {
			acquired = r.backend.ModelsAcquired(current.Operation.Generation)
		}
	}
	if !detect && !acquired {
		if err := r.backend.CheckSource(settings.DownloadSource); err != nil {
			return err
		}
	}
	kind, phase := "downloading", "downloading"
	if detect {
		kind, phase = "detecting", "scanning_system"
	} else if component == "model" {
		kind, phase = "downloading", "acquiring_models"
		if acquired {
			python, err := r.store.RuntimeComponent(ctx, definition.Platform, "python", settings.PythonSource)
			if err != nil {
				return err
			}
			if python.Ready() {
				kind, phase = "validating", "validating_models"
			} else {
				kind, phase = "waiting_for_python", "waiting_for_python"
			}
		}
	} else {
		kind, phase = "installing", "installing_"+component
	}
	state, err := r.store.BeginRuntime(ctx, definition.Platform, component, source, kind, phase, reuse)
	if err != nil {
		return err
	}
	if kind == "waiting_for_python" {
		value := uint32(100)
		state.Operation.Progress = &value
		return r.store.UpdateRuntime(ctx, definition.Platform, state)
	}
	r.active[component] = true
	r.wg.Add(1)
	go r.perform(state, settings, detect, acquired)
	return nil
}
func (r *RuntimeManager) perform(state domain.RuntimeComponent, settings domain.RuntimeSettings, detect, acquired bool) {
	defer r.wg.Done()
	log := func(text string) error { return r.backend.AppendLog(state.Component, text) }
	artifact, waiting, err := r.work(state, settings, detect, acquired, log)
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, state.Component)
	finalize, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	definition := r.backend.Definition()
	current, readErr := r.store.RuntimeComponent(finalize, definition.Platform, state.Component, state.SourceName())
	if readErr != nil || current.Operation.Generation != state.Operation.Generation {
		return
	}
	state = current
	if err != nil {
		reason := "runtime_install_failed"
		phase := "install_failed"
		if detect {
			reason, phase = "runtime_system_not_detected", "system_not_detected"
			state.Availability = "unavailable"
			state.Artifact = nil
		}
		if state.Component == "model" {
			if acquired {
				phase = "model_validation_failed"
			} else {
				phase = "model_acquisition_failed"
			}
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			reason, phase = "runtime_operation_interrupted", "interrupted"
		}
		state.Operation.Kind = "failed"
		state.Operation.Phase = phase
		state.Operation.Progress = nil
		state.Operation.LastError = &reason
	} else if waiting {
		value := uint32(100)
		state.Operation.Kind = "waiting_for_python"
		state.Operation.Phase = "waiting_for_python"
		state.Operation.Progress = &value
		state.Operation.LastError = nil
	} else {
		value := uint32(100)
		state.Availability = "ready"
		state.Artifact = &artifact
		state.Operation.Kind = "idle"
		state.Operation.Phase = "ready"
		state.Operation.Progress = &value
		state.Operation.LastError = nil
	}
	state.UpdatedAt = runtimeNow()
	if r.store.UpdateRuntime(finalize, definition.Platform, state) != nil {
		return
	}
	// A source switch may arrive while the previous source is still busy. Run
	// the deferred system detection after that operation has fully joined.
	if !r.closed && state.Component != "model" && state.SourceName() == "managed" {
		if selected, err := r.store.RuntimeSettings(finalize); err == nil && selected.Source(state.Component) == "system" {
			_ = r.beginLocked(finalize, state.Component, true)
		}
	}
	if !r.closed && (state.Component == "python" || state.Component == "model") {
		models, err := r.store.RuntimeComponent(finalize, definition.Platform, "model", "managed")
		if err == nil && models.Operation.Kind == "waiting_for_python" {
			_ = r.beginLocked(finalize, "model", false)
		}
	}
}
func (r *RuntimeManager) work(state domain.RuntimeComponent, settings domain.RuntimeSettings, detect, acquired bool, log func(string) error) (domain.RuntimeArtifact, bool, error) {
	if detect {
		path, err := r.backend.Detect(r.ctx, state.Component, settings, log)
		return domain.RuntimeArtifact{GenerationID: "system-" + strconv.FormatInt(state.Operation.Generation, 10), Version: "system", Path: path}, false, err
	}
	if state.Component != "model" {
		artifact, err := r.backend.InstallTool(r.ctx, state.Component, state.Operation.Generation, settings.DownloadSource, log)
		return artifact, false, err
	}
	if !acquired {
		if err := r.backend.AcquireModels(r.ctx, state.Operation.Generation, settings.DownloadSource, log); err != nil {
			return domain.RuntimeArtifact{}, false, err
		}
	}
	settings, err := r.store.RuntimeSettings(r.ctx)
	if err != nil {
		return domain.RuntimeArtifact{}, false, err
	}
	python, err := r.store.RuntimeComponent(r.ctx, r.backend.Definition().Platform, "python", settings.PythonSource)
	if err != nil {
		return domain.RuntimeArtifact{}, false, err
	}
	if !python.Ready() {
		return domain.RuntimeArtifact{}, true, nil
	}
	r.mu.Lock()
	current, err := r.store.RuntimeComponent(r.ctx, r.backend.Definition().Platform, "model", "managed")
	if err == nil && current.Operation.Generation != state.Operation.Generation {
		err = domain.ErrRuntimeStale
	}
	if err == nil {
		current.Operation.Kind = "validating"
		current.Operation.Phase = "validating_models"
		err = r.store.UpdateRuntime(r.ctx, r.backend.Definition().Platform, current)
	}
	r.mu.Unlock()
	if err != nil {
		return domain.RuntimeArtifact{}, false, err
	}
	artifact, err := r.backend.ValidateModels(r.ctx, state.Operation.Generation, python.Artifact.Path, log)
	return artifact, false, err
}
