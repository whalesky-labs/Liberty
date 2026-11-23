package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"liberty.local/liberty/internal/domain"
)

var ErrPath = errors.New("job_path_invalid")
var trashPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,192}$`)

// Files keeps all job operations rooted at the explicitly owned profile. Go's
// os.Root prevents symlink escape during the operation, not just at validation.
type Files struct {
	root      *os.Root
	directory string
}

func OpenFiles(profileDirectory string) (*Files, error) {
	canonical, err := filepath.EvalSymlinks(profileDirectory)
	if err != nil {
		return nil, ErrPath
	}
	root, err := os.OpenRoot(canonical)
	if err != nil {
		return nil, ErrPath
	}
	if err := ensureDirectory(root, "jobs", true); err != nil {
		root.Close()
		return nil, err
	}
	jobs, err := root.OpenRoot("jobs")
	root.Close()
	if err != nil {
		return nil, ErrPath
	}
	return &Files{root: jobs, directory: filepath.Join(canonical, "jobs")}, nil
}
func (f *Files) Close() error { return f.root.Close() }
func ensureDirectory(root *os.Root, path string, create bool) error {
	info, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && create {
		if err := root.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return ErrPath
		}
		info, err = root.Lstat(path)
	}
	if errors.Is(err, os.ErrNotExist) {
		return os.ErrNotExist
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrPath
	}
	return nil
}
func (f *Files) CreateJob(id string) error {
	if domain.ValidateJobID(id) != nil {
		return domain.ErrJobID
	}
	return ensureDirectory(f.root, id, true)
}
func (f *Files) CreateAttempt(l domain.RunLease) (string, error) {
	if l.Validate() != nil {
		return "", domain.ErrLease
	}
	if err := ensureDirectory(f.root, l.JobID, false); err != nil {
		return "", err
	}
	job, err := f.root.OpenRoot(l.JobID)
	if err != nil {
		return "", ErrPath
	}
	defer job.Close()
	if err := ensureDirectory(job, "attempts", true); err != nil {
		return "", err
	}
	attempts, err := job.OpenRoot("attempts")
	if err != nil {
		return "", ErrPath
	}
	defer attempts.Close()
	name := fmt.Sprintf("attempt-%d-%d", l.AttemptID, l.Token)
	// Reusing an old attempt output would accept a stale result. New leases must
	// always receive an empty directory, never clear an existing one to proceed.
	if err := attempts.Mkdir(name, 0700); err != nil {
		return "", ErrPath
	}
	return filepath.Join(f.directory, l.JobID, "attempts", name), nil
}
func (f *Files) Trash(id, name string) error {
	if domain.ValidateJobID(id) != nil || !trashPattern.MatchString(name) {
		return ErrPath
	}
	if err := ensureDirectory(f.root, ".trash", true); err != nil {
		return err
	}
	originalErr := ensureDirectory(f.root, id, false)
	destination := filepath.Join(".trash", name)
	trashErr := ensureDirectory(f.root, destination, false)
	if originalErr != nil && !errors.Is(originalErr, os.ErrNotExist) {
		return originalErr
	}
	if trashErr != nil && !errors.Is(trashErr, os.ErrNotExist) {
		return trashErr
	}
	if errors.Is(originalErr, os.ErrNotExist) {
		return nil
	}
	if trashErr == nil {
		return ErrPath
	}
	if f.root.Rename(id, destination) != nil {
		return ErrPath
	}
	return nil
}
func (f *Files) Purge(name string) error {
	if !trashPattern.MatchString(name) {
		return ErrPath
	}
	if err := ensureDirectory(f.root, ".trash", false); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	path := filepath.Join(".trash", name)
	if err := ensureDirectory(f.root, path, false); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if f.root.RemoveAll(path) != nil {
		return ErrPath
	}
	return nil
}

// ReadAttemptFile is bounded and tied to the lease directory. It never accepts
// an output_dir supplied by an untrusted UI or a stale database row.
func (f *Files) ReadAttemptFile(l domain.RunLease, name string, limit int64) ([]byte, error) {
	if l.Validate() != nil || (name != "result.json" && name != "progress.json") {
		return nil, ErrPath
	}
	path := filepath.Join(l.JobID, "attempts", fmt.Sprintf("attempt-%d-%d", l.AttemptID, l.Token))
	for _, part := range []string{l.JobID, filepath.Join(l.JobID, "attempts"), path} {
		if err := ensureDirectory(f.root, part, false); err != nil {
			return nil, ErrPath
		}
	}
	root, err := f.root.OpenRoot(path)
	if err != nil {
		return nil, ErrPath
	}
	defer root.Close()
	return readOutput(root, name, limit)
}
