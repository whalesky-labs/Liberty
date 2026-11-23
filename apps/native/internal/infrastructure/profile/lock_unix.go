//go:build darwin || linux

package profile

import (
	"errors"
	"os"
	"syscall"
)

func openLock(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
}
func singleLink(info os.FileInfo) bool { return info.Sys().(*syscall.Stat_t).Nlink == 1 }
func lockFile(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrInUse
	}
	if err != nil {
		return ErrLock
	}
	return nil
}
