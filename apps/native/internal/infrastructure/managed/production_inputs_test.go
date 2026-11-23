package managed

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

type vendorTransport func(*http.Request) (*http.Response, error)

func (transport vendorTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestProductionVendorRejectsOversizedUpstreamBeforeBuild(t *testing.T) {
	for _, contentLength := range []int64{-1, 2048} {
		i, executable, _, _ := productionVendorFixture(t)
		i.Download = &Downloader{client: &http.Client{Transport: vendorTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, ContentLength: contentLength, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 2048)))}, nil
		})}}
		i.Commands = commandFunc(func(context.Context, string, []string, string, map[string]string, func(string) error) error {
			t.Fatal("oversized upstream reached the builder")
			return nil
		})
		if err := i.ensureProductionVendor(context.Background(), executable, nil); !errors.Is(err, ErrDownload) {
			t.Fatal("upstream download ignored its pinned byte limit", err)
		}
		entries, err := os.ReadDir(filepath.Join(i.PlatformRoot(), "downloads"))
		if err != nil || len(entries) != 0 || i.productionVendor.Load() != nil {
			t.Fatal("oversized upstream left accepted or partial inputs", err)
		}
	}
}

func productionVendorFixture(t *testing.T) (*Installer, string, map[string][]byte, *atomic.Int32) {
	t.Helper()
	i, err := NewInstaller(t.TempDir(), "darwin-aarch64")
	if err != nil {
		t.Fatal(err)
	}
	executable := prepareCandidateInterpreter(t, i)
	upstream := []byte("synthetic-reviewed-upstream")
	requests := &atomic.Int32{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(upstream)
	}))
	t.Cleanup(server.Close)
	i.Download = &Downloader{client: server.Client()}
	recipe, err := json.Marshal(map[string]any{"schemaVersion": 1, "upstream": map[string]any{
		"filename": "funasr-1.3.9-py3-none-any.whl", "url": server.URL + "/pinned/source.whl", "bytes": len(upstream), "sha256": digestBytes(upstream),
	}})
	if err != nil {
		t.Fatal(err)
	}
	i.inputs = map[string][]byte{"build_vendor_wheel.py": []byte("synthetic reviewed builder"), "funasr-1.3.9.json": recipe}
	i.Manifest.ProductionInputs = &ProductionInputs{Builder: RuntimeInputFile{Name: "build_vendor_wheel.py", Bytes: int64(len(i.inputs["build_vendor_wheel.py"])), SHA256: digestBytes(i.inputs["build_vendor_wheel.py"])}}
	outputs := map[string][]byte{}
	for _, name := range runtimeInputNames() {
		content := []byte("synthetic-output:" + name)
		if strings.HasPrefix(name, "requirements-lock-") {
			content = []byte(resources[name])
			i.inputs[name] = content
		} else {
			outputs[name] = content
		}
		i.Manifest.ProductionInputs.Files = append(i.Manifest.ProductionInputs.Files, RuntimeInputFile{Name: name, Bytes: int64(len(content)), SHA256: digestBytes(content)})
	}
	i.inputs["production-inputs.json"], err = json.Marshal(i.Manifest.ProductionInputs)
	if err != nil {
		t.Fatal(err)
	}
	i.InputDigest = resourceDigest(i.inputs)
	i.Resources, err = materializeResources(i.Root, i.inputs)
	if err != nil {
		t.Fatal(err)
	}
	return i, executable, outputs, requests
}

func publishSyntheticVendor(t *testing.T, args []string, outputs map[string][]byte, mutation string) {
	t.Helper()
	if len(args) != 7 || args[0] != "-I" || args[1] != "-S" || args[3] != "--upstream-wheel" || args[5] != "--output-dir" {
		t.Fatal("builder was not invoked in isolated stdlib mode", args)
	}
	if data, err := os.ReadFile(args[2]); err != nil || string(data) != "synthetic reviewed builder" {
		t.Fatal("builder source was not materialized")
	}
	if data, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(args[2])), "vendor", "funasr-1.3.9.json")); err != nil || !json.Valid(data) {
		t.Fatal("recipe layout differs from the reviewed builder interface")
	}
	if data, err := os.ReadFile(args[4]); err != nil || string(data) != "synthetic-reviewed-upstream" {
		t.Fatal("unverified upstream was passed to the builder")
	}
	if err := os.Mkdir(args[6], 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range outputs {
		if mutation == "checksum" && name == "provenance.json" {
			content = append([]byte(nil), content...)
			content[0] ^= 1
		}
		if err := os.WriteFile(filepath.Join(args[6], name), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if mutation == "extra" {
		if err := os.WriteFile(filepath.Join(args[6], "unreviewed.whl"), []byte("extra"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProductionVendorInstallationUsesVerifiedOutputsAndIsolatedPip(t *testing.T) {
	i, executable, outputs, requests := productionVendorFixture(t)
	t.Setenv("PIP_EXTRA_INDEX_URL", "https://untrusted.invalid/simple")
	builds, pips, validations := 0, 0, 0
	i.Commands = commandFunc(func(_ context.Context, actual string, args []string, _ string, env map[string]string, _ func(string) error) error {
		if actual != executable || env["PIP_CONFIG_FILE"] != os.DevNull || env["PIP_EXTRA_INDEX_URL"] != "" {
			t.Fatal("production interpreter or pip environment changed")
		}
		if len(args) > 1 && args[1] == "-S" {
			builds++
			publishSyntheticVendor(t, args, outputs, "")
		} else if len(args) > 2 && args[2] == "pip" {
			pips++
			want := []string{"-I", "-m", "pip", "--isolated", "install", "--disable-pip-version-check", "--no-input", "--require-hashes", "--no-build-isolation", "--no-cache-dir", "--find-links", i.Resources, "-i", "https://pypi.org/simple", "-r", filepath.Join(i.Resources, "requirements-lock-"+i.Platform.ID+".txt")}
			if !reflect.DeepEqual(args, want) || i.productionVendor.Load() == nil {
				t.Fatal("production pip omitted verified vendor inputs or hash isolation")
			}
		} else if args[len(args)-1] != "--interpreter-only" {
			validations++
			if validations == 1 {
				return errors.New("synthetic_missing_dependencies")
			}
		}
		return nil
	})
	artifact, err := i.InstallTool(context.Background(), "python", 1, "official", nil)
	if err != nil || builds != 1 || pips != 1 || validations != 2 || requests.Load() != 1 || !i.artifactBound("python", artifact) {
		t.Fatal("production runtime did not validate before publication", err, builds, pips, validations, requests.Load())
	}
	i.productionVendor.Store(nil)
	if err := i.ensureProductionVendor(context.Background(), executable, nil); err != nil || builds != 1 || requests.Load() != 1 {
		t.Fatal("verified persistent vendor cache was not reusable", err)
	}
	if !reflect.DeepEqual(*i.productionVendor.Load(), outputs) {
		t.Fatal("cached vendor outputs differ from the manifest")
	}
}

func TestProductionVendorInstallationRejectsPartialOrTamperedOutputAndRetries(t *testing.T) {
	for _, mutation := range []string{"checksum", "extra", "builder_failure", "canceled"} {
		t.Run(mutation, func(t *testing.T) {
			i, executable, outputs, _ := productionVendorFixture(t)
			injected := errors.New("synthetic_builder_failure")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			i.Commands = commandFunc(func(_ context.Context, _ string, args []string, _ string, _ map[string]string, _ func(string) error) error {
				if args[len(args)-1] == "--interpreter-only" {
					return nil
				}
				if len(args) > 1 && args[1] == "-S" {
					if mutation == "canceled" {
						cancel()
						return context.Canceled
					}
					if mutation == "builder_failure" {
						return injected
					}
					publishSyntheticVendor(t, args, outputs, mutation)
					return nil
				}
				if len(args) > 2 && args[2] == "pip" {
					t.Fatal("pip received unverified vendor output")
				}
				return injected
			})
			if _, err := i.InstallTool(ctx, "python", 1, "official", nil); err == nil || i.productionVendor.Load() != nil {
				t.Fatal("invalid vendor output was accepted", err)
			}
			generation, err := i.generation("python", 1)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(generation, "ready.json")); !os.IsNotExist(err) {
				t.Fatal("failed production installation published a ready marker")
			}
			i.Commands = commandFunc(func(_ context.Context, _ string, args []string, _ string, _ map[string]string, _ func(string) error) error {
				publishSyntheticVendor(t, args, outputs, "")
				return nil
			})
			if err := i.ensureProductionVendor(context.Background(), executable, nil); err != nil {
				t.Fatal("failed build could not retry in a fresh workspace", err)
			}
		})
	}
}

func TestProductionVendorCommandSnapshot(t *testing.T) {
	if os.Getenv("LIBERTY_PRODUCTION_SNAPSHOT_HELPER") == "1" {
		args := os.Args[len(os.Args)-2:]
		if !strings.Contains(args[0], "install-inputs") {
			os.Exit(2)
		}
		for _, filename := range args {
			data, err := os.ReadFile(filename)
			if err != nil || !strings.HasPrefix(string(data), "synthetic-output:") {
				os.Exit(3)
			}
		}
		return
	}
	i, _, outputs, _ := productionVendorFixture(t)
	i.productionVendor.Store(&outputs)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-test.run=^TestProductionVendorCommandSnapshot$", "--", filepath.Join(i.Resources, "funasr-1.3.9+liberty.local2-py3-none-any.whl"), filepath.Join(i.Resources, "source.cdx.json")}
	if err := (Commands{installer: i}).Run(context.Background(), executable, args, i.Resources, map[string]string{"LIBERTY_PRODUCTION_SNAPSHOT_HELPER": "1"}, nil); err != nil {
		t.Fatal("production pip inputs were not copied into the private command snapshot", err)
	}
	entries, err := os.ReadDir(filepath.Join(i.PlatformRoot(), "install-inputs"))
	if err != nil || len(entries) != 0 {
		t.Fatal("production command snapshot was not removed", err)
	}
}
