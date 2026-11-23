package runner

import (
	"liberty.local/liberty/internal/domain"
	"os"
	"path/filepath"
	"testing"
)

func TestAttemptPathsAndIdempotentTrash(t *testing.T) {
	dir := t.TempDir()
	files, err := OpenFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	lease := domain.RunLease{JobID: "job-1700000000000-1", AttemptID: 1, Token: 2}
	if err := files.CreateJob(lease.JobID); err != nil {
		t.Fatal(err)
	}
	path, err := files.CreateAttempt(lease)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := files.CreateAttempt(lease); err == nil {
		t.Fatal("stale attempt reused")
	}
	if os.WriteFile(filepath.Join(path, "result.json"), []byte("synthetic"), 0600) != nil {
		t.Fatal("fixture")
	}
	if data, err := files.ReadAttemptFile(lease, "result.json", 100); err != nil || string(data) != "synthetic" {
		t.Fatal("result read")
	}
	for i := 0; i < 2; i++ {
		if err := files.Trash(lease.JobID, "synthetic-trash"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := files.Purge("synthetic-trash"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "jobs", ".trash", "synthetic-trash")); !os.IsNotExist(err) {
		t.Fatal("trash not purged")
	}
	for _, id := range []string{"../escape", "job-1700000000000-1/child", "job-1-1"} {
		if files.CreateJob(id) == nil {
			t.Fatal("invalid path allowed")
		}
	}
}
func TestFilesRejectSymlinkedJobAndOutput(t *testing.T) {
	dir := t.TempDir()
	files, err := OpenFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	outside := t.TempDir()
	id := "job-1700000000000-1"
	if os.Symlink(outside, filepath.Join(dir, "jobs", id)) != nil {
		t.Skip("symlinks unavailable")
	}
	if files.CreateJob(id) == nil {
		t.Fatal("symlinked job accepted")
	}
	lease := domain.RunLease{JobID: "job-1700000000000-2", AttemptID: 1, Token: 1}
	if files.CreateJob(lease.JobID) != nil {
		t.Fatal("create")
	}
	path, err := files.CreateAttempt(lease)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "private-fixture")
	if os.WriteFile(target, []byte("private"), 0600) != nil {
		t.Fatal("fixture")
	}
	if os.Symlink(target, filepath.Join(path, "result.json")) != nil {
		t.Fatal("symlink")
	}
	if _, err := files.ReadAttemptFile(lease, "result.json", 100); err == nil {
		t.Fatal("symlinked output accepted")
	}
}
