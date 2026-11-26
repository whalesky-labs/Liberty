package managed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestTranslationEngineInventoryRejectsInjectedCodeLinksAndCorruption(t *testing.T) {
	for _, change := range []string{"extra-code", "extra-directory", "symlink", "corrupt", "missing"} {
		t.Run(change, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.MkdirAll(filepath.Join(directory, "engine", "package"), 0700); err != nil {
				t.Fatal(err)
			}
			data := []byte("synthetic engine")
			name := filepath.Join(directory, "engine", "package", "__init__.py")
			if err := os.WriteFile(name, data, 0600); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(data)
			archive := TranslationArchive{SHA256: hex.EncodeToString(sum[:]), Files: []TranslationFile{{Path: "package/__init__.py", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))}}}
			root, err := os.OpenRoot(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			installer := &Installer{}
			if err := installer.verifyTranslation(context.Background(), root, "engine", archive); err != nil {
				t.Fatal("valid inventory rejected", err)
			}
			switch change {
			case "extra-code":
				err = os.WriteFile(filepath.Join(directory, "engine", "sitecustomize.py"), data, 0600)
			case "extra-directory":
				err = os.Mkdir(filepath.Join(directory, "engine", "__pycache__"), 0700)
			case "corrupt":
				err = os.WriteFile(name, []byte("changed"), 0600)
			case "missing":
				err = os.Remove(name)
			case "symlink":
				if err = os.Remove(name); err == nil {
					err = os.Symlink(filepath.Join(directory, "elsewhere"), name)
				}
				if err != nil {
					t.Skip("symbolic links unavailable")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := installer.verifyTranslation(context.Background(), root, "engine", archive); err == nil {
				t.Fatal("changed engine accepted")
			}
		})
	}
}

func TestLiveRunnerRevalidatesMaterializedResourcesBeforeLaunch(t *testing.T) {
	for _, name := range []string{"live_runner.py", "translation_runner.py"} {
		t.Run(name, func(t *testing.T) {
			installer, err := NewInstaller(t.TempDir(), CurrentPlatform())
			if err != nil {
				t.Fatal(err)
			}
			script, err := installer.LiveScript(context.Background(), name)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(script, []byte("changed after installation"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := installer.LiveScript(context.Background(), name); err == nil {
				t.Fatal("changed script accepted at launch")
			}
			if _, err := installer.LiveScript(context.Background(), "runner.py"); err == nil {
				t.Fatal("unrelated script accepted as a live worker")
			}
		})
	}
}
