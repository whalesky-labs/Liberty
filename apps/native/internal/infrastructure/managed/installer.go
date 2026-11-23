package managed

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/runner"
)

type CommandRunner interface {
	Run(context.Context, string, []string, string, map[string]string, func(string) error) error
}
type Commands struct {
	installer *Installer
}

func (commands Commands) Run(ctx context.Context, exe string, args []string, dir string, env map[string]string, log func(string) error) (result error) {
	if commands.installer == nil || !commands.installer.pinnedInputs() {
		return runner.RunCommand(ctx, exe, args, dir, env, log)
	}
	i := commands.installer
	snapshot := resourceContents(i.inputs)
	if vendor := i.productionVendor.Load(); vendor != nil {
		for name, content := range *vendor {
			snapshot[name] = content
		}
	}
	if err := i.verifyResources(); err != nil {
		return err
	}
	parent, err := i.runtimeDirectory(path.Join(i.platformRelative(), "install-inputs"), true)
	if err != nil {
		return err
	}
	defer parent.Close()
	directory, cleanup, err := commandInputSnapshot(ctx, parent, snapshot)
	if err != nil {
		return err
	}
	defer func() {
		if cleanup() != nil {
			result = ErrResources
		}
	}()
	actualArgs := append([]string(nil), args...)
	for index, argument := range actualArgs {
		if argument == i.Resources {
			actualArgs[index] = directory
		} else if withinDirectory(i.Resources, argument) {
			relative, err := filepath.Rel(i.Resources, argument)
			if err != nil || !safeName(relative) {
				return ErrResources
			}
			actualArgs[index] = filepath.Join(directory, relative)
		}
	}
	if dir == i.Resources {
		dir = directory
	}
	if err := verifyResourceSnapshot(directory, snapshot); err != nil {
		return err
	}
	err = runner.RunCommand(ctx, exe, actualArgs, dir, env, log)
	if verifyResourceSnapshot(directory, snapshot) != nil || i.verifyResources() != nil {
		return ErrResources
	}
	return err
}

type Installer struct {
	Root             string
	Manifest         Manifest
	Platform         Platform
	Resources        string
	Download         *Downloader
	Commands         CommandRunner
	RuntimeProfile   string
	InputDigest      string
	inputs           map[string][]byte
	interpreterMu    sync.Mutex
	interpreterOK    map[string]interpreterFingerprint
	vendorBuildMu    sync.Mutex
	productionVendor atomic.Pointer[map[string][]byte]
}

type interpreterFingerprint struct {
	info   os.FileInfo
	digest [sha256.Size]byte
}

func NewInstaller(rootDirectory, platform string) (*Installer, error) {
	return NewInstallerWithProfile(rootDirectory, platform, ProductionRuntimeProfile)
}

func NewInstallerWithProfile(rootDirectory, platform, profileID string) (*Installer, error) {
	profileID, err := runtimeProfileID(profileID)
	if err != nil {
		return nil, err
	}
	manifest, err := LoadManifest()
	if err != nil {
		return nil, err
	}
	executable := ""
	if profileID != ProductionRuntimeProfile {
		executable, err = os.Executable()
		if err != nil {
			return nil, ErrResources
		}
	}
	return newInstallerWithProfile(rootDirectory, platform, profileID, manifest, executable, runtime.GOOS)
}

func newInstallerWithProfile(rootDirectory, platform, profileID string, manifest Manifest, executable, operatingSystem string) (*Installer, error) {
	profileID, err := runtimeProfileID(profileID)
	if err != nil {
		return nil, err
	}
	manifest, inputs, inputDigest, err := loadRuntimeInputs(manifest, platform, profileID, executable, operatingSystem)
	if err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(rootDirectory)
	if err != nil {
		return nil, ErrResources
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, ErrResources
	}
	root, err := openUnlinkedDirectory(absolute)
	if err != nil {
		return nil, ErrResources
	}
	root.Close()
	target, err := manifest.Platform(platform)
	if err != nil && platform == "unsupported" && profileID == ProductionRuntimeProfile {
		// Non-desktop hosts may use repositories and remote services, but must
		// expose an unavailable runtime without borrowing another target's assets.
		target = Platform{ID: platform, UnsupportedReason: "runtime_platform_unsupported"}
		err = nil
	}
	if err != nil {
		return nil, err
	}
	resourceDirectory, err := materializeResources(absolute, inputs)
	if err != nil {
		return nil, err
	}
	installer := &Installer{Root: absolute, Manifest: manifest, Platform: target, Resources: resourceDirectory, Download: NewDownloader(), RuntimeProfile: profileID, InputDigest: inputDigest, inputs: inputs}
	installer.Commands = Commands{installer: installer}
	return installer, nil
}
func (i *Installer) candidateProfile() bool { return i.RuntimeProfile == SecurityCandidateProfile }
func (i *Installer) pinnedInputs() bool {
	return i.candidateProfile() || i.Manifest.ProductionInputs != nil
}
func (i *Installer) platformRelative() string {
	if i.candidateProfile() {
		return path.Join("runtime", "profiles", i.RuntimeProfile, i.Platform.ID)
	}
	return path.Join("runtime", i.Platform.ID)
}
func (i *Installer) PlatformRoot() string {
	return filepath.Join(i.Root, filepath.FromSlash(i.platformRelative()))
}
func (i *Installer) generation(component string, generation int64) (string, error) {
	relative, err := i.generationRelative(component, generation)
	if err != nil {
		return "", err
	}
	root, err := i.runtimeDirectory(relative, true)
	if err != nil {
		return "", ErrResources
	}
	defer root.Close()
	return filepath.Join(i.Root, filepath.FromSlash(relative)), nil
}
func (i *Installer) generationRelative(component string, generation int64) (string, error) {
	if domain.RuntimeKey(component, "managed") != nil || generation < 1 {
		return "", domain.ErrRuntimeComponent
	}
	return path.Join(i.platformRelative(), "components", component, "generations", strconv.FormatInt(generation, 10)), nil
}
func (i *Installer) openGeneration(component string, generation int64) (*os.Root, error) {
	relative, err := i.generationRelative(component, generation)
	if err != nil {
		return nil, err
	}
	return i.runtimeDirectory(relative, false)
}
func (i *Installer) InstallTool(ctx context.Context, component string, generation int64, sourceID string, log func(string) error) (domain.RuntimeArtifact, error) {
	if err := ctx.Err(); err != nil {
		return domain.RuntimeArtifact{}, err
	}
	if i.Platform.UnsupportedReason != "" {
		return domain.RuntimeArtifact{}, domain.ErrRuntimeUnsupported
	}
	if component != "python" && component != "ffmpeg" {
		return domain.RuntimeArtifact{}, domain.ErrRuntimeComponent
	}
	if i.pinnedInputs() {
		if i.verifyResources() != nil {
			return domain.RuntimeArtifact{}, ErrResources
		}
	}
	source, err := i.Manifest.Source(sourceID)
	if err != nil {
		return domain.RuntimeArtifact{}, err
	}
	directory, err := i.generation(component, generation)
	if err != nil {
		return domain.RuntimeArtifact{}, err
	}
	asset, candidates := i.Platform.Python, i.Platform.PythonCandidates
	if component == "ffmpeg" {
		asset, candidates = i.Platform.FFmpeg, i.Platform.FFmpegCandidates
	}
	if asset == nil {
		return domain.RuntimeArtifact{}, ErrAsset
	}
	root, err := i.openGeneration(component, generation)
	if err != nil {
		return domain.RuntimeArtifact{}, ErrResources
	}
	defer root.Close()
	executable, err := findManagedCandidate(root, directory, candidates)
	if err != nil {
		return domain.RuntimeArtifact{}, err
	}
	if executable != "" {
		if _, err := root.Lstat("ready.json"); err == nil {
			artifact := domain.RuntimeArtifact{GenerationID: strconv.FormatInt(generation, 10), Version: i.Manifest.RuntimeVersion, Path: executable}
			if !i.artifactBound(component, artifact) {
				return domain.RuntimeArtifact{}, ErrResources
			}
		} else if !os.IsNotExist(err) {
			return domain.RuntimeArtifact{}, ErrResources
		}
	}
	if component == "python" && executable != "" {
		if err := i.validateInterpreter(ctx, executable, log); err != nil {
			if ctx.Err() != nil {
				return domain.RuntimeArtifact{}, ctx.Err()
			}
			executable = ""
		}
	}
	if executable != "" {
		if i.validate(ctx, component, executable, true, log) == nil {
			return i.publish(root, component, generation, executable, i.Manifest.RuntimeVersion)
		}
		if ctx.Err() != nil {
			return domain.RuntimeArtifact{}, ctx.Err()
		}
	}
	if component == "ffmpeg" {
		// Unlike Python, a failed FFmpeg validation cannot be repaired by pip.
		// Re-extract the verified asset instead of validating the broken file again.
		executable = ""
	}
	if executable == "" {
		downloads, err := i.downloads()
		if err != nil {
			return domain.RuntimeArtifact{}, err
		}
		defer downloads.Close()
		address, err := asset.URL(source.ID)
		if err != nil {
			return domain.RuntimeArtifact{}, err
		}
		if info, err := downloads.Lstat(asset.Name); err == nil {
			if !info.Mode().IsRegular() || linkedFile(info) {
				return domain.RuntimeArtifact{}, ErrResources
			}
			file, openErr := downloads.Open(asset.Name)
			if openErr != nil {
				return domain.RuntimeArtifact{}, ErrResources
			}
			opened, statErr := file.Stat()
			valid := statErr == nil && os.SameFile(info, opened) && singleLinkFile(file)
			closeErr := file.Close()
			if !valid || closeErr != nil {
				return domain.RuntimeArtifact{}, ErrResources
			}
		} else if !os.IsNotExist(err) {
			return domain.RuntimeArtifact{}, ErrResources
		}
		if err := i.Download.Fetch(ctx, downloads, asset.Name, address, asset.SHA256, nil, func(done, total int64) error {
			if log != nil {
				return log("runtime_download_bytes=" + strconv.FormatInt(done, 10))
			}
			return nil
		}); err != nil {
			return domain.RuntimeArtifact{}, err
		}
		stage := component + ".stage"
		if err := clearDirectory(root, stage); err != nil {
			return domain.RuntimeArtifact{}, err
		}
		if err := Extract(ctx, filepath.Join(i.PlatformRoot(), "downloads", asset.Name), root, stage); err != nil {
			return domain.RuntimeArtifact{}, err
		}
		stageRoot, err := openPlainChild(root, stage, false)
		if err != nil {
			return domain.RuntimeArtifact{}, ErrArchive
		}
		marker := findExecutable(stageRoot, component)
		stageRoot.Close()
		if marker == "" {
			return domain.RuntimeArtifact{}, ErrArchive
		}
		parts := strings.Split(marker, "/")
		sourceRoot := stage
		if len(parts) > 1 {
			sourceRoot = path.Join(stage, parts[0])
		}
		if err := clearDirectory(root, component); err != nil {
			return domain.RuntimeArtifact{}, err
		}
		if root.Rename(sourceRoot, component) != nil {
			return domain.RuntimeArtifact{}, ErrArchive
		}
		if sourceRoot != stage {
			_ = root.RemoveAll(stage)
		}
		executable, err = findManagedCandidate(root, directory, candidates)
		if err != nil {
			return domain.RuntimeArtifact{}, err
		}
		if executable == "" {
			return domain.RuntimeArtifact{}, ErrArchive
		}
		if component == "python" {
			if err := i.validateInterpreter(ctx, executable, log); err != nil {
				return domain.RuntimeArtifact{}, err
			}
		}
	}
	if component == "python" {
		if err := i.ensureProductionVendor(ctx, executable, log); err != nil {
			return domain.RuntimeArtifact{}, err
		}
		lock := "requirements-lock-" + i.Platform.ID + ".txt"
		if _, ok := resources[lock]; !ok {
			return domain.RuntimeArtifact{}, domain.ErrRuntimeUnsupported
		}
		args := []string{"-m", "pip", "install", "--disable-pip-version-check", "--no-input", "--require-hashes", "--no-build-isolation"}
		if i.pinnedInputs() {
			args = []string{"-I", "-m", "pip", "--isolated", "install", "--disable-pip-version-check", "--no-input", "--require-hashes", "--no-build-isolation", "--no-cache-dir", "--find-links", i.Resources}
		}
		if source.PipIndex != "" {
			args = append(args, "-i", source.PipIndex)
		}
		args = append(args, "-r", filepath.Join(i.Resources, lock))
		runCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		err := i.runCommand(runCtx, executable, args, directory, log)
		cancel()
		if err != nil {
			return domain.RuntimeArtifact{}, err
		}
	}
	if err := i.validate(ctx, component, executable, true, log); err != nil {
		return domain.RuntimeArtifact{}, err
	}
	return i.publish(root, component, generation, executable, i.Manifest.RuntimeVersion)
}
func (i *Installer) environment() map[string]string {
	environment := map[string]string{"PYTHONUTF8": "1", "PYTHONUNBUFFERED": "1", "LIBERTY_ASR_BACKEND": i.Platform.Backend}
	if i.pinnedInputs() {
		environment["PIP_CONFIG_FILE"] = os.DevNull
		for _, entry := range os.Environ() {
			key, _, _ := strings.Cut(entry, "=")
			if strings.HasPrefix(strings.ToUpper(key), "PIP_") && !strings.EqualFold(key, "PIP_CONFIG_FILE") {
				environment[key] = ""
			}
		}
	}
	return environment
}
func (i *Installer) validateInterpreter(ctx context.Context, executable string, log func(string) error) error {
	if !filepath.IsAbs(executable) {
		return ErrResources
	}
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := []string{"-I", filepath.Join(i.Resources, "runtime_validate.py"), "--python-version", i.Manifest.PythonVersion, "--interpreter-only"}
	return i.runCommand(runCtx, executable, args, i.Resources, log)
}

func (i *Installer) reconciledInterpreterValid(executable string) bool {
	fingerprint, ok := interpreterFingerprintFor(executable)
	if !ok {
		return false
	}

	i.interpreterMu.Lock()
	cached, cachedOK := i.interpreterOK[executable]
	i.interpreterMu.Unlock()
	if cachedOK && sameInterpreterFingerprint(cached, fingerprint) {
		return true
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	err := i.validateInterpreter(ctx, executable, nil)
	cancel()
	if err != nil {
		i.interpreterMu.Lock()
		delete(i.interpreterOK, executable)
		i.interpreterMu.Unlock()
		return false
	}
	current, ok := interpreterFingerprintFor(executable)
	if !ok || !sameInterpreterFingerprint(fingerprint, current) {
		return false
	}
	i.interpreterMu.Lock()
	if i.interpreterOK == nil {
		i.interpreterOK = make(map[string]interpreterFingerprint)
	}
	i.interpreterOK[executable] = current
	i.interpreterMu.Unlock()
	return true
}

func interpreterFingerprintFor(executable string) (interpreterFingerprint, bool) {
	info, err := os.Stat(executable)
	if err != nil || !info.Mode().IsRegular() {
		return interpreterFingerprint{}, false
	}
	file, err := os.Open(executable)
	if err != nil {
		return interpreterFingerprint{}, false
	}
	checksum := sha256.New()
	_, copyErr := io.Copy(checksum, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return interpreterFingerprint{}, false
	}
	after, err := os.Stat(executable)
	if err != nil || !sameInterpreterMetadata(info, after) {
		return interpreterFingerprint{}, false
	}
	var digest [sha256.Size]byte
	copy(digest[:], checksum.Sum(nil))
	return interpreterFingerprint{info: after, digest: digest}, true
}

func sameInterpreterMetadata(left, right os.FileInfo) bool {
	return os.SameFile(left, right) && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}

func sameInterpreterFingerprint(left, right interpreterFingerprint) bool {
	return sameInterpreterMetadata(left.info, right.info) && left.digest == right.digest
}

func (i *Installer) validate(ctx context.Context, component, executable string, managed bool, log func(string) error) error {
	if !filepath.IsAbs(executable) {
		return ErrResources
	}
	timeout := 15 * time.Second
	args := []string{"-hide_banner", "-version"}
	if component == "python" {
		timeout = 90 * time.Second
		args = []string{filepath.Join(i.Resources, "runtime_validate.py")}
		if managed {
			args = append([]string{"-I"}, args...)
			args = append(args, "--python-version", i.Manifest.PythonVersion, "--lock", filepath.Join(i.Resources, "requirements-lock-"+i.Platform.ID+".txt"))
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if managed {
		return i.runCommand(runCtx, executable, args, i.Resources, log)
	}
	return i.Commands.Run(runCtx, executable, args, i.Resources, i.environment(), log)
}

func (i *Installer) verifyResources() error {
	if !i.pinnedInputs() {
		return nil
	}
	if !validHash(i.InputDigest) || resourceDigest(i.inputs) != i.InputDigest {
		return ErrResources
	}
	return verifyResourceSnapshot(i.Resources, resourceContents(i.inputs))
}

func (i *Installer) runCommand(ctx context.Context, executable string, args []string, directory string, log func(string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := i.verifyResources(); err != nil {
		return err
	}
	if i.candidateProfile() || withinDirectory(i.Root, executable) {
		if _, err := resolvedArtifact(i.PlatformRoot(), executable, false); err != nil {
			return ErrResources
		}
	}
	err := i.Commands.Run(ctx, executable, args, directory, i.environment(), log)
	if i.verifyResources() != nil {
		return ErrResources
	}
	return err
}
func (i *Installer) ValidateSystem(ctx context.Context, component, executable string, log func(string) error) error {
	if component != "python" && component != "ffmpeg" {
		return domain.ErrRuntimeComponent
	}
	return i.validate(ctx, component, executable, false, log)
}
func (i *Installer) downloads() (*os.Root, error) {
	relative := path.Join(i.platformRelative(), "downloads")
	return i.runtimeDirectory(relative, true)
}
func clearDirectory(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || linkedFile(info) {
		return ErrResources
	}
	if root.RemoveAll(name) != nil {
		return ErrResources
	}
	return nil
}
func findCandidate(root *os.Root, directory string, candidates []string) string {
	executable, _ := findManagedCandidate(root, directory, candidates)
	return executable
}

func findManagedCandidate(root *os.Root, directory string, candidates []string) (string, error) {
	for _, candidate := range candidates {
		resolved, err := resolvedArtifact(directory, filepath.Join(directory, filepath.FromSlash(candidate)), false)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", ErrResources
		}
		info, err := os.Lstat(resolved)
		if err == nil && info.Mode().IsRegular() {
			if info.Mode()&0111 == 0 {
				relative, err := filepath.Rel(directory, resolved)
				if err != nil || root.Chmod(relative, info.Mode().Perm()|0755) != nil {
					continue
				}
			}
			return resolved, nil
		}
	}
	return "", nil
}
func findExecutable(root *os.Root, component string) string {
	names := map[string]bool{component: true, component + ".exe": true}
	if component == "python" {
		names["python3"] = true
		names["python3.exe"] = true
	}
	found := ""
	count := 0
	_ = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fs.SkipDir
		}
		count++
		if count > maxEntries {
			return fs.SkipAll
		}
		if names[strings.ToLower(entry.Name())] && !entry.IsDir() {
			info, err := root.Stat(name)
			if err == nil && info.Mode().IsRegular() {
				found = name
				return fs.SkipAll
			}
		}
		return nil
	})
	return found
}

type readyMarker struct {
	Component      string `json:"component"`
	Generation     int64  `json:"generation"`
	Version        string `json:"artifactVersion"`
	Path           string `json:"resolvedPath"`
	Profile        string `json:"modelProfile"`
	Completed      string `json:"completedAt"`
	RuntimeProfile string `json:"runtimeProfile"`
	InputDigest    string `json:"runtimeInputSha256,omitempty"`
}

func (i *Installer) publish(root *os.Root, component string, generation int64, resolved, version string) (domain.RuntimeArtifact, error) {
	artifact := domain.RuntimeArtifact{GenerationID: strconv.FormatInt(generation, 10), Version: version, Path: resolved}
	owned, err := i.openGeneration(component, generation)
	if err != nil {
		return artifact, ErrResources
	}
	defer owned.Close()
	expectedInfo, expectedErr := owned.Stat(".")
	actualInfo, actualErr := root.Stat(".")
	if expectedErr != nil || actualErr != nil || !os.SameFile(expectedInfo, actualInfo) || i.verifyResources() != nil {
		return artifact, ErrResources
	}
	if _, err := resolvedArtifact(owned.Name(), resolved, component == "model"); err != nil {
		return artifact, ErrResources
	}
	profileID := i.RuntimeProfile
	if profileID == "" {
		profileID = ProductionRuntimeProfile
	}
	marker := readyMarker{Component: component, Generation: generation, Version: version, Path: resolved, Profile: i.Manifest.ModelProfile, Completed: strconv.FormatInt(time.Now().UnixMilli(), 10), RuntimeProfile: profileID, InputDigest: i.InputDigest}
	data, err := json.Marshal(marker)
	if err != nil {
		return artifact, ErrResources
	}
	if err := atomicJSON(root, "ready.json", data); err != nil {
		return artifact, err
	}
	return artifact, nil
}

func (i *Installer) artifactBound(component string, artifact domain.RuntimeArtifact) bool {
	generation, err := strconv.ParseInt(artifact.GenerationID, 10, 64)
	if err != nil || generation < 1 || strconv.FormatInt(generation, 10) != artifact.GenerationID || (i.candidateProfile() && !validHash(i.InputDigest)) {
		return false
	}
	root, err := i.openGeneration(component, generation)
	if err != nil {
		return false
	}
	defer root.Close()
	info, err := root.Lstat("ready.json")
	if err != nil || info.Size() < 1 || info.Size() > 64*1024 {
		return false
	}
	data, err := readRegularFile(root, "ready.json", info.Size())
	var marker readyMarker
	if err != nil || json.Unmarshal(data, &marker) != nil || marker.Component != component || marker.Generation != generation || marker.Version != artifact.Version || marker.Profile != i.Manifest.ModelProfile || marker.InputDigest != i.InputDigest || (marker.RuntimeProfile != i.RuntimeProfile && (i.candidateProfile() || marker.RuntimeProfile != "")) {
		return false
	}
	directory := filepath.Join(i.PlatformRoot(), "components", component, "generations", artifact.GenerationID)
	actual, err := resolvedArtifact(directory, artifact.Path, component == "model")
	marked, markerErr := resolvedArtifact(directory, marker.Path, component == "model")
	return err == nil && markerErr == nil && actual == marked
}
func atomicJSON(root *os.Root, name string, data []byte) error {
	temporary := "." + name + ".tmp"
	// This file is exclusive to a component's active operation under profile lock.
	if err := root.Remove(temporary); err != nil && !os.IsNotExist(err) {
		return ErrResources
	}
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrResources
	}
	defer root.Remove(temporary)
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return ErrResources
	}
	if root.Rename(temporary, name) != nil {
		return ErrResources
	}
	return nil
}
