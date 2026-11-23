// Package fileout publishes artifacts to a destination granted by a native
// save dialog. Paths never enter public DTOs or error messages.
package fileout

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var ErrOutput = errors.New("export_write_failed")

func Write(ctx context.Context, filename string, data []byte) error {
	return write(ctx, filename, data, func(root *os.Root, from, to string) error { return root.Rename(from, to) })
}

func write(ctx context.Context, filename string, data []byte, replace func(*os.Root, string, string) error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !filepath.IsAbs(filename) {
		return ErrOutput
	}
	for _, part := range strings.FieldsFunc(filename, func(c rune) bool { return c == '/' || c == '\\' }) {
		if part == ".." {
			return ErrOutput
		}
	}
	parent := filepath.Dir(filename)
	for dir := parent; ; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrOutput
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return ErrOutput
	}
	defer root.Close()
	name := filepath.Base(filename)
	before, err := root.Lstat(name)
	if err == nil && !before.Mode().IsRegular() {
		return ErrOutput
	}
	if err != nil && !os.IsNotExist(err) {
		return ErrOutput
	}
	var nonce [16]byte
	rand.Read(nonce[:])
	temporary := ".liberty-export-" + hex.EncodeToString(nonce[:]) + ".tmp"
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return ErrOutput
	}
	defer root.Remove(temporary)
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return ErrOutput
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	after, err := root.Lstat(name)
	if before == nil {
		if !os.IsNotExist(err) {
			return ErrOutput
		}
	} else if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return ErrOutput
	}
	if replace(root, temporary, name) != nil {
		return ErrOutput
	}
	// Directory sync is best-effort where the OS doesn't support it. File data
	// was synced before publication; an OS write error never removes the target.
	if dir, err := root.Open("."); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
