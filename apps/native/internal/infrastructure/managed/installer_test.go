package managed

import (
	"archive/tar"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

type commandFunc func(context.Context, string, []string, string, map[string]string, func(string) error) error

func (f commandFunc) Run(c context.Context, e string, a []string, d string, v map[string]string, l func(string) error) error {
	return f(c, e, a, d, v, l)
}
func testInstaller(t *testing.T, platform, component, archive string) (*Installer, *atomic.Int32) {
	t.Helper()
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	requests := &atomic.Int32{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = w.Write(data) }))
	t.Cleanup(server.Close)
	i, err := NewInstaller(t.TempDir(), platform)
	if err != nil {
		t.Fatal(err)
	}
	// This harness tests archive/probe/retry mechanics with an injected command
	// runner. Production vendoring and isolated pip are covered separately by
	// TestProductionVendorInstallation* using the actual pinned-input boundary.
	i.Manifest.ProductionInputs = nil
	i.inputs, i.InputDigest = nil, ""
	i.Download = &Downloader{client: server.Client()}
	a := &Asset{Name: filepath.Base(archive), SHA256: digest(string(data)), URLs: map[string]string{"official": server.URL + "/pinned/asset"}}
	if component == "python" {
		i.Platform.Python = a
	} else {
		i.Platform.FFmpeg = a
	}
	return i, requests
}
func TestPythonInstallerUsesPlatformHashLockAndValidatesBeforePublish(t *testing.T) {
	for _, platform := range []string{"darwin-aarch64", "darwin-x64", "windows-x64"} {
		t.Run(platform, func(t *testing.T) {
			name := "python/bin/python3"
			if platform == "windows-x64" {
				name = "python/python.exe"
			}
			archive := tarFixture(t, []*tar.Header{{Name: name, Typeflag: tar.TypeReg, Size: 7, Mode: 0755}})
			i, requests := testInstaller(t, platform, "python", archive)
			calls := 0
			i.Commands = commandFunc(func(ctx context.Context, exe string, args []string, dir string, env map[string]string, _ func(string) error) error {
				calls++
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("installer command has no deadline")
				}
				if !filepath.IsAbs(exe) || !filepath.IsAbs(dir) || env["PYTHONUTF8"] != "1" || env["LIBERTY_ASR_BACKEND"] != "funasr" {
					t.Fatal("invalid invocation")
				}
				if calls == 1 || calls == 4 {
					want := []string{"-I", filepath.Join(i.Resources, "runtime_validate.py"), "--python-version", i.Manifest.PythonVersion, "--interpreter-only"}
					if !reflect.DeepEqual(args, want) {
						t.Fatal("interpreter was not verified before install or reuse")
					}
				} else if calls == 2 {
					want := []string{"-m", "pip", "install", "--disable-pip-version-check", "--no-input", "--require-hashes", "--no-build-isolation", "-i", "https://pypi.org/simple", "-r", filepath.Join(i.Resources, "requirements-lock-"+platform+".txt")}
					if !reflect.DeepEqual(args, want) {
						t.Fatal("hash lock or pip options changed")
					}
				} else if !reflect.DeepEqual(args, []string{"-I", filepath.Join(i.Resources, "runtime_validate.py"), "--python-version", i.Manifest.PythonVersion, "--lock", filepath.Join(i.Resources, "requirements-lock-"+platform+".txt")}) {
					t.Fatal("original validator not used")
				}
				generation, _ := i.generation("python", 1)
				if calls <= 3 {
					if _, err := os.Stat(filepath.Join(generation, "ready.json")); !os.IsNotExist(err) {
						t.Fatal("published before validation")
					}
				}
				return nil
			})
			artifact, err := i.InstallTool(context.Background(), "python", 1, "official", nil)
			if err != nil {
				t.Fatal(err)
			}
			if artifact.Version != i.Manifest.RuntimeVersion || artifact.GenerationID != "1" || calls != 3 {
				t.Fatal("invalid installation result")
			}
			if _, err := i.InstallTool(context.Background(), "python", 1, "official", nil); err != nil {
				t.Fatal(err)
			}
			if calls != 5 || requests.Load() != 1 {
				t.Fatal("validated install was downloaded/reinstalled")
			}
		})
	}
}
func TestFailedDependencyInstallHasNoReadyMarkerAndCanRetry(t *testing.T) {
	archive := tarFixture(t, []*tar.Header{{Name: "python/bin/python3", Typeflag: tar.TypeReg, Size: 7, Mode: 0755}})
	i, requests := testInstaller(t, "darwin-aarch64", "python", archive)
	injected := errors.New("synthetic_dependency_failure")
	i.Commands = commandFunc(func(_ context.Context, _ string, args []string, _ string, _ map[string]string, _ func(string) error) error {
		if len(args) > 0 && args[len(args)-1] == "--interpreter-only" {
			return nil
		}
		return injected
	})
	if _, err := i.InstallTool(context.Background(), "python", 1, "official", nil); !errors.Is(err, injected) {
		t.Fatal("dependency failure lost")
	}
	dir, _ := i.generation("python", 1)
	if _, err := os.Stat(filepath.Join(dir, "ready.json")); !os.IsNotExist(err) {
		t.Fatal("failed runtime published")
	}
	calls := 0
	i.Commands = commandFunc(func(_ context.Context, _ string, a []string, _ string, _ map[string]string, _ func(string) error) error {
		calls++
		if calls == 2 {
			return injected
		}
		return nil
	})
	if _, err := i.InstallTool(context.Background(), "python", 1, "official", nil); err != nil {
		t.Fatal(err)
	}
	if calls != 4 || requests.Load() != 1 {
		t.Fatal("retry did not repair dependencies from existing interpreter")
	}
}
func TestBrokenFFmpegIsReplacedFromVerifiedCache(t *testing.T) {
	archive := zipFixture(t, map[string]string{"upstream/bin/ffmpeg.exe": "verified-ffmpeg"})
	i, requests := testInstaller(t, "windows-x64", "ffmpeg", archive)
	dir, err := i.generation("ffmpeg", 1)
	if err != nil {
		t.Fatal(err)
	}
	if os.MkdirAll(filepath.Join(dir, "ffmpeg", "bin"), 0700) != nil || os.WriteFile(filepath.Join(dir, "ffmpeg", "bin", "ffmpeg.exe"), []byte("broken"), 0700) != nil {
		t.Fatal("fixture")
	}
	i.Commands = commandFunc(func(_ context.Context, e string, a []string, _ string, _ map[string]string, _ func(string) error) error {
		if !reflect.DeepEqual(a, []string{"-hide_banner", "-version"}) {
			t.Fatal("FFmpeg validation changed")
		}
		data, err := os.ReadFile(e)
		if err != nil || string(data) != "verified-ffmpeg" {
			return ErrAsset
		}
		return nil
	})
	if _, err := i.InstallTool(context.Background(), "ffmpeg", 1, "official", nil); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatal("FFmpeg was not repaired")
	}
}
func TestInstallerRejectsHashFailureAndUnsupportedTargetBeforeExecution(t *testing.T) {
	archive := zipFixture(t, map[string]string{"ffmpeg": "fixture"})
	i, _ := testInstaller(t, "darwin-aarch64", "ffmpeg", archive)
	i.Platform.FFmpeg.SHA256 = strings.Repeat("0", 64)
	i.Commands = commandFunc(func(context.Context, string, []string, string, map[string]string, func(string) error) error {
		t.Fatal("unverified command ran")
		return nil
	})
	if _, err := i.InstallTool(context.Background(), "ffmpeg", 1, "official", nil); !errors.Is(err, ErrChecksum) {
		t.Fatal("hash failure accepted")
	}
	unsupported, err := NewInstaller(t.TempDir(), "unsupported")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unsupported.InstallTool(context.Background(), "python", 1, "official", nil); err == nil {
		t.Fatal("unsupported platform invented a runtime")
	}
}
