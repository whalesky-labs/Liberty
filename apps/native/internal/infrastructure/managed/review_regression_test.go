package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

type reviewOfflineTransport struct{}

func (reviewOfflineTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("synthetic_network_forbidden")
}

func TestReviewRejectsLinkedGenerationBeforeExecutingOrPublishing(t *testing.T) {
	candidate, _, _ := candidateInstallerFixture(t)
	candidate.Download = &Downloader{client: &http.Client{Transport: reviewOfflineTransport{}}}
	production, err := NewInstaller(candidate.Root, candidate.Platform.ID)
	if err != nil {
		t.Fatal("synthetic production fixture failed")
	}
	productionGeneration, err := production.generation("ffmpeg", 1)
	if err != nil {
		t.Fatal("synthetic production generation failed")
	}
	if os.MkdirAll(filepath.Join(productionGeneration, "ffmpeg"), 0700) != nil || os.WriteFile(filepath.Join(productionGeneration, "ffmpeg", "ffmpeg"), []byte("synthetic-production-ffmpeg"), 0700) != nil {
		t.Fatal("synthetic executable seeding failed")
	}
	relative, err := candidate.generationRelative("ffmpeg", 1)
	if err != nil {
		t.Fatal("synthetic candidate relative path failed")
	}
	candidateGeneration := filepath.Join(candidate.Root, filepath.FromSlash(relative))
	if os.MkdirAll(filepath.Dir(candidateGeneration), 0700) != nil {
		t.Fatal("synthetic candidate parent seeding failed")
	}
	target, err := filepath.Rel(filepath.Dir(candidateGeneration), productionGeneration)
	if err != nil || os.Symlink(target, candidateGeneration) != nil {
		t.Fatal("synthetic in-profile link seeding failed")
	}
	calls := 0
	candidate.Commands = commandFunc(func(context.Context, string, []string, string, map[string]string, func(string) error) error {
		calls++
		return nil
	})
	if _, err := candidate.InstallTool(context.Background(), "ffmpeg", 1, "official", nil); !errors.Is(err, ErrResources) || calls != 0 {
		t.Error("linked candidate generation reached executable validation or publication")
	}
	if _, err := os.Stat(filepath.Join(productionGeneration, "ready.json")); !os.IsNotExist(err) {
		t.Error("candidate changed the production generation")
	}
}

func TestReviewRejectsLockChangedAfterInstallEntryBeforePip(t *testing.T) {
	candidate, _, verified := candidateInstallerFixture(t)
	prepareCandidateInterpreter(t, candidate)
	candidate.Download = &Downloader{client: &http.Client{Transport: reviewOfflineTransport{}}}
	lockName := "requirements-lock-" + candidate.Platform.ID + ".txt"
	changed := append([]byte(nil), verified[lockName]...)
	changed[0] ^= 1
	calls := 0
	candidate.Commands = commandFunc(func(_ context.Context, _ string, _ []string, _ string, _ map[string]string, _ func(string) error) error {
		calls++
		if calls == 2 {
			if os.WriteFile(filepath.Join(candidate.Resources, lockName), changed, 0600) != nil {
				t.Fatal("synthetic post-verification mutation failed")
			}
			return errors.New("synthetic_dependencies_need_installation")
		}
		return nil
	})
	if _, err := candidate.InstallTool(context.Background(), "python", 1, "official", nil); !errors.Is(err, ErrResources) || calls != 2 {
		t.Error("changed lock reached pip or was published")
	}
	root, err := candidate.openGeneration("python", 1)
	if err != nil {
		t.Fatal("synthetic generation opening failed")
	}
	defer root.Close()
	if _, err := root.Lstat("ready.json"); !os.IsNotExist(err) {
		t.Error("changed resource snapshot received a ready marker")
	}
}

func TestReviewProductionRejectsCandidateModelArtifact(t *testing.T) {
	candidate, _, _ := candidateInstallerFixture(t)
	production, err := NewInstaller(candidate.Root, candidate.Platform.ID)
	if err != nil {
		t.Fatal("synthetic production fixture failed")
	}
	directory, err := candidate.generation("model", 1)
	if err != nil {
		t.Fatal("synthetic model generation failed")
	}
	resolved := filepath.Join(directory, "models")
	if os.Mkdir(resolved, 0700) != nil {
		t.Fatal("synthetic model directory seeding failed")
	}
	root, err := candidate.openGeneration("model", 1)
	if err != nil {
		t.Fatal("synthetic model generation opening failed")
	}
	defer root.Close()
	artifact, err := candidate.publish(root, "model", 1, resolved, candidate.Manifest.ModelSetVersion)
	if err != nil {
		t.Fatal("synthetic model publication failed")
	}
	state := domain.NewRuntimeComponent("model", "managed", "")
	state.Artifact, state.Availability = &artifact, "ready"
	actual, err := production.Reconcile(state, domain.RuntimeOverview{}, domain.RuntimeSettings{})
	if err != nil || actual.Ready() || actual.Artifact != nil || actual.Operation.Phase != "artifact_unverified" {
		t.Fatal("production kept a candidate-owned model artifact ready")
	}
}

func TestReviewRejectsLinksAtEveryManagedDirectoryLevel(t *testing.T) {
	for _, operation := range []string{"generation", "downloads", "append-log", "read-log"} {
		levels := []string{"runtime", "runtime/profiles", "runtime/profiles/security-candidate", "runtime/profiles/security-candidate/darwin-aarch64"}
		if operation == "generation" {
			for _, tail := range []string{"components", "components/python", "components/python/generations", "components/python/generations/1"} {
				levels = append(levels, levels[3]+"/"+tail)
			}
		} else if operation == "downloads" {
			levels = append(levels, levels[3]+"/downloads")
		} else {
			levels = append(levels, levels[3]+"/logs")
		}
		for _, level := range levels {
			t.Run(operation+"/"+strings.ReplaceAll(level, "/", "_"), func(t *testing.T) {
				installer, _, _ := candidateInstallerFixture(t)
				link := filepath.Join(installer.Root, filepath.FromSlash(level))
				target := filepath.Join(installer.Root, "synthetic-link-target")
				if os.MkdirAll(filepath.Dir(link), 0700) != nil || os.Mkdir(target, 0700) != nil || os.Symlink(target, link) != nil {
					t.Fatal("synthetic directory link setup failed")
				}
				var err error
				switch operation {
				case "generation":
					_, err = installer.generation("python", 1)
					if root, openErr := installer.openGeneration("python", 1); openErr == nil {
						root.Close()
						t.Error("status opened a linked generation")
					}
				case "downloads":
					var root *os.Root
					root, err = installer.downloads()
					if root != nil {
						root.Close()
					}
				case "append-log":
					err = installer.AppendLog("python", "synthetic")
				case "read-log":
					_, err = installer.Log("python")
				}
				if !errors.Is(err, ErrResources) {
					t.Fatal("managed directory link was accepted")
				}
				entries, readErr := os.ReadDir(target)
				if readErr != nil || len(entries) != 0 {
					t.Fatal("rejected directory link changed its target")
				}
			})
		}
	}
}

func TestReviewReadyBindingRejectsLinkedArtifactAndKeepsInternalExecutableAlias(t *testing.T) {
	for _, mode := range []string{"internal-alias", "sibling-alias", "linked-directory", "hardlink"} {
		t.Run(mode, func(t *testing.T) {
			installer, _, _ := candidateInstallerFixture(t)
			directory, err := installer.generation("python", 1)
			if err != nil {
				t.Fatal("synthetic generation failed")
			}
			binaryDirectory := filepath.Join(directory, "python", "bin")
			if os.MkdirAll(binaryDirectory, 0700) != nil {
				t.Fatal("synthetic executable directory failed")
			}
			actual := filepath.Join(binaryDirectory, "python3.11")
			if os.WriteFile(actual, []byte("synthetic-interpreter"), 0700) != nil {
				t.Fatal("synthetic executable failed")
			}
			alias := filepath.Join(binaryDirectory, "python3")
			if os.Symlink("python3.11", alias) != nil {
				t.Fatal("synthetic interpreter alias failed")
			}
			root, err := installer.openGeneration("python", 1)
			if err != nil {
				t.Fatal("synthetic generation opening failed")
			}
			defer root.Close()
			artifact, err := installer.publish(root, "python", 1, alias, installer.Manifest.RuntimeVersion)
			if err != nil || !installer.artifactBound("python", artifact) {
				t.Fatal("normal within-generation interpreter alias was rejected")
			}
			switch mode {
			case "internal-alias":
				resolved, err := findManagedCandidate(root, directory, installer.Platform.PythonCandidates)
				if err != nil || resolved != actual {
					t.Fatal("normal interpreter alias did not resolve to its verified generation")
				}
				artifact.Path = resolved
				if !installer.artifactBound("python", artifact) {
					t.Fatal("previous ready marker could not bind the resolved executable alias")
				}
				return
			case "sibling-alias":
				sibling, err := installer.generation("python", 2)
				if err != nil || os.WriteFile(filepath.Join(sibling, "python"), []byte("sibling-interpreter"), 0700) != nil || os.Remove(alias) != nil || os.Symlink(filepath.Join(sibling, "python"), alias) != nil {
					t.Fatal("synthetic sibling alias failed")
				}
			case "linked-directory":
				moved := filepath.Join(directory, "moved-bin")
				if os.Rename(binaryDirectory, moved) != nil || os.Symlink(moved, binaryDirectory) != nil {
					t.Fatal("synthetic directory alias failed")
				}
			case "hardlink":
				if os.Link(actual, filepath.Join(directory, "extra-interpreter-link")) != nil {
					t.Fatal("synthetic executable hardlink failed")
				}
			}
			if installer.artifactBound("python", artifact) {
				t.Fatal("ready identity accepted a linked or foreign artifact")
			}
			state := domain.NewRuntimeComponent("python", "managed", "")
			state.Artifact, state.Availability = &artifact, "ready"
			actualState, err := installer.Reconcile(state, domain.RuntimeOverview{}, domain.RuntimeSettings{})
			if err != nil || actualState.Artifact != nil || actualState.Ready() {
				t.Fatal("linked artifact survived reconciliation")
			}
		})
	}
}

func TestReviewProductionRejectsUnboundAndCandidateReady(t *testing.T) {
	for _, profileID := range []string{"", ProductionRuntimeProfile, SecurityCandidateProfile} {
		t.Run("profile-"+profileID, func(t *testing.T) {
			installer, err := NewInstaller(t.TempDir(), CurrentPlatform())
			if err != nil {
				t.Fatal("synthetic production installer failed")
			}
			directory, err := installer.generation("ffmpeg", 1)
			if err != nil {
				t.Fatal("synthetic production generation failed")
			}
			resolved := filepath.Join(directory, "ffmpeg")
			if os.WriteFile(resolved, []byte("synthetic-executable"), 0700) != nil {
				t.Fatal("synthetic production executable failed")
			}
			marker := readyMarker{Component: "ffmpeg", Generation: 1, Version: installer.Manifest.RuntimeVersion, Path: resolved, Profile: installer.Manifest.ModelProfile, RuntimeProfile: profileID}
			encoded, err := json.Marshal(marker)
			if err != nil || os.WriteFile(filepath.Join(directory, "ready.json"), encoded, 0600) != nil {
				t.Fatal("synthetic production ready marker failed")
			}
			state := domain.NewRuntimeComponent("ffmpeg", "managed", "")
			state.Artifact = &domain.RuntimeArtifact{GenerationID: "1", Version: marker.Version, Path: resolved}
			state.Availability = "ready"
			actual, err := installer.Reconcile(state, domain.RuntimeOverview{}, domain.RuntimeSettings{})
			if err != nil || actual.Ready() || actual.Artifact != nil {
				t.Fatal("production reused a runtime without its current input binding")
			}
		})
	}
}

func TestReviewCommandSnapshotIsPrivateExactAndRemoved(t *testing.T) {
	installer, _, _ := candidateInstallerFixture(t)
	parent, err := installer.runtimeDirectory(filepath.Join(installer.platformRelative(), "install-inputs"), true)
	if err != nil {
		t.Fatal("synthetic command input parent failed")
	}
	defer parent.Close()
	snapshot := resourceContents(installer.inputs)
	directory, cleanup, err := commandInputSnapshot(context.Background(), parent, snapshot)
	if err != nil {
		t.Fatal("private command snapshot failed")
	}
	defer cleanup()
	if verifyResourceSnapshot(directory, snapshot) != nil {
		t.Fatal("private command snapshot differs from its verified bytes")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(directory)
		if err != nil || info.Mode().Perm() != 0500 {
			t.Fatal("private command input directory is writable")
		}
		for name := range snapshot {
			info, err := os.Stat(filepath.Join(directory, name))
			if err != nil || info.Mode().Perm() != 0400 {
				t.Fatal("private command input file is writable")
			}
		}
	}
	if cleanup() != nil {
		t.Fatal("private command snapshot cleanup failed")
	}
	if _, err := os.Lstat(directory); !os.IsNotExist(err) {
		t.Fatal("private command snapshot survived cleanup")
	}
}

func TestReviewRealCommandReadsPrivateSnapshotAndCleansSuccessAndFailure(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("real synthetic command fixture runs on macOS")
	}
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failure], func(t *testing.T) {
			installer, _, verified := candidateInstallerFixture(t)
			audit := filepath.Join(installer.Root, "synthetic-audit")
			output := filepath.Join(installer.Root, "synthetic-lock-output")
			lock := "requirements-lock-" + installer.Platform.ID + ".txt"
			script := `printf '%s' "$1" > "$3"
/bin/cat "$2" > "$4"
exit "$5"
`
			exitCode := "0"
			if failure {
				exitCode = "1"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := installer.Commands.Run(ctx, "/bin/bash", []string{"-c", script, "synthetic-command", installer.Resources, filepath.Join(installer.Resources, lock), audit, output, exitCode}, installer.Resources, installer.environment(), nil)
			if (err != nil) != failure {
				t.Fatal("synthetic real command result changed")
			}
			actual, err := os.ReadFile(output)
			if err != nil || !bytes.Equal(actual, verified[lock]) {
				t.Fatal("real command read different requirement bytes")
			}
			inputDirectory, err := os.ReadFile(audit)
			if err != nil || string(inputDirectory) == installer.Resources || !withinDirectory(filepath.Join(installer.PlatformRoot(), "install-inputs"), string(inputDirectory)) {
				t.Fatal("real command used the mutable shared resource directory")
			}
			if _, err := os.Lstat(string(inputDirectory)); !os.IsNotExist(err) {
				t.Fatal("real command input snapshot was not removed")
			}
			if verifyResourceSnapshot(installer.Resources, resourceContents(installer.inputs)) != nil {
				t.Fatal("real command changed the shared resource directory")
			}
		})
	}
}

func TestReviewLinkedFileRecognizesWindowsReparseAttributes(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal("synthetic reparse fixture failed")
	}
	for _, attributes := range []uint32{0, 0x400} {
		actual := linkedFile(reviewWindowsFileInfo{FileInfo: info, attributes: attributes})
		if actual != (attributes == 0x400) {
			t.Fatal("Windows reparse attributes were not rejected")
		}
	}
}

type reviewWindowsFileInfo struct {
	os.FileInfo
	attributes uint32
}

func (info reviewWindowsFileInfo) Sys() any {
	return &struct{ FileAttributes uint32 }{FileAttributes: info.attributes}
}
