//go:build runtime_acceptance

package managed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
	"liberty.local/liberty/internal/infrastructure/runner"
)

// This opt-in test installs a complete hash-locked candidate and then exercises
// real streaming inference through the Go process owner. It never opens a mic.
func TestPinnedFunASRUpgradeStreamingAcceptance(t *testing.T) {
	root, inputDirectory, baselineRoot, modelSeed := os.Getenv("LIBERTY_NATIVE_UPGRADE_ROOT"), os.Getenv("LIBERTY_NATIVE_UPGRADE_INPUTS"), os.Getenv("LIBERTY_NATIVE_UPGRADE_BASELINE"), os.Getenv("LIBERTY_NATIVE_UPGRADE_MODEL_SEED")
	for _, p := range []string{root, inputDirectory, baselineRoot, modelSeed} {
		if !filepath.IsAbs(p) {
			t.Fatal("explicit isolated acceptance inputs required")
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, ".native-funasr-upgrade-acceptance")); err != nil || string(data) != "isolated-funasr-upgrade-v1\n" {
		t.Fatal("isolated upgrade marker required")
	}
	if data, err := os.ReadFile(filepath.Join(baselineRoot, ".native-runtime-acceptance")); err != nil || string(data) != "isolated-runtime-acceptance-v1\n" {
		t.Fatal("pinned baseline acceptance cache required")
	}
	owner, err := profile.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	m, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "acceptance-host", "liberty.exe")
	if runtime.GOOS == "darwin" {
		executable = filepath.Join(root, "acceptance-host", "Contents", "MacOS", "liberty-native")
	}
	sidecar, err := runtimeInputDirectory(executable, runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	if os.MkdirAll(sidecar, 0700) != nil {
		t.Fatal("sidecar directory")
	}
	copyFile := func(from, to string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(to), 0700); err != nil {
			t.Fatal("acceptance directory")
		}
		src, err := os.Open(from)
		if err != nil {
			t.Fatal("pinned input missing")
		}
		defer src.Close()
		dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			t.Fatal("input copy")
		}
		_, err = io.Copy(dst, src)
		closeErr := dst.Close()
		if err != nil || closeErr != nil {
			t.Fatal("input copy")
		}
	}
	for _, file := range m.CandidateProfiles[FunASRUpgradeProfile].Files {
		copyFile(filepath.Join(inputDirectory, file.Name), filepath.Join(sidecar, file.Name))
	}
	i, err := newInstallerWithProfile(root, CurrentPlatform(), FunASRUpgradeProfile, m, executable, runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	base, err := NewInstaller(baselineRoot, CurrentPlatform())
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range []*Asset{i.Platform.Python, i.Platform.FFmpeg} {
		copyFile(filepath.Join(base.PlatformRoot(), "downloads", asset.Name), filepath.Join(i.PlatformRoot(), "downloads", asset.Name))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	source := os.Getenv("LIBERTY_NATIVE_UPGRADE_SOURCE")
	if source == "" {
		source = "official"
	}
	python, err := i.InstallTool(ctx, "python", 1, source, nil)
	if err != nil {
		t.Fatal("candidate Python install/validation", err)
	}
	ffmpeg, err := i.InstallTool(ctx, "ffmpeg", 1, source, nil)
	if err != nil {
		t.Fatal("candidate ffmpeg install/validation", err)
	}
	// Seed only verified public-model cache files into the isolated generation.
	// ValidateModels rehashes them and runs offline warmup with the new package.
	baselineModel := filepath.Join(base.PlatformRoot(), "components", "model", "generations", "1")
	upgradeModel, err := i.generation("model", 1)
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(baselineModel, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(baselineModel, name)
		if err != nil {
			return err
		}
		if relative == "." || relative == "ready.json" {
			return nil
		}
		target := filepath.Join(upgradeModel, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !entry.Type().IsRegular() {
			return ErrAsset
		}
		copyFile(name, target)
		return nil
	})
	if err != nil {
		t.Fatal("public model seed copy", err)
	}
	offline, err := i.ValidateModels(ctx, 1, python.Path, nil)
	if err != nil {
		t.Fatal("candidate offline four-model warmup", err)
	}
	var version string
	err = runner.RunCommand(ctx, python.Path, []string{"-I", "-c", "from importlib.metadata import version; print(version('funasr'))"}, root, nil, func(line string) error { version = strings.TrimSpace(line); return nil })
	if err != nil || version != "1.4.29+liberty.local1" {
		t.Fatal("candidate did not select the pinned package", err)
	}
	for _, file := range i.Manifest.LiveASR.Files {
		copyFile(filepath.Join(modelSeed, file.Path), filepath.Join(root, "runtime", "live-models", i.Manifest.LiveASR.Version, file.Path))
	}
	if err := i.PrepareLiveModel(ctx, source, nil); err != nil {
		t.Fatal("live model hash verification", err)
	}
	model, err := i.ResolveLiveModel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ready struct {
		Generation int64  `json:"generation"`
		Version    string `json:"artifactVersion"`
		Path       string `json:"resolvedPath"`
	}
	data, err := os.ReadFile(filepath.Join(base.PlatformRoot(), "components", "model", "generations", "1", "ready.json"))
	if err != nil || json.Unmarshal(data, &ready) != nil {
		t.Fatal("pinned baseline model unavailable")
	}
	paths, err := base.ResolveModels(ctx, domain.RuntimeArtifact{GenerationID: "1", Version: ready.Version, Path: ready.Path})
	if err != nil {
		t.Fatal("baseline model validation", err)
	}
	input := filepath.Join(modelSeed, "example", "asr_example.wav")
	data, err = os.ReadFile(input)
	if err != nil {
		t.Fatal("public fixture unavailable")
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != "a1bd32dc78493c123f9625a66deee562aed2895f53fbc39f2cca3be7e6f4f20f" {
		t.Fatal("public fixture changed")
	}
	frames := make(chan domain.LiveAudio, 50)
	fed := make(chan error, 1)
	partials, finals := 0, 0
	err = (runner.LiveCommand{Python: python.Path, Script: filepath.Join(i.Resources, "live_runner.py"), Model: model, VAD: paths.VAD, Directory: root, Threads: 2}).Run(ctx, frames, func(event domain.LiveRecognitionEvent) error {
		switch event.Type {
		case "ready":
			go func() {
				var offset int64
				err := runner.CapturePCM(ctx, ffmpeg.Path, []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-i", input, "-vn", "-ac", "1", "-ar", "16000", "-f", "s16le", "pipe:1"}, root, func(pcm []byte) error {
					select {
					case frames <- domain.LiveAudio{Kind: "audio", OffsetMS: offset, PCM: pcm}:
						offset += int64(len(pcm) / 32)
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
				if err == nil {
					select {
					case frames <- domain.LiveAudio{Kind: "stop", OffsetMS: offset}:
					case <-ctx.Done():
						err = ctx.Err()
					}
				}
				fed <- err
			}()
		case "partial":
			partials++
		case "final":
			if strings.TrimSpace(event.Text) == "" {
				return domain.ErrLiveProtocol
			}
			finals++
		}
		return nil
	})
	if err != nil || partials < 1 || finals != 1 {
		t.Fatal("real streaming inference did not produce partial and final text", partials, finals, err)
	}
	if err := <-fed; err != nil {
		t.Fatal("fixture PCM transport", err)
	}
	if owner.Close() != nil {
		t.Fatal("acceptance ownership transfer")
	}
	store, _, err := jobdb.OpenPrepared(ctx, filepath.Join(root, "liberty.sqlite3"), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for component, artifact := range map[string]domain.RuntimeArtifact{"python": python, "ffmpeg": ffmpeg, "model": offline} {
		state, err := store.BeginRuntime(ctx, CurrentPlatform(), component, "managed", "validating", "validating", false)
		if err != nil {
			t.Fatal(err)
		}
		state.Artifact = &artifact
		state.Availability = "ready"
		state.Operation.Kind = "idle"
		state.Operation.Phase = "ready"
		if err := store.UpdateRuntime(ctx, CurrentPlatform(), state); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("Pinned FunASR 1.4.29 candidate installed; real Go-owned streaming runner produced partials and a final. Quality and hardware microphone acceptance are separate.")
}
