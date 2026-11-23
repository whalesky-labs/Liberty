package profile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestProfileLocationsKeepCandidateSeparate(t *testing.T) {
	base := filepath.Join(t.TempDir(), "合成 Application Support")
	production, candidate := directoryWithin(base, false), directoryWithin(base, true)
	if production != filepath.Join(base, "com.westng.liberty") || candidate != filepath.Join(base, "com.westng.liberty.native-candidate") {
		t.Fatal("profile identities changed")
	}
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Fatal("resolving a profile created directories")
	}
}

func TestPrepareProfileDirectoryRequiresExplicitCreation(t *testing.T) {
	name := "资料 #?"
	if runtime.GOOS == "windows" {
		name = "资料 #%" // A question mark is not a legal Windows filename.
	}
	directory := filepath.Join(t.TempDir(), "nested", name)
	if err := PrepareDirectory(directory, false); !errors.Is(err, ErrLocation) {
		t.Fatal("missing directory accepted without preparation", err)
	}
	if _, err := os.Stat(filepath.Dir(directory)); !os.IsNotExist(err) {
		t.Fatal("read-only selection created directories")
	}
	if err := PrepareDirectory(directory, true); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		t.Fatal("profile not created", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		t.Fatal("new profile is accessible to other users")
	}
	if err := PrepareDirectory(directory, false); err != nil {
		t.Fatal("existing profile refused", err)
	}
	if err := PrepareDirectory("relative", true); !errors.Is(err, ErrLocation) {
		t.Fatal("relative profile accepted", err)
	}
}

func TestPrepareProfileRejectsFilesAndLinks(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, create := range []bool{false, true} {
		if err := PrepareDirectory(file, create); !errors.Is(err, ErrLocation) {
			t.Fatal("file accepted as profile", err)
		}
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(base, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlink privilege unavailable")
		}
		t.Fatal(err)
	}
	if err := PrepareDirectory(link, true); !errors.Is(err, ErrLocation) {
		t.Fatal("linked profile accepted", err)
	}
}
