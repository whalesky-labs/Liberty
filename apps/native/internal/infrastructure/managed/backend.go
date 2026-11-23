package managed

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/diagnostics"
)

var runtimeLogMu sync.Mutex

func (i *Installer) Definition() application.RuntimeDefinition {
	return application.RuntimeDefinition{Platform: i.Platform.ID, Version: i.Manifest.RuntimeVersion, PythonVersion: i.Manifest.PythonVersion, ModelVersion: i.Manifest.ModelSetVersion, InstallRoot: i.PlatformRoot(), UnsupportedReason: i.Platform.UnsupportedReason}
}
func (i *Installer) CheckSource(source string) error { _, err := i.Manifest.Source(source); return err }
func (i *Installer) Sources() []domain.RuntimeSourceOption {
	options := make([]domain.RuntimeSourceOption, 0, len(i.Manifest.Sources))
	for _, s := range i.Manifest.Sources {
		options = append(options, domain.RuntimeSourceOption{ID: s.ID, Name: s.Name})
	}
	return options
}
func (i *Installer) Reconcile(state domain.RuntimeComponent, legacy domain.RuntimeOverview, settings domain.RuntimeSettings) (domain.RuntimeComponent, error) {
	if state.Operation.Generation == 0 && state.Artifact == nil && state.Operation.Kind == "idle" && (!i.candidateProfile() || state.SourceName() == "system") {
		candidate := ""
		version := i.Manifest.RuntimeVersion
		if state.Component == "model" {
			if legacy.ModelsRoot != nil {
				candidate = *legacy.ModelsRoot
			}
			version = i.Manifest.ModelSetVersion
		} else if state.SourceName() == "system" {
			version = "system"
			if state.Component == "python" {
				candidate = settings.PythonPath
			} else {
				candidate = settings.FFmpegPath
			}
		} else {
			if state.Component == "python" {
				version = legacy.RuntimeVersion
			}
			if legacy.InstallRoot != nil && !candidateRuntimePath(*legacy.InstallRoot) {
				if root, err := openUnlinkedDirectory(*legacy.InstallRoot); err == nil {
					candidates := i.Platform.PythonCandidates
					if state.Component == "ffmpeg" {
						candidates = i.Platform.FFmpegCandidates
					}
					candidate = findCandidate(root, *legacy.InstallRoot, candidates)
					root.Close()
				}
			}
			if candidate == "" && state.Component == "python" && legacy.PythonPath != nil {
				candidate = *legacy.PythonPath
			}
		}
		if artifactExists(candidate, state.Component) {
			state.Artifact = &domain.RuntimeArtifact{GenerationID: "legacy", Version: version, Path: candidate}
			state.Availability = "ready"
			state.Operation = domain.RuntimeOperation{Kind: "idle", Phase: "idle"}
		}
	}
	if state.Artifact != nil {
		expected := i.Manifest.RuntimeVersion
		if state.Component == "model" {
			expected = i.Manifest.ModelSetVersion
		} else if state.SourceName() == "system" {
			expected = state.Artifact.Version
		}
		exists := artifactExists(state.Artifact.Path, state.Component)
		bound := state.SourceName() == "system" || i.managedArtifactAllowed(state.Component, *state.Artifact)
		if !exists || state.Artifact.Version != expected || !bound {
			phase, reason := "artifact_missing", "runtime_artifact_missing"
			if exists {
				phase, reason = "version_outdated", "runtime_version_outdated"
			}
			if exists && state.Artifact.Version == expected && !bound {
				phase, reason = "artifact_unverified", "runtime_artifact_unverified"
			}
			state.Availability = "unavailable"
			state.Artifact = nil
			state.Operation.Kind = "failed"
			state.Operation.Phase = phase
			state.Operation.Progress = nil
			state.Operation.LastError = &reason
		} else if state.Component == "python" && state.SourceName() != "system" && i.Platform.UnsupportedReason == "" && !i.reconciledInterpreterValid(state.Artifact.Path) {
			state.Availability = "unavailable"
			state.Artifact = nil
			state.Operation.Kind = "failed"
			state.Operation.Phase = "interpreter_invalid"
			state.Operation.Progress = nil
			reason := "runtime_python_interpreter_invalid"
			state.Operation.LastError = &reason
		}
	}
	return state, nil
}

func (i *Installer) managedArtifactAllowed(component string, artifact domain.RuntimeArtifact) bool {
	if i.pinnedInputs() {
		return i.artifactBound(component, artifact)
	}
	resolved, err := filepath.EvalSymlinks(artifact.Path)
	if err != nil || candidateRuntimePath(artifact.Path) || candidateRuntimePath(resolved) {
		return false
	}
	if withinDirectory(i.Root, artifact.Path) {
		if _, err := resolvedArtifact(i.Root, artifact.Path, component == "model"); err != nil {
			return false
		}
	}
	for directory := filepath.Dir(resolved); ; directory = filepath.Dir(directory) {
		info, err := os.Lstat(filepath.Join(directory, "ready.json"))
		if err == nil {
			root, err := openUnlinkedDirectory(directory)
			if err != nil || info.Size() < 1 || info.Size() > 64*1024 {
				if root != nil {
					root.Close()
				}
				return false
			}
			data, readErr := readRegularFile(root, "ready.json", info.Size())
			root.Close()
			var marker readyMarker
			if readErr != nil || json.Unmarshal(data, &marker) != nil || (marker.RuntimeProfile != "" && marker.RuntimeProfile != ProductionRuntimeProfile) || marker.InputDigest != "" {
				return false
			}
			break
		} else if !os.IsNotExist(err) {
			return false
		}
		if filepath.Dir(directory) == directory {
			break
		}
	}
	if withinDirectory(filepath.Join(i.PlatformRoot(), "components"), artifact.Path) {
		return i.artifactBound(component, artifact)
	}
	return true
}

func candidateRuntimePath(filename string) bool {
	normalized := "/" + strings.ToLower(filepath.ToSlash(filepath.Clean(filename))) + "/"
	return strings.Contains(normalized, "/runtime/profiles/"+SecurityCandidateProfile+"/")
}
func artifactExists(candidate, component string) bool {
	if !filepath.IsAbs(candidate) {
		return false
	}
	info, err := os.Stat(candidate)
	if err != nil {
		return false
	}
	if component == "model" {
		return info.IsDir()
	}
	return info.Mode().IsRegular()
}
func systemCandidates(component string, settings domain.RuntimeSettings) []string {
	names := []string{"ffmpeg"}
	preferred := settings.FFmpegPath
	if component == "python" {
		names = []string{"python3.13", "python3.12", "python3.11", "python3.10", "python3.9", "python3", "python", "py"}
		preferred = settings.PythonPath
	}
	directories := filepath.SplitList(os.Getenv("PATH"))
	if runtime.GOOS == "darwin" {
		directories = append(directories, "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/opt/local/bin")
	}
	if runtime.GOOS == "windows" {
		for _, version := range []string{"313", "312", "311", "310", "39"} {
			if base := os.Getenv("LOCALAPPDATA"); base != "" {
				directories = append(directories, filepath.Join(base, "Programs", "Python", "Python"+version))
			}
			if base := os.Getenv("ProgramFiles"); base != "" {
				directories = append(directories, filepath.Join(base, "Python"+version))
			}
		}
		for _, extra := range []struct{ key, child string }{{"ProgramFiles", "ffmpeg/bin"}, {"ChocolateyInstall", "bin"}, {"USERPROFILE", "scoop/shims"}} {
			if base := os.Getenv(extra.key); base != "" {
				directories = append(directories, filepath.Join(base, filepath.FromSlash(extra.child)))
			}
		}
	}
	candidates := []string{}
	seen := map[string]bool{}
	appendPath := func(candidate string) {
		if filepath.IsAbs(candidate) && !seen[candidate] && artifactExists(candidate, component) {
			seen[candidate] = true
			candidates = append(candidates, candidate)
		}
	}
	appendPath(strings.TrimSpace(preferred))
	for _, name := range names {
		for _, directory := range directories {
			if !filepath.IsAbs(directory) {
				continue
			}
			candidate := filepath.Join(directory, name)
			appendPath(candidate)
			if runtime.GOOS == "windows" {
				appendPath(candidate + ".exe")
			}
		}
	}
	return candidates
}
func (i *Installer) Detect(ctx context.Context, component string, settings domain.RuntimeSettings, log func(string) error) (string, error) {
	if i.Platform.UnsupportedReason != "" {
		return "", domain.ErrRuntimeUnsupported
	}
	if component != "python" && component != "ffmpeg" {
		return "", domain.ErrRuntimeComponent
	}
	for _, candidate := range systemCandidates(component, settings) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if i.ValidateSystem(ctx, component, candidate, log) == nil {
			return candidate, nil
		}
	}
	return "", domain.ErrRuntime
}
func (i *Installer) AppendLog(component, text string) error {
	if domain.RuntimeKey(component, "managed") != nil {
		return domain.ErrRuntimeComponent
	}
	runtimeLogMu.Lock()
	defer runtimeLogMu.Unlock()
	directory := path.Join(i.platformRelative(), "logs")
	root, err := i.runtimeDirectory(directory, true)
	if err != nil {
		return ErrResources
	}
	defer root.Close()
	name := component + ".log"
	previous := []byte{}
	if info, err := root.Lstat(name); err == nil {
		if !info.Mode().IsRegular() || linkedFile(info) {
			return ErrResources
		}
		file, err := root.Open(name)
		if err != nil {
			return ErrResources
		}
		opened, statErr := file.Stat()
		if statErr != nil || !os.SameFile(info, opened) || !singleLinkFile(file) {
			file.Close()
			return ErrResources
		}
		size := min(info.Size(), int64(512*1024))
		previous = make([]byte, int(size))
		_, readErr := file.ReadAt(previous, info.Size()-size)
		file.Close()
		if readErr != nil && size > 0 {
			return ErrResources
		}
		if info.Size() > size {
			if index := strings.IndexByte(string(previous), '\n'); index >= 0 {
				previous = previous[index+1:]
			}
		}
	} else if !os.IsNotExist(err) {
		return ErrResources
	}
	next := append(previous, []byte(diagnostics.Text(text, 4096)+"\n")...)
	return atomicJSON(root, name, next)
}
func (i *Installer) Log(component string) (string, error) {
	if domain.RuntimeKey(component, "managed") != nil {
		return "", domain.ErrRuntimeComponent
	}
	runtimeLogMu.Lock()
	defer runtimeLogMu.Unlock()
	root, err := i.runtimeDirectory(path.Join(i.platformRelative(), "logs"), false)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", ErrResources
	}
	defer root.Close()
	name := component + ".log"
	info, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil || !info.Mode().IsRegular() || linkedFile(info) || info.Size() > 520*1024 {
		return "", ErrResources
	}
	data, err := readRegularFile(root, name, info.Size())
	if err != nil {
		return "", ErrResources
	}
	return string(data), nil
}
