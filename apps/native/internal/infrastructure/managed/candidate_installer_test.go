package managed

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"liberty.local/liberty/internal/domain"
)

func prepareCandidateInterpreter(t *testing.T, installer *Installer) string {
	t.Helper()
	directory, err := installer.generation("python", 1)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(directory, "python", "bin", "python3")
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("synthetic-interpreter"), 0700); err != nil {
		t.Fatal(err)
	}
	return executable
}

func TestCandidateInstallerUsesIsolatedPipAndVerifiedInputs(t *testing.T) {
	installer, _, _ := candidateInstallerFixture(t)
	executable := prepareCandidateInterpreter(t, installer)
	t.Setenv("PIP_EXTRA_INDEX_URL", "https://untrusted.invalid/simple")
	t.Setenv("PIP_FIND_LINKS", "https://untrusted.invalid/wheels")
	t.Setenv("PIP_CONFIG_FILE", filepath.Join(t.TempDir(), "untrusted.conf"))
	calls := 0
	installer.Commands = commandFunc(func(ctx context.Context, actual string, args []string, directory string, env map[string]string, _ func(string) error) error {
		calls++
		if actual != executable {
			t.Fatal("wrong interpreter selected")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("command has no deadline")
		}
		if env["PIP_CONFIG_FILE"] != os.DevNull || env["PIP_EXTRA_INDEX_URL"] != "" || env["PIP_FIND_LINKS"] != "" {
			t.Fatal("candidate inherited pip configuration")
		}
		switch calls {
		case 1, 5:
			want := []string{"-I", filepath.Join(installer.Resources, "runtime_validate.py"), "--python-version", "3.11.18", "--interpreter-only"}
			if !reflect.DeepEqual(args, want) {
				t.Fatal("interpreter pin was not checked before pip/reuse", args)
			}
		case 2:
			if !strings.Contains(strings.Join(args, " "), "--lock") {
				t.Fatal("cached dependency validation omitted lock")
			}
			return errors.New("synthetic_incomplete_dependencies")
		case 3:
			want := []string{"-I", "-m", "pip", "--isolated", "install", "--disable-pip-version-check", "--no-input", "--require-hashes", "--no-build-isolation", "--no-cache-dir", "--find-links", installer.Resources, "-i", "https://pypi.tuna.tsinghua.edu.cn/simple", "-r", filepath.Join(installer.Resources, "requirements-lock-"+installer.Platform.ID+".txt")}
			if !reflect.DeepEqual(args, want) {
				t.Fatal("candidate pip source or integrity flags changed", args)
			}
			if directory != filepath.Dir(filepath.Dir(filepath.Dir(executable))) {
				t.Fatal("pip directory was not its candidate generation")
			}
		default:
			want := []string{"-I", filepath.Join(installer.Resources, "runtime_validate.py"), "--python-version", "3.11.18", "--lock", filepath.Join(installer.Resources, "requirements-lock-"+installer.Platform.ID+".txt")}
			if !reflect.DeepEqual(args, want) {
				t.Fatal("candidate full validation omitted pin or lock", args)
			}
		}
		return nil
	})
	artifact, err := installer.InstallTool(context.Background(), "python", 1, "tsinghua", nil)
	if err != nil || calls != 4 || !installer.artifactBound("python", artifact) {
		t.Fatal("candidate was not validated before publication", err, calls)
	}
	if _, err := installer.InstallTool(context.Background(), "python", 1, "tsinghua", nil); err != nil || calls != 6 {
		t.Fatal("valid candidate cache was not revalidated", err, calls)
	}
}

func TestCandidateInstallerFailureDoesNotPublishOrFallback(t *testing.T) {
	for _, failure := range []string{"interpreter", "pip", "final", "canceled"} {
		t.Run(failure, func(t *testing.T) {
			installer, _, _ := candidateInstallerFixture(t)
			prepareCandidateInterpreter(t, installer)
			injected := errors.New("synthetic_candidate_failure")
			calls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			installer.Commands = commandFunc(func(_ context.Context, _ string, args []string, _ string, _ map[string]string, _ func(string) error) error {
				calls++
				if calls == 1 && failure == "canceled" {
					cancel()
					return context.Canceled
				}
				if calls == 1 && failure == "interpreter" {
					cancel()
					return injected
				}
				if calls == 2 {
					return injected
				}
				if len(args) > 2 && args[2] == "pip" && failure == "pip" {
					return injected
				}
				if calls == 4 && failure == "final" {
					return injected
				}
				return nil
			})
			if _, err := installer.InstallTool(ctx, "python", 1, "official", nil); err == nil {
				t.Fatal("failed candidate installation succeeded")
			}
			root, err := installer.openGeneration("python", 1)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if _, err := root.Stat("ready.json"); !os.IsNotExist(err) {
				t.Fatal("failed candidate published a ready marker")
			}
			if _, err := os.Stat(filepath.Join(installer.Root, "runtime", installer.Platform.ID)); !os.IsNotExist(err) {
				t.Fatal("candidate fell back to production")
			}
		})
	}
}

func TestCandidateInstallerRejectsMismatchedCacheBindingBeforeExecution(t *testing.T) {
	installer, _, _ := candidateInstallerFixture(t)
	executable := prepareCandidateInterpreter(t, installer)
	root, err := installer.openGeneration("python", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := installer.publish(root, "python", 1, executable, installer.Manifest.RuntimeVersion); err != nil {
		t.Fatal(err)
	}
	content, err := root.ReadFile("ready.json")
	if err != nil {
		t.Fatal(err)
	}
	var marker readyMarker
	if err := json.Unmarshal(content, &marker); err != nil {
		t.Fatal(err)
	}
	marker.RuntimeProfile = ProductionRuntimeProfile
	content, err = json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(root, "ready.json", content); err != nil {
		t.Fatal(err)
	}
	installer.Commands = commandFunc(func(context.Context, string, []string, string, map[string]string, func(string) error) error {
		t.Fatal("unbound cached interpreter executed")
		return nil
	})
	if _, err := installer.InstallTool(context.Background(), "python", 1, "official", nil); !errors.Is(err, ErrResources) {
		t.Fatal("unbound cache was relabeled", err)
	}
	state := domain.NewRuntimeComponent("python", "system", "")
	state.Artifact = &domain.RuntimeArtifact{GenerationID: "system", Version: "system", Path: executable}
	state.Availability = "ready"
	actual, err := installer.Reconcile(state, domain.RuntimeOverview{}, domain.RuntimeSettings{})
	if err != nil || !actual.Ready() {
		t.Fatal("system Python capability policy changed", err)
	}
}
