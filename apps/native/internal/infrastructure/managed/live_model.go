package managed

import (
	"context"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type LiveModel struct {
	Version  string      `json:"modelSetVersion"`
	ID       string      `json:"modelId"`
	Revision string      `json:"revision"`
	License  string      `json:"license"`
	Files    []ModelFile `json:"files"`
}

func validLiveModel(m LiveModel) bool {
	if !safeName(m.Version) || !revisionPattern.MatchString(m.Revision) || m.License != "Apache-2.0" || len(m.Files) != 7 {
		return false
	}
	if _, err := modelRelative(m.ID); err != nil {
		return false
	}
	names := map[string]bool{"README.md": false, "am.mvn": false, "config.yaml": false, "configuration.json": false, "model.pt": false, "seg_dict": false, "tokens.json": false}
	for _, f := range m.Files {
		seen, ok := names[f.Path]
		if !ok || seen || !revisionPattern.MatchString(f.Revision) || !validHash(f.SHA256) || f.Size <= 0 || f.Size > 1024*1024*1024 {
			return false
		}
		names[f.Path] = true
	}
	return true
}

// This optional model has its own immutable directory; the offline four-model
// set and its ready marker are never overwritten by a live-model installation.
func (i *Installer) PrepareLiveModel(ctx context.Context, sourceID string, progress func(string)) error {
	i.liveModelMu.Lock()
	defer i.liveModelMu.Unlock()
	m := i.Manifest.LiveASR
	if m == nil || !validLiveModel(*m) {
		return ErrManifest
	}
	source, err := i.Manifest.Source(sourceID)
	if err != nil {
		return err
	}
	root, err := i.runtimeDirectory(path.Join("runtime", "live-models", m.Version), true)
	if err != nil {
		return err
	}
	defer root.Close()
	i.liveModelFiles = nil
	for _, f := range m.Files {
		if progress != nil {
			progress("live_model_downloading")
		}
		address := strings.TrimRight(source.ModelEndpoint, "/") + "/api/v1/models/" + m.ID + "/repo?" + url.Values{"Revision": {f.Revision}, "FilePath": {f.Path}}.Encode()
		if err := i.Download.Fetch(ctx, root, f.Path, address, f.SHA256, &f.Size, nil); err != nil {
			return err
		}
	}
	return i.verifyLiveModel(ctx, root)
}

func (i *Installer) verifyLiveModel(ctx context.Context, root *os.Root) error {
	m := i.Manifest.LiveASR
	if m == nil || !validLiveModel(*m) {
		return ErrManifest
	}
	next := map[string]os.FileInfo{}
	for _, f := range m.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := root.Lstat(f.Path)
		if err != nil || !info.Mode().IsRegular() || linkedFile(info) || info.Size() != f.Size {
			return ErrChecksum
		}
		old := i.liveModelFiles[f.Path]
		if old == nil || !os.SameFile(old, info) || old.Size() != info.Size() || !old.ModTime().Equal(info.ModTime()) {
			if !checksum(root, f.Path, f.SHA256, &f.Size) {
				return ErrChecksum
			}
			after, e := root.Lstat(f.Path)
			if e != nil || !os.SameFile(info, after) || !info.ModTime().Equal(after.ModTime()) || after.Size() != info.Size() {
				return ErrChecksum
			}
		}
		next[f.Path] = info
	}
	i.liveModelFiles = next
	return nil
}

func (i *Installer) ResolveLiveModel(ctx context.Context) (string, error) {
	i.liveModelMu.Lock()
	defer i.liveModelMu.Unlock()
	m := i.Manifest.LiveASR
	if m == nil {
		return "", ErrManifest
	}
	relative := path.Join("runtime", "live-models", m.Version)
	root, err := i.runtimeDirectory(relative, false)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err := i.verifyLiveModel(ctx, root); err != nil {
		return "", err
	}
	return filepath.Join(i.Root, filepath.FromSlash(relative)), nil
}
