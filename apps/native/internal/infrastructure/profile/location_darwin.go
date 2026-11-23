package profile

import (
	"os"
	"path/filepath"
)

func localDataDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return "", ErrLocation
	}
	return filepath.Join(home, "Library", "Application Support"), nil
}
