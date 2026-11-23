package managed

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"liberty.local/liberty/internal/domain"
)

func interpreterFixture(t *testing.T, platform string) (*Installer, func() int32) {
	t.Helper()
	name := "python/bin/python3"
	if platform == "windows-x64" {
		name = "python/python.exe"
	}
	archive := tarFixture(t, []*tar.Header{{Name: name, Typeflag: tar.TypeReg, Size: 7, Mode: 0755}})
	installer, requests := testInstaller(t, platform, "python", archive)
	return installer, requests.Load
}

func TestManagedPythonRejectsWrongInterpreterBeforeInstallingDependencies(t *testing.T) {
	for _, platform := range []string{"darwin-aarch64", "darwin-x64", "windows-x64"} {
		t.Run(platform, func(t *testing.T) {
			installer, requests := interpreterFixture(t, platform)
			failure := errors.New("synthetic-interpreter-version-mismatch")
			probes, installations := 0, 0
			installer.Commands = commandFunc(func(_ context.Context, _ string, args []string, _ string, _ map[string]string, _ func(string) error) error {
				if reflect.DeepEqual(args, []string{"-I", filepath.Join(installer.Resources, "runtime_validate.py"), "--python-version", installer.Manifest.PythonVersion, "--interpreter-only"}) {
					probes++
					return failure
				}
				if len(args) > 1 && args[0] == "-m" && args[1] == "pip" {
					installations++
				}
				return nil
			})
			if _, err := installer.InstallTool(context.Background(), "python", 1, "official", nil); !errors.Is(err, failure) {
				t.Fatal("wrong interpreter was accepted or its failure was lost", err)
			}
			if probes != 1 || installations != 0 || requests() != 1 {
				t.Fatal("dependency installation ran before verifying the interpreter", probes, installations, requests())
			}
			directory, err := installer.generation("python", 1)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(directory, "ready.json")); !os.IsNotExist(err) {
				t.Fatal("wrong interpreter received a ready marker", err)
			}
		})
	}
}

func TestManagedPythonReplacesWrongCachedInterpreterBeforeRepairingDependencies(t *testing.T) {
	for _, platform := range []string{"darwin-aarch64", "darwin-x64", "windows-x64"} {
		t.Run(platform, func(t *testing.T) {
			installer, requests := interpreterFixture(t, platform)
			directory, err := installer.generation("python", 1)
			if err != nil {
				t.Fatal(err)
			}
			candidate := "python/bin/python3"
			if platform == "windows-x64" {
				candidate = "python/python.exe"
			}
			cached := filepath.Join(directory, filepath.FromSlash(candidate))
			if err := os.MkdirAll(filepath.Dir(cached), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cached, []byte("synthetic-wrong-version"), 0700); err != nil {
				t.Fatal(err)
			}
			probes, installations, validations := 0, 0, 0
			installer.Commands = commandFunc(func(_ context.Context, executable string, args []string, _ string, _ map[string]string, _ func(string) error) error {
				content, err := os.ReadFile(executable)
				if err != nil {
					t.Fatal(err)
				}
				if len(args) > 0 && args[len(args)-1] == "--interpreter-only" {
					probes++
					if string(content) != "xxxxxxx" {
						return errors.New("synthetic-interpreter-version-mismatch")
					}
					return nil
				}
				if string(content) != "xxxxxxx" {
					t.Error("unverified cached interpreter was used outside the version probe")
				}
				if len(args) > 1 && args[0] == "-m" && args[1] == "pip" {
					installations++
				} else {
					validations++
					want := []string{"-I", filepath.Join(installer.Resources, "runtime_validate.py"), "--python-version", installer.Manifest.PythonVersion, "--lock", filepath.Join(installer.Resources, "requirements-lock-"+platform+".txt")}
					if !reflect.DeepEqual(args, want) {
						t.Error("managed validation did not bind the interpreter and dependency lock")
					}
				}
				return nil
			})
			artifact, err := installer.InstallTool(context.Background(), "python", 1, "official", nil)
			if err != nil || artifact.Version != installer.Manifest.RuntimeVersion || probes != 2 || installations != 1 || validations != 1 || requests() != 1 {
				t.Fatal("wrong cached interpreter was not replaced safely", err, probes, installations, validations, requests())
			}
		})
	}
}

func TestSystemPythonValidationKeepsUnpinnedInterpreterPolicy(t *testing.T) {
	installer, err := NewInstaller(t.TempDir(), "darwin-aarch64")
	if err != nil {
		t.Fatal(err)
	}
	called := false
	installer.Commands = commandFunc(func(_ context.Context, _ string, args []string, _ string, _ map[string]string, _ func(string) error) error {
		called = true
		if !reflect.DeepEqual(args, []string{filepath.Join(installer.Resources, "runtime_validate.py")}) {
			t.Fatal("system Python was required to match the managed interpreter")
		}
		return nil
	})
	if err := installer.ValidateSystem(context.Background(), "python", filepath.Join(t.TempDir(), "system-python"), nil); err != nil || !called {
		t.Fatal("system Python validation was not performed", err)
	}
}

func managedPythonArtifactFixture(t *testing.T) (*Installer, domain.RuntimeComponent, string) {
	t.Helper()
	installer, err := NewInstaller(t.TempDir(), "windows-x64")
	if err != nil {
		t.Fatal("synthetic managed installer failed", err)
	}
	directory, err := installer.generation("python", 1)
	if err != nil {
		t.Fatal("synthetic managed generation failed", err)
	}
	executable := filepath.Join(directory, filepath.FromSlash(installer.Platform.PythonCandidates[0]))
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		t.Fatal("synthetic interpreter directory failed", err)
	}
	if err := os.WriteFile(executable, []byte("synthetic-cpython"), 0700); err != nil {
		t.Fatal("synthetic interpreter failed", err)
	}
	root, err := installer.openGeneration("python", 1)
	if err != nil {
		t.Fatal("synthetic generation root failed", err)
	}
	artifact, err := installer.publish(root, "python", 1, executable, installer.Manifest.RuntimeVersion)
	root.Close()
	if err != nil {
		t.Fatal("synthetic interpreter publication failed", err)
	}
	state := domain.NewRuntimeComponent("python", "managed", "")
	state.Artifact, state.Availability = &artifact, "ready"
	return installer, state, executable
}

func TestReconcileRejectsManagedPythonWithWrongInterpreter(t *testing.T) {
	installer, state, _ := managedPythonArtifactFixture(t)
	probeCalls := 0
	want := []string{"-I", filepath.Join(installer.Resources, "runtime_validate.py"), "--python-version", installer.Manifest.PythonVersion, "--interpreter-only"}
	installer.Commands = commandFunc(func(_ context.Context, _ string, args []string, _ string, _ map[string]string, _ func(string) error) error {
		probeCalls++
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("unexpected managed interpreter probe: %#v", args)
		}
		return errors.New("synthetic_wrong_cpython_version")
	})

	actual, err := installer.Reconcile(state, domain.RuntimeOverview{}, domain.RuntimeSettings{})
	if err != nil || actual.Ready() || actual.Artifact != nil || actual.Operation.Kind != "failed" || actual.Operation.Phase != "interpreter_invalid" || actual.Operation.LastError == nil || *actual.Operation.LastError != "runtime_python_interpreter_invalid" || probeCalls != 1 {
		t.Fatalf("wrong managed interpreter remained ready: %+v, probes=%d, err=%v", actual, probeCalls, err)
	}
	actual, err = installer.Reconcile(actual, domain.RuntimeOverview{}, domain.RuntimeSettings{})
	if err != nil || actual.Ready() || probeCalls != 1 {
		t.Fatal("failed reconciliation retried or restored an invalid interpreter", actual, probeCalls, err)
	}
}

func TestReconcileCachesExactProbeAndInvalidatesReplacedInterpreter(t *testing.T) {
	installer, state, executable := managedPythonArtifactFixture(t)
	probeCalls := 0
	installer.Commands = commandFunc(func(_ context.Context, _ string, args []string, _ string, _ map[string]string, _ func(string) error) error {
		probeCalls++
		if len(args) == 0 || args[len(args)-1] != "--interpreter-only" {
			t.Fatalf("managed reconciliation ran more than the exact interpreter probe: %#v", args)
		}
		return nil
	})

	for range 2 {
		actual, err := installer.Reconcile(state, domain.RuntimeOverview{}, domain.RuntimeSettings{})
		if err != nil || !actual.Ready() {
			t.Fatal("verified managed Python did not reconcile as ready", actual, err)
		}
	}
	if probeCalls != 1 {
		t.Fatalf("unchanged interpreter was probed %d times", probeCalls)
	}
	temporary := executable + ".replacement"
	if err := os.WriteFile(temporary, []byte("replacement-cpython"), 0700); err != nil {
		t.Fatal("replacement interpreter fixture failed", err)
	}
	if err := os.Remove(executable); err != nil {
		t.Fatal("old interpreter removal failed", err)
	}
	if err := os.Rename(temporary, executable); err != nil {
		t.Fatal("replacement interpreter installation failed", err)
	}
	actual, err := installer.Reconcile(state, domain.RuntimeOverview{}, domain.RuntimeSettings{})
	if err != nil || !actual.Ready() || probeCalls != 2 {
		t.Fatal("replaced interpreter reused stale validation", actual, probeCalls, err)
	}
}

func TestReconcileInvalidatesInPlaceInterpreterWithRestoredMetadata(t *testing.T) {
	installer, state, executable := managedPythonArtifactFixture(t)
	probeCalls := 0
	installer.Commands = commandFunc(func(_ context.Context, _ string, args []string, _ string, _ map[string]string, _ func(string) error) error {
		probeCalls++
		if len(args) == 0 || args[len(args)-1] != "--interpreter-only" {
			t.Fatalf("managed reconciliation ran more than the exact interpreter probe: %#v", args)
		}
		return nil
	})

	if actual, err := installer.Reconcile(state, domain.RuntimeOverview{}, domain.RuntimeSettings{}); err != nil || !actual.Ready() {
		t.Fatal("initial managed interpreter reconciliation failed", actual, err)
	}
	before, err := os.Stat(executable)
	if err != nil {
		t.Fatal("interpreter stat failed", err)
	}
	file, err := os.OpenFile(executable, os.O_WRONLY|os.O_TRUNC, before.Mode().Perm())
	if err != nil {
		t.Fatal("interpreter replacement failed", err)
	}
	if _, err := file.Write(bytes.Repeat([]byte{'x'}, int(before.Size()))); err != nil {
		file.Close()
		t.Fatal("interpreter replacement write failed", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal("interpreter replacement close failed", err)
	}
	if err := os.Chtimes(executable, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal("interpreter metadata restore failed", err)
	}
	after, err := os.Stat(executable)
	if err != nil {
		t.Fatal("replaced interpreter stat failed", err)
	}
	if !sameInterpreterMetadata(before, after) {
		t.Fatalf("test replacement did not preserve metadata: before=%v after=%v", before, after)
	}

	actual, err := installer.Reconcile(state, domain.RuntimeOverview{}, domain.RuntimeSettings{})
	if err != nil || !actual.Ready() || probeCalls != 2 {
		t.Fatal("in-place interpreter replacement reused stale validation", actual, probeCalls, err)
	}
}

func TestReconcileDoesNotPinExplicitSystemPython(t *testing.T) {
	installer, err := NewInstaller(t.TempDir(), "windows-x64")
	if err != nil {
		t.Fatal("synthetic installer failed", err)
	}
	executable := filepath.Join(t.TempDir(), "system-python")
	if err := os.WriteFile(executable, []byte("system-python"), 0600); err != nil {
		t.Fatal("synthetic system Python failed", err)
	}
	probeCalls := 0
	installer.Commands = commandFunc(func(context.Context, string, []string, string, map[string]string, func(string) error) error {
		probeCalls++
		return nil
	})
	state := domain.NewRuntimeComponent("python", "system", "")
	state.Artifact = &domain.RuntimeArtifact{GenerationID: "system", Version: "system", Path: executable}
	state.Availability = "ready"
	actual, err := installer.Reconcile(state, domain.RuntimeOverview{}, domain.RuntimeSettings{})
	if err != nil || !actual.Ready() || probeCalls != 0 {
		t.Fatal("explicit system Python was pinned to the managed runtime", actual, probeCalls, err)
	}
}

func TestManagedPythonCanceledInterpreterProbeDoesNotReplaceExistingFiles(t *testing.T) {
	installer, requests := interpreterFixture(t, "darwin-aarch64")
	directory, err := installer.generation("python", 1)
	if err != nil {
		t.Fatal(err)
	}
	cached := filepath.Join(directory, "python", "bin", "python3")
	if err := os.MkdirAll(filepath.Dir(cached), 0700); err != nil {
		t.Fatal(err)
	}
	before := []byte("synthetic-existing-interpreter")
	if err := os.WriteFile(cached, before, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	installer.Commands = commandFunc(func(_ context.Context, _ string, args []string, _ string, _ map[string]string, _ func(string) error) error {
		calls++
		if args[len(args)-1] != "--interpreter-only" {
			t.Fatal("canceled interpreter reached dependency validation or installation")
		}
		cancel()
		return context.Canceled
	})
	if _, err := installer.InstallTool(ctx, "python", 1, "official", nil); !errors.Is(err, context.Canceled) || calls != 1 || requests() != 0 {
		t.Fatal("canceled interpreter probe triggered archive replacement", err, calls, requests())
	}
	after, err := os.ReadFile(cached)
	if err != nil || string(after) != string(before) {
		t.Fatal("canceled interpreter probe replaced the cached interpreter", err)
	}
}
