package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func modelPathFixture(t *testing.T, directory string) ModelPaths {
	t.Helper()
	models := ModelPaths{
		Profile:     "paraformer",
		ASR:         filepath.Join(directory, "modelscope", "models", "synthetic", "asr"),
		VAD:         filepath.Join(directory, "modelscope", "models", "synthetic", "vad"),
		Punctuation: filepath.Join(directory, "modelscope", "models", "synthetic", "punc"),
		Speaker:     filepath.Join(directory, "modelscope", "models", "synthetic", "speaker"),
	}
	for _, model := range []string{models.ASR, models.VAD, models.Punctuation, models.Speaker} {
		if err := os.MkdirAll(model, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return models
}

func TestModelPathsRejectMissingAndEscapingDirectories(t *testing.T) {
	directory := t.TempDir()
	models := modelPathFixture(t, directory)
	for _, scenario := range []struct {
		name string
		path string
	}{
		{"missing", filepath.Join(directory, "absent")},
		{"relative", "relative/model"},
		{"parent", t.TempDir()},
		{"root", directory},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			invalid := models
			invalid.Speaker = scenario.path
			if _, err := invalid.Environment(directory); !errors.Is(err, ErrStart) {
				t.Fatal("invalid model path was accepted", err)
			}
		})
	}
	if runtime.GOOS != "windows" {
		t.Run("linked_parent", func(t *testing.T) {
			outside := t.TempDir()
			if err := os.MkdirAll(filepath.Join(outside, "model"), 0700); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(directory, "escape")
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			invalid := models
			invalid.ASR = filepath.Join(link, "model")
			if _, err := invalid.Environment(directory); !errors.Is(err, ErrStart) {
				t.Fatal("linked model escaped selected artifact", err)
			}
		})
	}
}

func TestCommandOverridesInheritedModelSelection(t *testing.T) {
	spec := testSpec(t, "valid")
	spec.Models = filepath.Join(spec.Directory, "models")
	spec.ModelPaths = modelPathFixture(t, spec.Models)
	expected, err := spec.ModelPaths.Environment(spec.Models)
	if err != nil {
		t.Fatal(err)
	}
	for key := range expected {
		t.Setenv(key, "unselected-model-alias")
	}
	command, err := spec.command(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range expected {
		count := 0
		for _, entry := range command.Env {
			if strings.HasPrefix(entry, key+"=") {
				count++
				if entry != key+"="+value {
					t.Fatal("inherited model selection reached runner", key)
				}
			}
		}
		if count != 1 {
			t.Fatal("model selection must be overridden exactly once", key, count)
		}
	}
	spec.ModelPaths.Speaker = "cam++"
	if _, err := spec.command(context.Background()); !errors.Is(err, ErrStart) {
		t.Fatal("runner accepted an unresolved model alias", err)
	}
}
