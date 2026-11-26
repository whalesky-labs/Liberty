package managed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveModelPinsRejectDriftAndLinkedWeights(t *testing.T) {
	installer, err := NewInstaller(t.TempDir(), CurrentPlatform())
	if err != nil {
		t.Fatal(err)
	}
	model := *installer.Manifest.LiveASR
	model.Files = append([]ModelFile(nil), model.Files...)
	model.Version = "synthetic-live-v1"
	if _, err := installer.ResolveLiveModel(context.Background()); err == nil {
		t.Fatal("missing model accepted")
	}
	if _, err := os.Stat(filepath.Join(installer.Root, "runtime", "live-models")); !os.IsNotExist(err) {
		t.Fatal("readiness check created model files")
	}
	directory := filepath.Join(installer.Root, "runtime", "live-models", model.Version)
	if os.MkdirAll(directory, 0700) != nil {
		t.Fatal("fixture directory")
	}
	for index := range model.Files {
		f := &model.Files[index]
		data := []byte("synthetic " + f.Path)
		sum := sha256.Sum256(data)
		f.SHA256 = hex.EncodeToString(sum[:])
		f.Size = int64(len(data))
		if err := os.WriteFile(filepath.Join(directory, f.Path), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	installer.Manifest.LiveASR = &model
	if actual, err := installer.ResolveLiveModel(context.Background()); err != nil || actual != directory {
		t.Fatal("pinned model unavailable", err)
	}
	weight := filepath.Join(directory, "model.pt")
	info, _ := os.Stat(weight)
	if err := os.WriteFile(weight, []byte(strings.Repeat("x", int(info.Size()))), 0600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(weight, time.Now(), info.ModTime().Add(time.Second))
	if _, err := installer.ResolveLiveModel(context.Background()); err == nil {
		t.Fatal("changed weights reused cached validation")
	}
	_ = os.Remove(weight)
	external := filepath.Join(t.TempDir(), "weights")
	_ = os.WriteFile(external, []byte("synthetic model.pt"), 0600)
	if err := os.Symlink(external, weight); err == nil {
		if _, err := installer.ResolveLiveModel(context.Background()); err == nil {
			t.Fatal("linked weights accepted")
		}
	}
	bad := model
	bad.Revision = "master"
	if validLiveModel(bad) {
		t.Fatal("mutable model revision accepted")
	}
}
