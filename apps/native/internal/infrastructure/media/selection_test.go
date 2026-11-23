package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSelectionRequiresDialogGrantAndRejectsReplacedFile(t *testing.T) {
	s := NewSelections()
	dir := t.TempDir()
	path := filepath.Join(dir, "synthetic.wav")
	if os.WriteFile(path, []byte("fixture"), 0600) != nil {
		t.Fatal("fixture")
	}
	if _, err := s.Resolve(context.Background(), path); !errors.Is(err, ErrSelection) {
		t.Fatal("raw path became file grant")
	}
	selected, err := s.Authorize(path)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Name != "synthetic.wav" || selected.Kind != "audio" || selected.Token == path {
		t.Fatal("private path exposed")
	}
	canonical, _ := filepath.EvalSymlinks(path)
	if input, err := s.Resolve(context.Background(), selected.Token); err != nil || input.Path != canonical {
		t.Fatal("selected input not resolved")
	}
	other := filepath.Join(dir, "other")
	if os.WriteFile(other, []byte("other"), 0600) != nil || os.Rename(other, path) != nil {
		t.Fatal("replace")
	}
	if _, err := s.Resolve(context.Background(), selected.Token); !errors.Is(err, ErrSelection) {
		t.Fatal("replaced file retained old grant")
	}
	selected, err = s.Authorize(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Release(selected.Token)
	if _, err := s.Resolve(context.Background(), selected.Token); !errors.Is(err, ErrSelection) {
		t.Fatal("released grant reusable")
	}
	if _, err := s.Authorize(dir); !errors.Is(err, ErrSelection) {
		t.Fatal("directory allowed as media")
	}
}
