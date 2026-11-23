package managed

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"liberty.local/liberty/internal/domain"
)

func TestUnsupportedHostKeepsRuntimeUnavailableWithoutBorrowingTargetAssets(t *testing.T) {
	installer, err := NewInstaller(t.TempDir(), "unsupported")
	if err != nil {
		t.Fatal("non-desktop host cannot compose non-runtime services", err)
	}
	definition := installer.Definition()
	if definition.Platform != "unsupported" || definition.UnsupportedReason == "" || installer.Platform.Python != nil || installer.Platform.FFmpeg != nil {
		t.Fatal("non-desktop host borrowed supported runtime assets")
	}
	ctx := context.Background()
	if _, err := installer.InstallTool(ctx, "python", 1, "official", nil); !errors.Is(err, domain.ErrRuntimeUnsupported) {
		t.Fatal("unsupported host attempted managed installation", err)
	}
	if _, err := installer.Detect(ctx, "python", domain.RuntimeSettings{}, nil); !errors.Is(err, domain.ErrRuntimeUnsupported) {
		t.Fatal("unsupported host attempted runtime detection", err)
	}
	if err := installer.AcquireModels(ctx, 1, "official", nil); !errors.Is(err, domain.ErrRuntimeUnsupported) {
		t.Fatal("unsupported host attempted model download", err)
	}
	if _, err := NewInstaller(t.TempDir(), "misspelled-target"); !errors.Is(err, ErrManifest) {
		t.Fatal("unknown manifest target was accepted", err)
	}
	if _, err := NewInstallerWithProfile(t.TempDir(), "unsupported", SecurityCandidateProfile); !errors.Is(err, domain.ErrRuntimeUnsupported) {
		t.Fatal("unsupported host accepted a candidate runtime", err)
	}
}

func TestLegacyManagedPythonRequiresTheRecordedLockVersion(t *testing.T) {
	installer, err := NewInstaller(t.TempDir(), "darwin-aarch64")
	if err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(t.TempDir(), "python")
	if err := os.WriteFile(candidate, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	legacy := domain.RuntimeOverview{PythonPath: &candidate, RuntimeVersion: "old"}
	component := domain.NewRuntimeComponent("python", "managed", "")
	component, err = installer.Reconcile(component, legacy, domain.RuntimeSettings{})
	if err != nil || component.Artifact != nil || component.Operation.Phase != "version_outdated" {
		t.Fatalf("obsolete managed lock accepted: %+v, %v", component, err)
	}
	legacy.RuntimeVersion = installer.Manifest.RuntimeVersion
	component, err = installer.Reconcile(component, legacy, domain.RuntimeSettings{})
	if err != nil || component.Artifact != nil {
		t.Fatal("subsequent polling reactivated the old installation")
	}
}

func TestOriginalResourcesAndFourPlatformRestrictions(t *testing.T) {
	m, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Platforms) != 4 || m.PythonVersion != "3.11.18" || m.ProductionInputs == nil {
		t.Fatal("runtime targets or version drift")
	}
	x86, err := m.Platform("windows-x86")
	if err != nil || x86.UnsupportedReason == "" {
		t.Fatal("x86 limitation lost")
	}
	pins, err := modelPins()
	if err != nil {
		t.Fatal(err)
	}
	for _, pin := range pins {
		if !revisionPattern.MatchString(pin.Revision) {
			t.Fatal("model is not pinned")
		}
		relative, err := modelRelative(pin.ID)
		if err != nil || relative != "modelscope/models/"+pin.ID {
			t.Fatal("legacy model cache layout changed")
		}
	}
	root := t.TempDir()
	directory, err := MaterializeResources(root)
	if err != nil {
		t.Fatal(err)
	}
	for name, expected := range resources {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || string(data) != expected {
			t.Fatal("resource differs: " + name)
		}
	}
	if os.WriteFile(filepath.Join(directory, "runner.py"), []byte("tampered"), 0600) != nil {
		t.Fatal("tamper fixture")
	}
	if _, err := MaterializeResources(root); err == nil {
		t.Fatal("tampered embedded resource accepted")
	}
}
func TestManifestRejectsMutableMissingAndInvalidAssets(t *testing.T) {
	for _, mutation := range []func(*Manifest){
		func(m *Manifest) { m.Platforms[0].Python.SHA256 = "" },
		func(m *Manifest) {
			m.Platforms[0].Python.URLs["official"] = "https://example.invalid/latest/python.tar.gz"
		},
		func(m *Manifest) { m.Platforms[0].FFmpeg = nil },
		func(m *Manifest) { m.Platforms[0].PythonCandidates = []string{"../python"} },
		func(m *Manifest) { m.Sources[0].ModelEndpoint = "http://example.invalid" },
	} {
		m, err := LoadManifest()
		if err != nil {
			t.Fatal(err)
		}
		mutation(&m)
		data, _ := json.Marshal(m)
		if _, err := parseManifest(data); err == nil {
			t.Fatal("invalid manifest accepted")
		}
	}
}

func TestNjuPackageIndexUsesOfficialPythonArchives(t *testing.T) {
	manifest, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	source, err := manifest.Source("nju")
	if err != nil || source.PipIndex != "https://mirror.nju.edu.cn/pypi/web/simple" {
		t.Fatal("NJU dependency index is missing", err)
	}
	for _, platform := range manifest.Platforms {
		t.Run(platform.ID, func(t *testing.T) {
			if platform.Python == nil {
				if platform.UnsupportedReason != "" {
					return
				}
				t.Fatal("supported platform has no Python archive")
			}
			archive, err := platform.Python.URL(source.ID)
			if err != nil || archive != platform.Python.URLs["official"] {
				t.Fatal("NJU package selection did not resolve the official Python archive", err)
			}
		})
	}
}

func TestWindowsFFmpegArchivesMatchReviewedPins(t *testing.T) {
	manifest, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []struct {
		platform, name, url, sha256 string
	}{
		{"windows-x64", "ffmpeg-n8.1.3-27-g8dca1586f4-win64-gpl-8.1.zip", "https://github.com/FFmpeg-Builds/FFmpeg-Builds/releases/download/autobuild-2026-10-31-12-59/ffmpeg-n8.1.3-27-g8dca1586f4-win64-gpl-8.1.zip", "401dffcf14c301082e1fc03b0bc876b92ea676bfc521fc56e80a1537d137034f"},
		{"windows-x86", "ffmpeg-n8.1.3-27-g8dca1586f4-win32-gpl-8.1.zip", "https://github.com/defisym/FFmpeg-Builds-Win32/releases/download/autobuild-2026-10-31-16-58/ffmpeg-n8.1.3-27-g8dca1586f4-win32-gpl-8.1.zip", "480a387ac116f25dc60bff063d67033c49e363284e2fd84022d1937084f7721c"},
	} {
		t.Run(expected.platform, func(t *testing.T) {
			platform, err := manifest.Platform(expected.platform)
			if err != nil || platform.FFmpeg == nil || platform.FFmpeg.Name != expected.name || platform.FFmpeg.URLs["official"] != expected.url || platform.FFmpeg.SHA256 != expected.sha256 {
				t.Fatal("Windows FFmpeg artifact differs from the reviewed archive", err)
			}
		})
	}
}
