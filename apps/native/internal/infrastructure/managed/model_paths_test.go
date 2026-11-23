package managed

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"liberty.local/liberty/internal/domain"
)

func TestResolveModelsUsesSelectedArtifactAndPinnedPaths(t *testing.T) {
	installer, _ := modelInstaller(t, false)
	ctx := context.Background()
	if err := installer.AcquireModels(ctx, 1, "official", nil); err != nil {
		t.Fatal(err)
	}
	directory, err := installer.generation("model", 1)
	if err != nil {
		t.Fatal(err)
	}
	artifact := domain.RuntimeArtifact{GenerationID: "1", Version: installer.Manifest.ModelSetVersion, Path: filepath.Join(directory, "models")}
	models, err := installer.ResolveModels(ctx, artifact)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := models.Environment(artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	if environment["FUNASR_PROFILE"] != installer.Manifest.ModelProfile {
		t.Fatal("selected model profile was lost")
	}
	pins, err := modelPins()
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{"model": "FUNASR_MODEL", "vad": "FUNASR_VAD_MODEL", "punc": "FUNASR_PUNC_MODEL", "spk": "FUNASR_SPK_MODEL"}
	for _, pin := range pins {
		relative, err := modelRelative(pin.ID)
		if err != nil || environment[keys[pin.Role]] != filepath.Join(artifact.Path, filepath.FromSlash(relative)) {
			t.Fatal("model path did not come from selected artifact pins", pin.Role, err)
		}
	}
	stale := artifact
	stale.Version = "outdated-model-set"
	if _, err := installer.ResolveModels(ctx, stale); !errors.Is(err, ErrManifest) {
		t.Fatal("outdated model set was accepted", err)
	}
	if err := os.RemoveAll(models.Speaker); err != nil {
		t.Fatal(err)
	}
	if _, err := installer.ResolveModels(ctx, artifact); !errors.Is(err, ErrAsset) {
		t.Fatal("missing pinned speaker model fell back to an alias", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := installer.ResolveModels(canceled, artifact); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled model resolution continued", err)
	}
}
