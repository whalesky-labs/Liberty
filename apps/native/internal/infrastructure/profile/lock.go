// Package profile owns the cross-executable lock shared with the Tauri client.
package profile

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

var (
	ErrInUse = errors.New("profile_in_use")
	ErrLock  = errors.New("profile_lock_failed")
)

// Lock holds OS ownership, never PID-file ownership. Kernel cleanup on exit or
// crash makes stale-file deletion unnecessary and avoids PID reuse races.
type Lock struct {
	file      *os.File
	directory string
	once      sync.Once
	err       error
}

func Acquire(directory string) (*Lock, error) {
	abs, err := filepath.Abs(directory)
	if err != nil {
		return nil, ErrLock
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, ErrLock
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return nil, ErrLock
	}
	file, err := openLock(filepath.Join(canonical, ".liberty-profile.lock"))
	if err != nil {
		return nil, ErrLock
	}
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || !singleLink(info) {
		file.Close()
		return nil, ErrLock
	}
	if err := lockFile(file); err != nil {
		file.Close()
		return nil, err
	}
	return &Lock{file: file, directory: canonical}, nil
}

func (l *Lock) Directory() string { return l.directory }

// Close is idempotent. The persistent empty file must not be removed.
func (l *Lock) Close() error {
	l.once.Do(func() {
		if l.file.Close() != nil {
			l.err = ErrLock
		}
	})
	return l.err
}
