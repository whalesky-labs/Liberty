package device_storage

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestScannerMeasuresProfileFilesByCategoryAndSkipsLinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeSized := func(name string, size int) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, size), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for name, size := range map[string]int{
		"liberty.sqlite3": 100, "liberty.sqlite3-wal": 20,
		"liberty.sqlite3.pre-native-v12-synthetic.bak": 30,
		"jobs/job-1/source.wav":                        200, "runtime/models/model.bin": 300,
		"desktop-pet-native-diagnostics.log": 40, "custom/notes.txt": 60,
	} {
		writeSized(name, size)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.bin"), make([]byte, 500), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "jobs", "outside")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation is unavailable")
		}
		t.Fatal(err)
	}

	report, err := (Scanner{}).Measure(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if report.Directory != canonical || report.Total != 750 || report.Database != 120 || report.Jobs != 200 || report.Runtime != 300 || report.Other != 130 {
		t.Fatalf("unexpected usage report: %+v", report)
	}
	if report.Total != report.Database+report.Jobs+report.Runtime+report.Other {
		t.Fatal("category totals do not add up to the profile total")
	}
}

func TestScannerRejectsMissingDirectoryAndHonorsCancellation(t *testing.T) {
	if _, err := (Scanner{}).Measure(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing profile directory was measured")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Scanner{}).Measure(cancelled, t.TempDir()); err == nil {
		t.Fatal("cancelled measurement was not stopped")
	}
}
