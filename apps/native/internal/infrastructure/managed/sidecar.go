package managed

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"

	"liberty.local/liberty/internal/domain"
)

func openPlainChild(parent *os.Root, relative string, create bool) (*os.Root, error) {
	normalized, err := safeRelative(filepath.ToSlash(relative))
	if err != nil {
		return nil, ErrResources
	}
	current, err := parent.OpenRoot(".")
	if err != nil {
		return nil, ErrResources
	}
	for _, name := range strings.Split(normalized, "/") {
		before, err := current.Lstat(name)
		if errors.Is(err, os.ErrNotExist) && create {
			if err := current.Mkdir(name, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				current.Close()
				return nil, ErrResources
			}
			before, err = current.Lstat(name)
		}
		if errors.Is(err, os.ErrNotExist) {
			current.Close()
			return nil, os.ErrNotExist
		}
		if err != nil || !before.IsDir() || linkedFile(before) {
			current.Close()
			return nil, ErrResources
		}
		next, err := current.OpenRoot(name)
		if err != nil {
			current.Close()
			return nil, ErrResources
		}
		opened, openedErr := next.Stat(".")
		after, afterErr := current.Lstat(name)
		current.Close()
		if openedErr != nil || afterErr != nil || linkedFile(opened) || linkedFile(after) || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
			next.Close()
			return nil, ErrResources
		}
		current = next
	}
	return current, nil
}

func (i *Installer) runtimeDirectory(relative string, create bool) (*os.Root, error) {
	root, err := openUnlinkedDirectory(i.Root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return openPlainChild(root, relative, create)
}

func withinDirectory(directory, filename string) bool {
	relative, err := filepath.Rel(directory, filename)
	return err == nil && relative != "." && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func resolvedArtifact(directory, filename string, model bool) (string, error) {
	if !filepath.IsAbs(filename) || !withinDirectory(directory, filename) {
		return "", ErrResources
	}
	parent, err := openUnlinkedDirectory(filepath.Dir(filename))
	if err != nil {
		return "", err
	}
	defer parent.Close()
	info, err := parent.Lstat(filepath.Base(filename))
	if err != nil {
		return "", err
	}
	if !model && info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(filename)
		if err != nil || !withinDirectory(directory, resolved) {
			return "", ErrResources
		}
		return resolvedArtifact(directory, resolved, false)
	}
	if linkedFile(info) || (model && !info.IsDir()) || (!model && !info.Mode().IsRegular()) {
		return "", ErrResources
	}
	if !model {
		file, err := parent.Open(filepath.Base(filename))
		if err != nil {
			return "", ErrResources
		}
		defer file.Close()
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) || !singleLinkFile(file) {
			return "", ErrResources
		}
	}
	return filename, nil
}

func verifyResourceSnapshot(directory string, snapshot map[string][]byte) error {
	root, err := openUnlinkedDirectory(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	expected := make(map[string]bool, len(snapshot))
	for name, content := range snapshot {
		expected[name] = true
		actual, err := readRegularFile(root, name, int64(len(content)))
		if err != nil || !bytes.Equal(actual, content) {
			return ErrResources
		}
	}
	return checkDirectoryEntries(root, expected)
}

func commandInputSnapshot(ctx context.Context, parent *os.Root, snapshot map[string][]byte) (string, func() error, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", nil, ErrResources
	}
	name := "input-" + hex.EncodeToString(nonce[:])
	if parent.Mkdir(name, 0700) != nil {
		return "", nil, ErrResources
	}
	root, err := openPlainChild(parent, name, false)
	if err != nil {
		parent.RemoveAll(name)
		return "", nil, err
	}
	cleanup := sync.OnceValue(func() error {
		failed := root.Chmod(".", 0700) != nil
		for filename := range snapshot {
			info, err := root.Lstat(filename)
			if err == nil && info.Mode().IsRegular() && !linkedFile(info) {
				file, openErr := root.Open(filename)
				if openErr != nil {
					failed = true
					continue
				}
				opened, statErr := file.Stat()
				if statErr != nil || !os.SameFile(info, opened) || !singleLinkFile(file) || root.Chmod(filename, 0600) != nil {
					failed = true
				}
				if file.Close() != nil {
					failed = true
				}
			}
		}
		if root.Close() != nil {
			failed = true
		}
		if parent.RemoveAll(name) != nil {
			failed = true
		}
		if failed {
			return ErrResources
		}
		return nil
	})
	for filename, content := range snapshot {
		if ctx.Err() != nil || !safeName(filename) {
			cleanup()
			return "", nil, ErrResources
		}
		file, err := root.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			cleanup()
			return "", nil, ErrResources
		}
		written, writeErr := file.Write(content)
		syncErr, closeErr := file.Sync(), file.Close()
		if writeErr != nil || written != len(content) || syncErr != nil || closeErr != nil || root.Chmod(filename, 0400) != nil {
			cleanup()
			return "", nil, ErrResources
		}
	}
	directory := filepath.Join(parent.Name(), name)
	if root.Chmod(".", 0500) != nil || verifyResourceSnapshot(directory, snapshot) != nil {
		cleanup()
		return "", nil, ErrResources
	}
	return directory, cleanup, nil
}

func ValidateRuntimeProfile(profileID string) error {
	profileID, err := runtimeProfileID(profileID)
	if err != nil || profileID == ProductionRuntimeProfile {
		return err
	}
	manifest, err := LoadManifest()
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return ErrResources
	}
	_, _, _, err = loadRuntimeInputs(manifest, CurrentPlatform(), profileID, executable, runtime.GOOS)
	return err
}

func runtimeInputDirectory(executable, operatingSystem string) (string, error) {
	if !filepath.IsAbs(executable) {
		return "", ErrResources
	}
	directory := filepath.Dir(executable)
	switch operatingSystem {
	case "darwin":
		if filepath.Base(directory) != "MacOS" || filepath.Base(filepath.Dir(directory)) != "Contents" {
			return "", ErrResources
		}
		return filepath.Join(filepath.Dir(directory), "Resources", "runtime-inputs"), nil
	case "windows":
		return filepath.Join(directory, "runtime-inputs"), nil
	default:
		return "", domain.ErrRuntimeUnsupported
	}
}

func loadRuntimeInputs(manifest Manifest, platformID, profileID, executable, operatingSystem string) (Manifest, map[string][]byte, string, error) {
	selected, candidate, err := manifest.withProfile(profileID)
	if err != nil {
		return Manifest{}, nil, "", err
	}
	if candidate == nil {
		inputs, digest, err := productionSourceInputs(selected)
		return selected, inputs, digest, err
	}
	supported := false
	for _, platform := range candidate.Platforms {
		supported = supported || platform.ID == platformID
	}
	if !supported {
		return Manifest{}, nil, "", domain.ErrRuntimeUnsupported
	}
	directory, err := runtimeInputDirectory(executable, operatingSystem)
	if err != nil {
		return Manifest{}, nil, "", err
	}
	root, err := openUnlinkedDirectory(directory)
	if err != nil {
		return Manifest{}, nil, "", err
	}
	defer root.Close()
	inputs := make(map[string][]byte, len(candidate.Files))
	expected := make(map[string]bool, len(candidate.Files))
	for _, file := range candidate.Files {
		expected[file.Name] = true
	}
	if err := checkDirectoryEntries(root, expected); err != nil {
		return Manifest{}, nil, "", err
	}
	for _, file := range candidate.Files {
		content, err := readRegularFile(root, file.Name, file.Bytes)
		if err != nil {
			return Manifest{}, nil, "", err
		}
		digest := sha256.Sum256(content)
		if !strings.EqualFold(hex.EncodeToString(digest[:]), file.SHA256) {
			return Manifest{}, nil, "", ErrChecksum
		}
		inputs[file.Name] = content
	}
	if err := checkDirectoryEntries(root, expected); err != nil {
		return Manifest{}, nil, "", err
	}
	return selected, inputs, resourceDigest(inputs), nil
}

func openUnlinkedDirectory(directory string) (*os.Root, error) {
	if !filepath.IsAbs(directory) {
		return nil, ErrResources
	}
	for current := filepath.Clean(directory); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		if err != nil || !info.IsDir() || linkedFile(info) {
			return nil, ErrResources
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, ErrResources
	}
	return root, nil
}

func linkedFile(info os.FileInfo) bool {
	if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return true
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer && !value.IsNil() {
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return false
	}
	if links := value.FieldByName("Nlink"); links.IsValid() && links.CanUint() && info.Mode().IsRegular() && links.Uint() > 1 {
		return true
	}
	attributes := value.FieldByName("FileAttributes")
	return attributes.IsValid() && attributes.CanUint() && attributes.Uint()&0x400 != 0
}

func readRegularFile(root *os.Root, name string, size int64) ([]byte, error) {
	before, err := root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || linkedFile(before) || before.Size() != size {
		return nil, ErrResources
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, ErrResources
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || linkedFile(opened) || !singleLinkFile(file) || !os.SameFile(before, opened) || opened.Size() != size {
		return nil, ErrResources
	}
	content, err := io.ReadAll(io.LimitReader(file, size+1))
	after, statErr := root.Lstat(name)
	if err != nil || statErr != nil || !after.Mode().IsRegular() || linkedFile(after) || !singleLinkFile(file) || !os.SameFile(opened, after) || int64(len(content)) != size {
		return nil, ErrResources
	}
	return content, nil
}

func checkDirectoryEntries(root *os.Root, expected map[string]bool) error {
	directory, err := root.Open(".")
	if err != nil {
		return ErrResources
	}
	defer directory.Close()
	entries, err := directory.ReadDir(len(expected) + 1)
	if err != nil && err != io.EOF {
		return ErrResources
	}
	if len(entries) != len(expected) {
		return ErrResources
	}
	for _, entry := range entries {
		if !expected[entry.Name()] || !entry.Type().IsRegular() {
			return ErrResources
		}
	}
	return nil
}

func resourceDigest(files map[string][]byte) string {
	keys := make([]string, 0, len(files))
	for name := range files {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, name := range keys {
		hash.Write([]byte(name))
		hash.Write([]byte{0})
		hash.Write(files[name])
	}
	return hex.EncodeToString(hash.Sum(nil))
}
