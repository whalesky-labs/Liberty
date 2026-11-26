package managed

import (
	"context"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type TranslationFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type TranslationArchive struct {
	Platform string            `json:"platformId,omitempty"`
	Name     string            `json:"fileName"`
	URL      string            `json:"url"`
	SHA256   string            `json:"sha256"`
	Size     int64             `json:"size"`
	Files    []TranslationFile `json:"files"`
}
type LocalTranslation struct {
	Version        string               `json:"version"`
	EngineVersion  string               `json:"engineVersion"`
	EngineLicense  string               `json:"engineLicense"`
	ModelLicense   string               `json:"modelLicense"`
	Source         string               `json:"sourceLanguage"`
	Target         string               `json:"targetLanguage"`
	ModelDirectory string               `json:"modelDirectory"`
	Archive        TranslationArchive   `json:"archive"`
	Engines        []TranslationArchive `json:"engines"`
	Reverse        *TranslationModel    `json:"reverse"`
}
type TranslationModel struct {
	Source    string             `json:"source"`
	Target    string             `json:"target"`
	Directory string             `json:"modelDirectory"`
	License   string             `json:"modelLicense"`
	Archive   TranslationArchive `json:"archive"`
}
type TranslationPaths struct{ Engine, Model string }

func validTranslationArchive(a TranslationArchive) bool {
	if !safeName(a.Name) || !immutableURL(a.URL) || !validHash(a.SHA256) || a.Size < 1 || a.Size > 256*1024*1024 || len(a.Files) < 1 || len(a.Files) > 256 {
		return false
	}
	seen := map[string]bool{}
	total := int64(0)
	for _, f := range a.Files {
		normalized, err := safeRelative(f.Path)
		if err != nil || normalized != f.Path || seen[strings.ToLower(f.Path)] || !validHash(f.SHA256) || f.Size < 0 || f.Size > 256*1024*1024 {
			return false
		}
		seen[strings.ToLower(f.Path)] = true
		total += f.Size
	}
	return total < 512*1024*1024
}
func validLocalTranslation(t LocalTranslation) bool {
	if !safeName(t.Version) || t.EngineVersion != "4.9.1" || t.EngineLicense != "MIT" || t.ModelLicense != "CC-BY-4.0" || t.Source != "zh" || t.Target != "en" || !safeName(t.ModelDirectory) || len(t.Engines) != 3 || !validTranslationArchive(t.Archive) {
		return false
	}
	if t.Reverse == nil || t.Reverse.Source != "en" || t.Reverse.Target != "zh" || t.Reverse.License != "CC-BY-4.0" || !safeName(t.Reverse.Directory) || !validTranslationArchive(t.Reverse.Archive) {
		return false
	}
	seen := map[string]bool{}
	for _, a := range t.Engines {
		if !validTranslationArchive(a) || seen[a.Platform] || (a.Platform != "darwin-aarch64" && a.Platform != "darwin-x64" && a.Platform != "windows-x64") {
			return false
		}
		seen[a.Platform] = true
	}
	return true
}
func (i *Installer) translationInputs() (LocalTranslation, TranslationArchive, error) {
	if i.Manifest.LiveTranslation == nil || !validLocalTranslation(*i.Manifest.LiveTranslation) {
		return LocalTranslation{}, TranslationArchive{}, ErrManifest
	}
	t := *i.Manifest.LiveTranslation
	for _, a := range t.Engines {
		if a.Platform == i.Platform.ID {
			return t, a, nil
		}
	}
	return t, TranslationArchive{}, ErrManifest
}
func (i *Installer) verifyTranslation(ctx context.Context, root *os.Root, prefix string, a TranslationArchive) error {
	info, err := root.Lstat(prefix)
	if err != nil || !info.IsDir() || linkedFile(info) {
		return ErrResources
	}
	expected := map[string]bool{}
	for _, f := range a.Files {
		expected[f.Path] = false
		for parent := path.Dir(f.Path); parent != "."; parent = path.Dir(parent) {
			expected[parent] = true
		}
	}
	var inspect func(string) error
	inspect = func(relative string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		folder, err := root.Open(path.Join(prefix, relative))
		if err != nil {
			return ErrResources
		}
		entries, readErr := folder.ReadDir(len(expected) + 1)
		folder.Close()
		if readErr != nil && readErr != io.EOF {
			return ErrResources
		}
		for _, entry := range entries {
			name := path.Join(relative, entry.Name())
			directory, ok := expected[name]
			if !ok {
				return ErrResources
			}
			info, err := root.Lstat(path.Join(prefix, name))
			if err != nil || linkedFile(info) || info.IsDir() != directory {
				return ErrResources
			}
			if directory {
				if err := inspect(name); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := inspect("."); err != nil {
		return err
	}
	for _, f := range a.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := path.Join(prefix, f.Path)
		info, err := root.Lstat(name)
		if err != nil || !info.Mode().IsRegular() || linkedFile(info) || info.Size() != f.Size {
			return ErrChecksum
		}
		key := a.SHA256 + ":" + name
		old := i.translationFiles[key]
		if old == nil || !os.SameFile(old, info) || old.Size() != info.Size() || !old.ModTime().Equal(info.ModTime()) {
			if !checksum(root, name, f.SHA256, &f.Size) {
				return ErrChecksum
			}
			after, e := root.Lstat(name)
			if e != nil || linkedFile(after) || !os.SameFile(info, after) || !after.ModTime().Equal(info.ModTime()) || after.Size() != info.Size() {
				return ErrChecksum
			}
		}
		if i.translationFiles == nil {
			i.translationFiles = map[string]os.FileInfo{}
		}
		i.translationFiles[key] = info
	}
	return nil
}
func (i *Installer) PrepareTranslation(ctx context.Context, progress func(string)) error {
	i.translationMu.Lock()
	defer i.translationMu.Unlock()
	t, engine, err := i.translationInputs()
	if err != nil {
		return err
	}
	relative := path.Join("runtime", "live-translation", t.Version, i.Platform.ID)
	root, err := i.runtimeDirectory(relative, true)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, item := range []struct {
		name    string
		archive TranslationArchive
	}{{"engine", engine}, {"model", t.Archive}, {"reverse-model", t.Reverse.Archive}} {
		if i.verifyTranslation(ctx, root, item.name, item.archive) == nil {
			continue
		}
		if progress != nil {
			progress("live_translation_downloading")
		}
		archiveName := item.name + ".zip"
		if err := i.Download.Fetch(ctx, root, archiveName, item.archive.URL, item.archive.SHA256, &item.archive.Size, nil); err != nil {
			return err
		}
		stage := "stage-" + item.name
		if err := clearDirectory(root, stage); err != nil {
			return err
		}
		if err := Extract(ctx, filepath.Join(i.Root, filepath.FromSlash(relative), archiveName), root, stage); err != nil {
			return err
		}
		if err := i.verifyTranslation(ctx, root, stage, item.archive); err != nil {
			return err
		}
		if err := clearDirectory(root, item.name); err != nil {
			return err
		}
		if root.Rename(stage, item.name) != nil {
			return ErrResources
		}
		if err := i.verifyTranslation(ctx, root, item.name, item.archive); err != nil {
			return err
		}
	}
	return nil
}
func (i *Installer) ResolveTranslation(ctx context.Context) (TranslationPaths, error) {
	return i.ResolveTranslationDirection(ctx, "zh", "en")
}
func (i *Installer) ResolveTranslationDirection(ctx context.Context, source, target string) (TranslationPaths, error) {
	i.translationMu.Lock()
	defer i.translationMu.Unlock()
	t, engine, err := i.translationInputs()
	if err != nil {
		return TranslationPaths{}, err
	}
	relative := path.Join("runtime", "live-translation", t.Version, i.Platform.ID)
	root, err := i.runtimeDirectory(relative, false)
	if err != nil {
		return TranslationPaths{}, err
	}
	defer root.Close()
	if err := i.verifyTranslation(ctx, root, "engine", engine); err != nil {
		return TranslationPaths{}, err
	}
	prefix, directory, archive := "model", t.ModelDirectory, t.Archive
	if source == "en" && target == "zh" {
		prefix, directory, archive = "reverse-model", t.Reverse.Directory, t.Reverse.Archive
	} else if source != "zh" || target != "en" {
		return TranslationPaths{}, ErrManifest
	}
	if err := i.verifyTranslation(ctx, root, prefix, archive); err != nil {
		return TranslationPaths{}, err
	}
	base := filepath.Join(i.Root, filepath.FromSlash(relative))
	return TranslationPaths{Engine: filepath.Join(base, "engine"), Model: filepath.Join(base, prefix, directory)}, nil
}
