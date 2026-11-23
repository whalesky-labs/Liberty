package managed

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func zipFixture(t *testing.T, entries map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fixture.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, data := range entries {
		out, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if w.Close() != nil || f.Close() != nil {
		t.Fatal("archive close")
	}
	return p
}
func tarFixture(t *testing.T, headers []*tar.Header) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fixture.tar.gz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	w := tar.NewWriter(gz)
	for _, h := range headers {
		if w.WriteHeader(h) != nil {
			t.Fatal("tar header")
		}
		if h.Size > 0 {
			if _, err := w.Write(bytes.Repeat([]byte("x"), int(h.Size))); err != nil {
				t.Fatal(err)
			}
		}
	}
	if w.Close() != nil || gz.Close() != nil || f.Close() != nil {
		t.Fatal("tar close")
	}
	return p
}
func TestArchivePreflightRejectsEscapesAndAliasesBeforeWrites(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "C:/absolute", "dir\\file", "NUL.txt", "dir/COM1", "dir/name. ", "dir/../../escape"} {
		t.Run(name, func(t *testing.T) {
			archive := zipFixture(t, map[string]string{"valid.txt": "valid", name: "escape"})
			dir := t.TempDir()
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if Extract(context.Background(), archive, root, "stage") == nil {
				t.Fatal("unsafe archive accepted")
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Fatal("preflight wrote output")
			}
		})
	}
	archive := zipFixture(t, map[string]string{"A/file": "one", "a/FILE": "two"})
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if Extract(context.Background(), archive, root, "stage") == nil {
		t.Fatal("case aliases accepted")
	}
}
func TestArchiveExtractsSafePythonLinksAndPreservesExistingDestination(t *testing.T) {
	archive := tarFixture(t, []*tar.Header{
		{Name: "python/bin/python3.10", Mode: 0755, Size: 7, Typeflag: tar.TypeReg},
		{Name: "python/bin/python3", Linkname: "python3.10", Typeflag: tar.TypeSymlink},
		{Name: "python/bin/python", Linkname: "python/bin/python3.10", Typeflag: tar.TypeLink},
	})
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := Extract(context.Background(), archive, root, "stage"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"python", "python3", "python3.10"} {
		data, err := os.ReadFile(filepath.Join(directory, "stage", "python", "bin", name))
		if err != nil || string(data) != "xxxxxxx" {
			t.Fatal("Python link layout changed")
		}
	}
	if Extract(context.Background(), archive, root, "stage") == nil {
		t.Fatal("existing stage overwritten")
	}
}
func TestArchiveRejectsEscapingLinksParentsAndTruncatedGzip(t *testing.T) {
	fixtures := [][]*tar.Header{
		{{Name: "dir/link", Linkname: "../../outside", Typeflag: tar.TypeSymlink}},
		{{Name: "link", Linkname: "inside", Typeflag: tar.TypeSymlink}, {Name: "link/file", Size: 1, Typeflag: tar.TypeReg}},
		{{Name: "a", Linkname: "b", Typeflag: tar.TypeSymlink}, {Name: "b", Linkname: "a", Typeflag: tar.TypeSymlink}},
		{{Name: "pipe", Typeflag: tar.TypeFifo}},
	}
	for _, headers := range fixtures {
		root, err := os.OpenRoot(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if Extract(context.Background(), tarFixture(t, headers), root, "stage") == nil {
			t.Fatal("unsafe tar accepted")
		}
		root.Close()
	}
	archive := tarFixture(t, []*tar.Header{{Name: "valid", Size: 3, Mode: 0600, Typeflag: tar.TypeReg}})
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if os.WriteFile(archive, data[:len(data)-6], 0600) != nil {
		t.Fatal("truncate")
	}
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if Extract(context.Background(), archive, root, "stage") == nil {
		t.Fatal("truncated gzip accepted")
	}
	if _, err := root.Stat("stage"); !os.IsNotExist(err) {
		t.Fatal("invalid gzip left output")
	}
}
func TestArchiveCancellationLeavesNoPartialOutput(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if Extract(ctx, zipFixture(t, map[string]string{"file": "data"}), root, "stage") != context.Canceled {
		t.Fatal("cancellation lost")
	}
	if _, err := root.Stat("stage"); !os.IsNotExist(err) {
		t.Fatal("cancelled extraction published stage")
	}
}
