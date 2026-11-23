package managed

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// MaterializeResources publishes verified copies under a content-addressed
// directory. It never reads or overwrites scripts in a user-selected location.
func MaterializeResources(directory string) (string, error) {
	return materializeResources(directory, nil)
}

func resourceContents(inputs map[string][]byte) map[string][]byte {
	contents := make(map[string][]byte, len(resources)+len(inputs))
	for name, content := range resources {
		contents[name] = []byte(content)
	}
	for name, content := range inputs {
		contents[name] = content
	}
	return contents
}

func materializeResources(directory string, inputs map[string][]byte) (string, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", ErrResources
	}
	defer root.Close()
	contents := resourceContents(inputs)
	keys := make([]string, 0, len(contents))
	for name := range contents {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	version := resourceDigest(contents)
	for _, name := range []string{"resources", "resources/" + version} {
		if root.Mkdir(name, 0700) != nil {
			info, err := root.Lstat(name)
			if err != nil || !info.IsDir() || linkedFile(info) {
				return "", ErrResources
			}
		}
	}
	target, err := root.OpenRoot("resources/" + version)
	if err != nil {
		return "", ErrResources
	}
	defer target.Close()
	expectedNames := make(map[string]bool, len(contents))
	for name := range contents {
		expectedNames[name] = true
	}
	existing, err := target.Open(".")
	if err != nil {
		return "", ErrResources
	}
	entries, readErr := existing.ReadDir(len(contents) + 1)
	existing.Close()
	if readErr != nil && readErr != io.EOF {
		return "", ErrResources
	}
	for _, entry := range entries {
		if !expectedNames[entry.Name()] || !entry.Type().IsRegular() {
			return "", ErrResources
		}
	}
	for _, name := range keys {
		expected := contents[name]
		if info, err := target.Lstat(name); err == nil {
			if !info.Mode().IsRegular() {
				return "", ErrResources
			}
			actual, err := readRegularFile(target, name, int64(len(expected)))
			if err != nil || !bytes.Equal(actual, expected) {
				return "", ErrResources
			}
			continue
		} else if !os.IsNotExist(err) {
			return "", ErrResources
		}
		f, err := target.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return "", ErrResources
		}
		_, writeErr := f.Write(expected)
		syncErr := f.Sync()
		closeErr := f.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			_ = target.Remove(name)
			return "", ErrResources
		}
	}
	if err := checkDirectoryEntries(target, expectedNames); err != nil {
		return "", err
	}
	return filepath.Join(directory, "resources", version), nil
}
