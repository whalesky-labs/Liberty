//go:build !windows

package schema

import (
	"os"
	"path/filepath"
)

func publishRestoreCredentialJournal(temporary, final string) error {
	if err := os.Rename(temporary, final); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(final))
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
