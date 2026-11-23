// Package legacyjobs reads the original jobs/<id>/job.json layout. It uses an
// explicitly owned profile root, bounded reads and no symlink traversal.
package legacyjobs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/infrastructure/runner"
)

var ErrImport = errors.New("legacy_job_import_failed")
var ErrLimit = errors.New("legacy_job_import_size_limit")
var errInvalid = errors.New("legacy_snapshot_invalid")

const maxSnapshot = 64 << 20
const maxLog = 8 << 20

type Source struct{ root *os.Root }

func Open(directory string) (*Source, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, ErrImport
	}
	defer root.Close()
	if err = root.Mkdir("jobs", 0700); err != nil && !os.IsExist(err) {
		return nil, ErrImport
	}
	info, err := root.Lstat("jobs")
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrImport
	}
	jobs, err := root.OpenRoot("jobs")
	if err != nil {
		return nil, ErrImport
	}
	return &Source{root: jobs}, nil
}
func (s *Source) Close() error { return s.root.Close() }
func (s *Source) Each(ctx context.Context, accept func(application.LegacyJob) error) (int, error) {
	dir, err := s.root.Open(".")
	if err != nil {
		return 0, ErrImport
	}
	defer dir.Close()
	skipped := 0
	for {
		entries, readErr := dir.ReadDir(64)
		if readErr != nil && readErr != io.EOF {
			return skipped, ErrImport
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				return skipped, ctx.Err()
			}
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			info, err := s.root.Lstat(entry.Name())
			if err != nil {
				return skipped, ErrImport
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				skipped++
				continue
			}
			job, err := s.read(entry.Name())
			if errors.Is(err, errInvalid) || errors.Is(err, os.ErrNotExist) {
				skipped++
				continue
			}
			if err != nil {
				return skipped, err
			}
			if err := accept(job); err != nil {
				return skipped, err
			}
		}
		if readErr == io.EOF {
			return skipped, nil
		}
	}
}
func read(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, ErrImport
	}
	if info.Size() > limit {
		return nil, ErrLimit
	}
	if !info.Mode().IsRegular() {
		return nil, errInvalid
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, ErrImport
	}
	defer f.Close()
	now, err := f.Stat()
	if err != nil || !now.Mode().IsRegular() || !os.SameFile(info, now) {
		return nil, ErrImport
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, ErrImport
	}
	if int64(len(data)) > limit {
		return nil, ErrLimit
	}
	if !utf8.Valid(data) {
		return nil, errInvalid
	}
	return data, nil
}
func (s *Source) read(name string) (application.LegacyJob, error) {
	root, err := s.root.OpenRoot(name)
	if err != nil {
		return application.LegacyJob{}, ErrImport
	}
	defer root.Close()
	data, err := read(root, "job.json", maxSnapshot)
	if err != nil {
		return application.LegacyJob{}, err
	}
	job, err := decodeJob(data)
	if err != nil {
		return job, err
	}
	// A snapshot cannot claim another task directory or an executable path ID.
	if job.Result.Job.ID != filepath.Base(name) {
		return job, errInvalid
	}
	if data, err = read(root, "progress.json", 1<<20); err == nil {
		if progress, e := runner.ParseProgress(data); e == nil {
			applyProgress(&job, progress)
		}
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, errInvalid) {
		return job, err
	}
	if data, err = read(root, "result.json", maxSnapshot); err == nil {
		applyResult(&job, data)
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, errInvalid) {
		return job, err
	}
	job.Result.ProcessLog = nil
	if data, err = read(root, "process.log", maxLog); err == nil {
		if text := strings.TrimSpace(string(data)); text != "" {
			job.Result.ProcessLog = &text
		}
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, errInvalid) {
		return job, err
	}
	job.Result.SummaryRuns = nil
	job.Result.Job.ActiveSummaryRunID = nil
	return job, nil
}
