package application

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

// The in-memory port preserves generation CAS and returns value snapshots, as
// the SQLite port does. Tests drive operations through the public use case.
type runtimeMemory struct {
	mu       sync.Mutex
	settings domain.RuntimeSettings
	states   map[string]domain.RuntimeComponent
	writes   int
}

func newRuntimeMemory() *runtimeMemory {
	s := domain.DefaultRuntimeSettings()
	s.DownloadSource = "official"
	return &runtimeMemory{settings: s, states: map[string]domain.RuntimeComponent{}}
}
func runtimeCopy(s domain.RuntimeComponent) domain.RuntimeComponent {
	if s.Artifact != nil {
		a := *s.Artifact
		s.Artifact = &a
	}
	return s
}
func (m *runtimeMemory) RuntimeSettings(context.Context) (domain.RuntimeSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings, nil
}
func (m *runtimeMemory) RuntimeSource(_ context.Context, c, s string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c == "python" {
		m.settings.PythonSource = s
	} else {
		m.settings.FFmpegSource = s
	}
	return nil
}
func (m *runtimeMemory) RuntimeDownloadSource(_ context.Context, s string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings.DownloadSource = s
	return nil
}
func (m *runtimeMemory) component(c, s string) domain.RuntimeComponent {
	if v, ok := m.states[c+":"+s]; ok {
		return runtimeCopy(v)
	}
	return domain.NewRuntimeComponent(c, s, "0")
}
func (m *runtimeMemory) RuntimeComponent(_ context.Context, _, c, s string) (domain.RuntimeComponent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.component(c, s), nil
}
func (m *runtimeMemory) BeginRuntime(_ context.Context, _, c, s, k, p string, reuse bool) (domain.RuntimeComponent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := m.component(c, s)
	if !reuse || v.Operation.Generation == 0 {
		v.Operation.Generation++
	}
	v.Operation.Kind = k
	v.Operation.Phase = p
	v.Operation.LastError = nil
	m.states[c+":"+s] = runtimeCopy(v)
	return v, nil
}
func (m *runtimeMemory) UpdateRuntime(_ context.Context, _ string, v domain.RuntimeComponent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := v.Component + ":" + v.SourceName()
	if m.component(v.Component, v.SourceName()).Operation.Generation != v.Operation.Generation {
		return domain.ErrRuntimeStale
	}
	m.states[key] = runtimeCopy(v)
	m.writes++
	return nil
}
func (m *runtimeMemory) LegacyRuntime(context.Context, string) (domain.RuntimeOverview, error) {
	return domain.RuntimeOverview{}, nil
}
func (m *runtimeMemory) SaveRuntimeOverview(context.Context, domain.RuntimeOverview) error {
	return nil
}
func (m *runtimeMemory) put(v domain.RuntimeComponent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[v.Component+":"+v.SourceName()] = runtimeCopy(v)
}

type runtimeBackendFixture struct {
	definition RuntimeDefinition
	install    func(context.Context, string, int64) (domain.RuntimeArtifact, error)
	acquire    func(context.Context, int64) error
	validate   func(context.Context, int64, string) (domain.RuntimeArtifact, error)
	detect     func(context.Context, string) (string, error)
	mu         sync.Mutex
	acquired   map[int64]bool
}

func newRuntimeBackendFixture() *runtimeBackendFixture {
	return &runtimeBackendFixture{definition: RuntimeDefinition{Platform: "darwin-aarch64", Version: "fixture"}, acquired: map[int64]bool{}}
}
func (b *runtimeBackendFixture) Definition() RuntimeDefinition { return b.definition }
func (b *runtimeBackendFixture) Sources() []domain.RuntimeSourceOption {
	return []domain.RuntimeSourceOption{{ID: "official", Name: "Official"}}
}
func (b *runtimeBackendFixture) Log(string) (string, error) { return "", nil }
func (b *runtimeBackendFixture) CheckSource(s string) error {
	if s != "official" {
		return domain.ErrRuntimeSource
	}
	return nil
}
func (b *runtimeBackendFixture) Reconcile(s domain.RuntimeComponent, _ domain.RuntimeOverview, _ domain.RuntimeSettings) (domain.RuntimeComponent, error) {
	return s, nil
}
func (b *runtimeBackendFixture) InstallTool(ctx context.Context, c string, g int64, _ string, _ func(string) error) (domain.RuntimeArtifact, error) {
	return b.install(ctx, c, g)
}
func (b *runtimeBackendFixture) AcquireModels(ctx context.Context, g int64, _ string, _ func(string) error) error {
	if b.acquire != nil {
		if err := b.acquire(ctx, g); err != nil {
			return err
		}
	}
	b.mu.Lock()
	b.acquired[g] = true
	b.mu.Unlock()
	return nil
}
func (b *runtimeBackendFixture) ValidateModels(ctx context.Context, g int64, p string, _ func(string) error) (domain.RuntimeArtifact, error) {
	return b.validate(ctx, g, p)
}
func (b *runtimeBackendFixture) ModelsAcquired(g int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.acquired[g]
}
func (b *runtimeBackendFixture) ModelRetryable(g int64) bool { return b.ModelsAcquired(g) }
func (b *runtimeBackendFixture) Detect(ctx context.Context, c string, _ domain.RuntimeSettings, _ func(string) error) (string, error) {
	return b.detect(ctx, c)
}
func (b *runtimeBackendFixture) AppendLog(string, string) error { return nil }
func fixtureRuntimeArtifact(g int64) domain.RuntimeArtifact {
	return domain.RuntimeArtifact{GenerationID: strconv.FormatInt(g, 10), Version: "fixture", Path: "/synthetic/runtime"}
}
func readyRuntime(c string) domain.RuntimeComponent {
	s := domain.NewRuntimeComponent(c, "managed", "0")
	a := fixtureRuntimeArtifact(1)
	s.Availability = "ready"
	s.Artifact = &a
	return s
}
func awaitRuntime(t *testing.T, m *runtimeMemory, c, s string, predicate func(domain.RuntimeComponent) bool) domain.RuntimeComponent {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		v, err := m.RuntimeComponent(context.Background(), "", c, s)
		if err != nil {
			t.Fatal(err)
		}
		if predicate(v) {
			return v
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("runtime transition did not settle")
	return domain.RuntimeComponent{}
}
func TestRuntimeModelsWaitForPythonThenValidateSameGeneration(t *testing.T) {
	ctx := context.Background()
	store := newRuntimeMemory()
	backend := newRuntimeBackendFixture()
	backend.install = func(context.Context, string, int64) (domain.RuntimeArtifact, error) {
		return fixtureRuntimeArtifact(1), nil
	}
	validated := make(chan int64, 1)
	backend.validate = func(_ context.Context, g int64, p string) (domain.RuntimeArtifact, error) {
		if p != "/synthetic/runtime" {
			return domain.RuntimeArtifact{}, domain.ErrRuntime
		}
		validated <- g
		return fixtureRuntimeArtifact(g), nil
	}
	r := NewRuntimeManager(store, backend)
	defer r.Close()
	if err := r.Install(ctx, "model"); err != nil {
		t.Fatal(err)
	}
	waiting := awaitRuntime(t, store, "model", "managed", func(s domain.RuntimeComponent) bool { return s.Operation.Kind == "waiting_for_python" })
	if waiting.Ready() || waiting.Operation.Progress == nil || *waiting.Operation.Progress != 100 {
		t.Fatal("unvalidated model published")
	}
	if err := r.Install(ctx, "python"); err != nil {
		t.Fatal(err)
	}
	model := awaitRuntime(t, store, "model", "managed", func(s domain.RuntimeComponent) bool { return s.Ready() })
	if model.Operation.Generation != waiting.Operation.Generation {
		t.Fatal("download generation discarded while waiting")
	}
	select {
	case g := <-validated:
		if g != waiting.Operation.Generation {
			t.Fatal("validated other generation")
		}
	default:
		t.Fatal("model marked ready without validation")
	}
}
func TestRuntimeSourceSwitchWaitsForBusyManagedInstall(t *testing.T) {
	ctx := context.Background()
	store := newRuntimeMemory()
	backend := newRuntimeBackendFixture()
	started, release := make(chan struct{}), make(chan struct{})
	backend.install = func(ctx context.Context, _ string, g int64) (domain.RuntimeArtifact, error) {
		close(started)
		select {
		case <-release:
			return fixtureRuntimeArtifact(g), nil
		case <-ctx.Done():
			return domain.RuntimeArtifact{}, ctx.Err()
		}
	}
	detected := make(chan struct{}, 1)
	backend.detect = func(context.Context, string) (string, error) { detected <- struct{}{}; return "/synthetic/system", nil }
	r := NewRuntimeManager(store, backend)
	defer r.Close()
	if err := r.Install(ctx, "python"); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := r.SwitchSource(ctx, "python", "system"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-detected:
		t.Fatal("system detection overlapped managed work")
	default:
	}
	close(release)
	awaitRuntime(t, store, "python", "system", func(s domain.RuntimeComponent) bool { return s.Ready() })
	status, err := r.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.PythonPath == nil || *status.PythonPath != "/synthetic/system" {
		t.Fatal("source selection was lost")
	}
}
func TestRuntimeFailedReinstallKeepsActiveArtifact(t *testing.T) {
	store := newRuntimeMemory()
	backend := newRuntimeBackendFixture()
	for _, c := range []string{"python", "ffmpeg", "model"} {
		store.put(readyRuntime(c))
	}
	backend.install = func(context.Context, string, int64) (domain.RuntimeArtifact, error) {
		return domain.RuntimeArtifact{}, errors.New("synthetic_failure")
	}
	r := NewRuntimeManager(store, backend)
	defer r.Close()
	if err := r.Install(context.Background(), "ffmpeg"); err != nil {
		t.Fatal(err)
	}
	failed := awaitRuntime(t, store, "ffmpeg", "managed", func(s domain.RuntimeComponent) bool { return s.Operation.Kind == "failed" })
	if !failed.Ready() {
		t.Fatal("failed reinstall destroyed usable runtime")
	}
	status, err := r.Status(context.Background())
	if err != nil || !status.ShellReady || status.Status != "ready" {
		t.Fatal("aggregate ignored active runtime")
	}
}
func TestRuntimeRestartMarksInterruptedOnce(t *testing.T) {
	store := newRuntimeMemory()
	backend := newRuntimeBackendFixture()
	state := readyRuntime("python")
	state.Operation = domain.RuntimeOperation{Generation: 5, Kind: "installing", Phase: "installing_dependencies"}
	store.put(state)
	r := NewRuntimeManager(store, backend)
	defer r.Close()
	for range 3 {
		status, err := r.Status(context.Background())
		if err != nil || status.Python.Operation.Phase != "interrupted" || !status.Python.Ready() {
			t.Fatal("interrupted runtime not reconciled")
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.writes != 1 {
		t.Fatal("unchanged components rewritten on every poll")
	}
}
func TestRuntimeCloseJoinsAndPersistsInterruptedOperation(t *testing.T) {
	store := newRuntimeMemory()
	backend := newRuntimeBackendFixture()
	started := make(chan struct{})
	joined := make(chan struct{})
	backend.install = func(ctx context.Context, _ string, _ int64) (domain.RuntimeArtifact, error) {
		close(started)
		<-ctx.Done()
		close(joined)
		return domain.RuntimeArtifact{}, ctx.Err()
	}
	r := NewRuntimeManager(store, backend)
	if err := r.Install(context.Background(), "ffmpeg"); err != nil {
		t.Fatal(err)
	}
	<-started
	r.Close()
	select {
	case <-joined:
	default:
		t.Fatal("shutdown returned with live installer")
	}
	s, _ := store.RuntimeComponent(context.Background(), "", "ffmpeg", "managed")
	if s.Operation.Phase != "interrupted" {
		t.Fatal("shutdown lost retry state")
	}
	if err := r.Install(context.Background(), "python"); !errors.Is(err, ErrSchedulerClosed) {
		t.Fatal("closed manager accepted work")
	}
}
func TestRuntimeStaleCompletionCannotPublishNewGeneration(t *testing.T) {
	store := newRuntimeMemory()
	backend := newRuntimeBackendFixture()
	started, release := make(chan struct{}), make(chan struct{})
	backend.install = func(context.Context, string, int64) (domain.RuntimeArtifact, error) {
		close(started)
		<-release
		return fixtureRuntimeArtifact(1), nil
	}
	r := NewRuntimeManager(store, backend)
	if err := r.Install(context.Background(), "ffmpeg"); err != nil {
		t.Fatal(err)
	}
	<-started
	newer, _ := store.BeginRuntime(context.Background(), "", "ffmpeg", "managed", "failed", "interrupted", false)
	close(release)
	r.Close()
	state, _ := store.RuntimeComponent(context.Background(), "", "ffmpeg", "managed")
	if state.Operation.Generation != newer.Operation.Generation || state.Artifact != nil {
		t.Fatal("stale completion overwrote newer operation")
	}
}
func TestRuntimeRejectsUnselectedDownloadSourceAndUnsupportedPlatform(t *testing.T) {
	store := newRuntimeMemory()
	backend := newRuntimeBackendFixture()
	store.settings.DownloadSource = ""
	r := NewRuntimeManager(store, backend)
	defer r.Close()
	if err := r.Install(context.Background(), "python"); !errors.Is(err, domain.ErrRuntimeSource) {
		t.Fatal("download started before source selection")
	}
	backend.definition.UnsupportedReason = "synthetic unsupported target"
	if err := r.Install(context.Background(), "python"); !errors.Is(err, domain.ErrRuntimeUnsupported) {
		t.Fatal("unsupported target installation accepted")
	}
	status, err := r.Status(context.Background())
	if err != nil || status.Status != "unsupported" || status.ShellReady {
		t.Fatal("unsupported target reported ready")
	}
}
