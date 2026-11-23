package profile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	ProductionID = "com.westng.liberty"
	CandidateID  = "com.westng.liberty.native-candidate"
	DatabaseName = "liberty.sqlite3"
)

var ErrLocation = errors.New("profile_directory_unavailable")
var ErrProfileOverlap = errors.New("production_profile_requires_explicit_selection")
var ErrProductionMutation = errors.New("production_profile_mutation_requires_isolated_import")

func DefaultDirectory(candidate bool) (string, error) {
	base, err := localDataDirectory()
	if err != nil || !filepath.IsAbs(base) {
		return "", ErrLocation
	}
	return directoryWithin(base, candidate), nil
}

func RequireIsolatedDirectory(directory, production string) error {
	_, err := IsolatedDirectory(directory, production)
	return err
}

func IsolatedDirectory(directory, production string) (string, error) {
	selected, err := ResolveDirectory(directory)
	if err != nil {
		return "", err
	}
	existing, err := ResolveDirectory(production)
	if err != nil {
		return "", err
	}
	if pathsOverlap(strings.ToLower(selected), strings.ToLower(existing)) {
		return "", ErrProfileOverlap
	}
	selectedFile, selectedErr := os.Stat(filepath.Join(selected, DatabaseName))
	productionFile, productionErr := os.Stat(filepath.Join(existing, DatabaseName))
	if (selectedErr != nil && !os.IsNotExist(selectedErr)) || (productionErr != nil && !os.IsNotExist(productionErr)) || (selectedErr == nil && productionErr == nil && os.SameFile(selectedFile, productionFile)) {
		return "", ErrProfileOverlap
	}
	return selected, nil
}

func pathsOverlap(left, right string) bool {
	if left == right {
		return true
	}
	return pathContains(left, right) || pathContains(right, left)
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil || relative == "." {
		return err == nil
	}
	return !filepath.IsAbs(relative) && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func ResolveDirectory(directory string) (string, error) {
	if !filepath.IsAbs(directory) || hasParentComponent(directory) {
		return "", ErrLocation
	}
	return resolveDirectoryPath(directory)
}

func hasParentComponent(directory string) bool {
	volume := filepath.VolumeName(directory)
	for _, component := range strings.FieldsFunc(directory[len(volume):], func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if component == ".." {
			return true
		}
	}
	return false
}

func resolveDirectoryPath(directory string) (string, error) {
	current := filepath.Clean(directory)
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			info, statErr := os.Stat(resolved)
			if statErr != nil || !info.IsDir() {
				return "", ErrLocation
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) || filepath.Dir(current) == current {
			return "", ErrLocation
		}
		missing = append(missing, filepath.Base(current))
		current = filepath.Dir(current)
	}
}

func directoryWithin(base string, candidate bool) string {
	identifier := ProductionID
	if candidate {
		identifier = CandidateID
	}
	return filepath.Join(base, identifier)
}

func PrepareDirectory(directory string, create bool) error {
	if !filepath.IsAbs(directory) {
		return ErrLocation
	}
	info, err := os.Lstat(directory)
	if os.IsNotExist(err) && create {
		if os.MkdirAll(directory, 0700) != nil {
			return ErrLocation
		}
		info, err = os.Lstat(directory)
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrLocation
	}
	return nil
}
