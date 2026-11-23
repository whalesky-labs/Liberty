package profile

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLockExcludesAnotherProcessAndSurvivesReopen(t *testing.T) {
	directory := t.TempDir()
	owner, err := Acquire(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if other, err := Acquire(directory); !errors.Is(err, ErrInUse) {
		if other != nil {
			other.Close()
		}
		t.Fatal("second owner accepted")
	}
	child := exec.Command(os.Args[0], "-test.run=^TestLockChild$")
	child.Env = append(os.Environ(), "LIBERTY_LOCK_TEST_DIR="+directory)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child: %v %s", err, output)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, ".liberty-profile.lock")); err != nil {
		t.Fatal("lock inode was removed")
	}
	other, err := Acquire(directory)
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
}

func TestLockChild(t *testing.T) {
	dir := os.Getenv("LIBERTY_LOCK_TEST_DIR")
	if dir == "" {
		t.Skip("subprocess helper")
	}
	if lock, err := Acquire(dir); !errors.Is(err, ErrInUse) {
		if lock != nil {
			lock.Close()
		}
		t.Fatal("cross-process exclusion failed")
	}
}

func TestLockRejectsSymlinkWithoutModifyingTarget(t *testing.T) {
	dir, target := t.TempDir(), filepath.Join(t.TempDir(), "external")
	if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, ".liberty-profile.lock")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if lock, err := Acquire(dir); err == nil {
		lock.Close()
		t.Fatal("symlink accepted")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "untouched" {
		t.Fatal("target changed")
	}
}
