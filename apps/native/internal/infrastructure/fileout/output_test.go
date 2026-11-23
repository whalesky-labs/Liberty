package fileout

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func destination(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "synthetic.txt")
}
func TestPublishAndFailurePreserveExistingArtifact(t *testing.T) {
	path := destination(t)
	ctx := context.Background()
	if err := Write(ctx, path, []byte("原始结果")); err != nil {
		t.Fatal(err)
	}
	if err := write(ctx, path, []byte("replacement"), func(*os.Root, string, string) error { return errors.New("synthetic failure") }); err != ErrOutput {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "原始结果" {
		t.Fatal("failed publication changed original")
	}
	if err := Write(ctx, path, []byte("更新结果")); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil || string(data) != "更新结果" {
		t.Fatal("replacement not published")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary artifacts remain")
	}
}
func TestCancelledOrLinkedDestinationNeverWritten(t *testing.T) {
	path := destination(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(Write(ctx, path, []byte("cancelled")), context.Canceled) {
		t.Fatal("cancelled export published")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("cancelled destination created")
	}
	target := destination(t)
	if os.WriteFile(target, []byte("preserve"), 0600) != nil {
		t.Fatal("fixture")
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skip("symlink unavailable")
	}
	if Write(context.Background(), path, []byte("rejected")) != ErrOutput {
		t.Fatal("symlink destination accepted")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "preserve" {
		t.Fatal("link target overwritten")
	}
	parent := filepath.Join(filepath.Dir(path), "linked-parent")
	if os.Symlink(filepath.Dir(target), parent) != nil {
		t.Fatal("parent link")
	}
	if Write(context.Background(), filepath.Join(parent, "new.txt"), []byte("rejected")) != ErrOutput {
		t.Fatal("linked parent accepted")
	}
}
