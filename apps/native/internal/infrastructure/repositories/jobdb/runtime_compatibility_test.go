//go:build compatibility

package jobdb

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"liberty.local/liberty/internal/domain"
)

func runtimeRevision(t *testing.T, s *Store) int64 {
	t.Helper()
	var revision int64
	if s.db.QueryRow(`SELECT settings_revision FROM app_settings WHERE id=1`).Scan(&revision) != nil {
		t.Fatal("settings revision unavailable")
	}
	return revision
}
func TestWriteRuntimeSourcesAndGenerationCASOnRustSchema(t *testing.T) {
	ctx := context.Background()
	s, path := fixtureStore(t)
	// Any accidental rewrite of credential fields fails this transaction. The
	// runtime settings port neither needs nor resolves those values.
	if _, err := s.db.Exec(`CREATE TRIGGER protect_runtime_credentials BEFORE UPDATE OF api_token,api_token_ref ON app_settings BEGIN SELECT RAISE(ABORT,'credential_write_forbidden'); END`); err != nil {
		t.Fatal(err)
	}
	before, err := s.RuntimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RuntimeDownloadSource(ctx, "official"); err != nil {
		t.Fatal(err)
	}
	base := runtimeRevision(t, s)
	if err := s.RuntimeSource(ctx, "python", "system"); err != nil {
		t.Fatal(err)
	}
	if runtimeRevision(t, s) != base+1 {
		t.Fatal("source change did not invalidate stale settings editor")
	}
	one, err := s.BeginRuntime(ctx, "darwin-aarch64", "python", "system", "detecting", "scanning_system", false)
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.BeginRuntime(ctx, "darwin-aarch64", "python", "system", "detecting", "scanning_system", false)
	if err != nil {
		t.Fatal(err)
	}
	one.Availability = "ready"
	one.Artifact = &domain.RuntimeArtifact{GenerationID: "system-stale", Version: "system", Path: "/synthetic/stale"}
	if err := s.UpdateRuntime(ctx, "darwin-aarch64", one); !errors.Is(err, domain.ErrRuntimeStale) {
		t.Fatal("stale detection overwrote source path")
	}
	two.Availability = "ready"
	two.Artifact = &domain.RuntimeArtifact{GenerationID: "system-current", Version: "system", Path: "/synthetic/python"}
	two.Operation.Kind = "idle"
	two.Operation.Phase = "ready"
	if err := s.UpdateRuntime(ctx, "darwin-aarch64", two); err != nil {
		t.Fatal(err)
	}
	after, err := s.RuntimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.PythonSource != "system" || after.PythonPath != "/synthetic/python" || runtimeRevision(t, s) != base+2 {
		t.Fatal("detected executable not atomically published")
	}
	if after.FFmpegSource != before.FFmpegSource || after.FFmpegPath != before.FFmpegPath || after.Device != before.Device {
		t.Fatal("unrelated settings changed")
	}
	if err := s.UpdateRuntime(ctx, "darwin-aarch64", two); err != nil {
		t.Fatal(err)
	}
	if runtimeRevision(t, s) != base+2 {
		t.Fatal("status refresh invalidated settings editor")
	}
	if err := s.RuntimeSource(ctx, "python", "managed"); err != nil {
		t.Fatal(err)
	}
	two.Artifact.Path = "/synthetic/new-system-python"
	if err := s.UpdateRuntime(ctx, "darwin-aarch64", two); err != nil {
		t.Fatal(err)
	}
	after, err = s.RuntimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.PythonPath != "" || after.PythonSource != "managed" || runtimeRevision(t, s) != base+3 {
		t.Fatal("late detection overrode chosen managed source")
	}
	if err := s.RuntimeDownloadSource(ctx, "nju"); err != nil {
		t.Fatal(err)
	}
	if runtimeRevision(t, s) != base+4 {
		t.Fatal("download source failed to invalidate stale settings")
	}
	s.Close()
	reopened, err := openCompatibility(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	persisted, err := reopened.RuntimeComponent(ctx, "darwin-aarch64", "python", "system")
	if err != nil || !reflect.DeepEqual(persisted, two) {
		t.Fatal("component snapshot changed after restart")
	}
}
func TestWriteRuntimeModelRetryAndOverviewPersistOnRustSchema(t *testing.T) {
	ctx := context.Background()
	s, _ := fixtureStore(t)
	m, err := s.BeginRuntime(ctx, "darwin-aarch64", "model", "managed", "downloading", "acquiring_models", false)
	if err != nil {
		t.Fatal(err)
	}
	m.Operation.Kind = "waiting_for_python"
	m.Operation.Phase = "waiting_for_python"
	if err := s.UpdateRuntime(ctx, "darwin-aarch64", m); err != nil {
		t.Fatal(err)
	}
	resumed, err := s.BeginRuntime(ctx, "darwin-aarch64", "model", "managed", "validating", "validating_models", true)
	if err != nil || resumed.Operation.Generation != m.Operation.Generation {
		t.Fatal("waiting model lost downloaded generation")
	}
	resumed.Availability = "ready"
	resumed.Artifact = &domain.RuntimeArtifact{GenerationID: "1", Version: "synthetic-model-version", Path: "/synthetic/models"}
	resumed.Operation.Kind = "idle"
	if err := s.UpdateRuntime(ctx, "darwin-aarch64", resumed); err != nil {
		t.Fatal(err)
	}
	state := domain.RuntimeOverview{PlatformID: "darwin-aarch64", RuntimeVersion: "synthetic-runtime", PythonVersion: "3.10.17", Status: "missing", ModelsRoot: &resumed.Artifact.Path, UpdatedAt: "1700000000000"}
	if err := s.SaveRuntimeOverview(ctx, state); err != nil {
		t.Fatal(err)
	}
	actual, err := s.LegacyRuntime(ctx, "darwin-aarch64")
	if err != nil || !reflect.DeepEqual(actual, state) {
		t.Fatal("legacy runtime projection no longer compatible")
	}
	if _, err := s.db.Exec(`UPDATE runtime_component_state SET operation_generation=? WHERE component='model'`, int64(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginRuntime(ctx, "darwin-aarch64", "model", "managed", "downloading", "acquiring_models", false); !errors.Is(err, domain.ErrRuntimeStale) {
		t.Fatal("SQLite generation overflow accepted")
	}
}
