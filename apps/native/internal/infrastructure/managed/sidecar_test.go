package managed

import (
	"bytes"
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

func embeddedRuntimeManifest(t *testing.T) Manifest {
	t.Helper()
	manifest, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func runtimeInputFixture(t *testing.T, operatingSystem string) (Manifest, string, string, map[string][]byte) {
	t.Helper()
	manifest := embeddedRuntimeManifest(t)
	candidate := manifest.CandidateProfiles[SecurityCandidateProfile]
	contents := map[string][]byte{}
	for index := range candidate.Files {
		file := &candidate.Files[index]
		content := []byte("verified:" + file.Name + "\n")
		file.Bytes, file.SHA256 = int64(len(content)), digest(string(content))
		contents[file.Name] = content
	}
	manifest.CandidateProfiles[SecurityCandidateProfile] = candidate
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(base, "Candidate.exe")
	if operatingSystem == "darwin" {
		executable = filepath.Join(base, "Candidate.app", "Contents", "MacOS", "liberty-native")
	}
	directory, err := runtimeInputDirectory(executable, operatingSystem)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range contents {
		if err := os.WriteFile(filepath.Join(directory, name), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return manifest, executable, directory, contents
}

func candidateInstallerFixture(t *testing.T) (*Installer, string, map[string][]byte) {
	t.Helper()
	manifest, executable, directory, contents := runtimeInputFixture(t, "darwin")
	installer, err := newInstallerWithProfile(t.TempDir(), "darwin-aarch64", SecurityCandidateProfile, manifest, executable, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	return installer, directory, contents
}

func TestCandidateManifestSelectionKeepsProductionAndSharedAssets(t *testing.T) {
	manifest := embeddedRuntimeManifest(t)
	selected, candidate, err := manifest.withProfile(SecurityCandidateProfile)
	if err != nil || candidate == nil || selected.RuntimeVersion != "2026.11.20-security-candidate.2" || selected.PythonVersion != "3.11.18" {
		t.Fatal("candidate identity was not selected", err)
	}
	if manifest.PythonVersion != "3.11.18" || manifest.RuntimeVersion != "2026.11.22-python311-security.1" || selected.ModelSetVersion != manifest.ModelSetVersion || !reflect.DeepEqual(selected.Sources, manifest.Sources) {
		t.Fatal("production or inherited facts changed")
	}
	for _, original := range manifest.Platforms {
		actual, err := selected.Platform(original.ID)
		if err != nil || !reflect.DeepEqual(actual.FFmpeg, original.FFmpeg) || !reflect.DeepEqual(actual.PythonCandidates, original.PythonCandidates) || actual.Backend != original.Backend || actual.UnsupportedReason != original.UnsupportedReason {
			t.Fatal("platform fields were not inherited", original.ID)
		}
		for _, override := range candidate.Platforms {
			if original.ID == override.ID && !reflect.DeepEqual(actual.Python, override.Python) {
				t.Fatal("candidate did not select its pinned Python bundle")
			}
		}
	}
	for _, profileID := range []string{"", ProductionRuntimeProfile} {
		production, candidate, err := manifest.withProfile(profileID)
		if err != nil || candidate != nil || !reflect.DeepEqual(production, manifest) || ValidateRuntimeProfile(profileID) != nil {
			t.Fatal("default production profile changed")
		}
	}
	for _, profileID := range []string{"unknown", " production", "../security-candidate"} {
		if ValidateRuntimeProfile(profileID) == nil {
			t.Fatal("unknown runtime profile accepted")
		}
	}
}

func TestCandidateManifestRejectsIncompleteAndUnsafePins(t *testing.T) {
	mutations := map[string]func(*CandidateProfile){
		"same runtime": func(candidate *CandidateProfile) {
			candidate.RuntimeVersion = embeddedRuntimeManifest(t).RuntimeVersion
		},
		"invalid Python":       func(candidate *CandidateProfile) { candidate.PythonVersion = "3.11" },
		"unsupported platform": func(candidate *CandidateProfile) { candidate.Platforms[0].ID = "windows-x86" },
		"duplicate platform":   func(candidate *CandidateProfile) { candidate.Platforms[0].ID = candidate.Platforms[1].ID },
		"mutable bundle": func(candidate *CandidateProfile) {
			candidate.Platforms[0].Python.URLs["official"] = "https://example.invalid/latest/python.tar.gz"
		},
		"missing bundle":    func(candidate *CandidateProfile) { candidate.Platforms[0].Python = nil },
		"missing file":      func(candidate *CandidateProfile) { candidate.Files = candidate.Files[1:] },
		"duplicate file":    func(candidate *CandidateProfile) { candidate.Files[0] = candidate.Files[1] },
		"unrecognized file": func(candidate *CandidateProfile) { candidate.Files[0].Name = "unknown.whl" },
		"invalid hash":      func(candidate *CandidateProfile) { candidate.Files[0].SHA256 = "invalid" },
		"excessive file":    func(candidate *CandidateProfile) { candidate.Files[0].Bytes = 3 * 1024 * 1024 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			manifest := embeddedRuntimeManifest(t)
			candidate := manifest.CandidateProfiles[SecurityCandidateProfile]
			mutate(&candidate)
			manifest.CandidateProfiles[SecurityCandidateProfile] = candidate
			content, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parseManifest(content); !errors.Is(err, ErrManifest) {
				t.Fatal("unsafe candidate manifest accepted", err)
			}
		})
	}
}

func TestRuntimeInputsVerifyExactExecutableLayoutWithoutWriting(t *testing.T) {
	for _, operatingSystem := range []string{"darwin", "windows"} {
		t.Run(operatingSystem, func(t *testing.T) {
			manifest, executable, directory, expected := runtimeInputFixture(t, operatingSystem)
			selected, actual, inputDigest, err := loadRuntimeInputs(manifest, "darwin-x64", SecurityCandidateProfile, executable, operatingSystem)
			if err != nil || selected.PythonVersion != "3.11.18" || !reflect.DeepEqual(actual, expected) || inputDigest != resourceDigest(expected) {
				t.Fatal("verified inputs not loaded", err)
			}
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 8 {
				t.Fatal("preflight mutated the input directory")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(directory), "resources")); !os.IsNotExist(err) {
				t.Fatal("preflight materialized resources")
			}
			if _, _, _, err := loadRuntimeInputs(manifest, "windows-x86", SecurityCandidateProfile, executable, operatingSystem); !errors.Is(err, domain.ErrRuntimeUnsupported) {
				t.Fatal("candidate pretended to support x86", err)
			}
			if _, _, _, err := loadRuntimeInputs(manifest, "darwin-x64", SecurityCandidateProfile, filepath.Join(t.TempDir(), "missing"), operatingSystem); err == nil {
				t.Fatal("missing sidecar fell back")
			}
		})
	}
	if _, err := runtimeInputDirectory("relative.exe", "windows"); err == nil {
		t.Fatal("relative executable accepted")
	}
	if _, err := runtimeInputDirectory(filepath.Join(t.TempDir(), "liberty-native"), "darwin"); err == nil {
		t.Fatal("CWD-style macOS layout accepted")
	}
}

func TestRuntimeInputsRejectUntrustedDirectoryEntries(t *testing.T) {
	for _, mutation := range []string{"missing", "extra", "size", "digest", "directory", "symlink", "hardlink", "linked parent"} {
		t.Run(mutation, func(t *testing.T) {
			manifest, executable, directory, _ := runtimeInputFixture(t, "darwin")
			filename := filepath.Join(directory, "recipe.json")
			switch mutation {
			case "missing":
				if err := os.Remove(filename); err != nil {
					t.Fatal(err)
				}
			case "extra":
				if err := os.WriteFile(filepath.Join(directory, "extra.whl"), []byte("unknown"), 0600); err != nil {
					t.Fatal(err)
				}
			case "size":
				if err := os.WriteFile(filename, []byte("short"), 0600); err != nil {
					t.Fatal(err)
				}
			case "digest":
				content, err := os.ReadFile(filename)
				if err != nil {
					t.Fatal(err)
				}
				content[0] ^= 1
				if err := os.WriteFile(filename, content, 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Remove(filename); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filename, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink", "hardlink":
				outside := filepath.Join(t.TempDir(), "recipe.json")
				content, err := os.ReadFile(filename)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(outside, content, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filename); err != nil {
					t.Fatal(err)
				}
				if mutation == "symlink" {
					if err := os.Symlink(outside, filename); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Link(outside, filename); err != nil {
					t.Fatal(err)
				}
			case "linked parent":
				relocated := directory + "-relocated"
				if err := os.Rename(directory, relocated); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(relocated, directory); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, _, err := loadRuntimeInputs(manifest, "darwin-x64", SecurityCandidateProfile, executable, "darwin"); err == nil {
				t.Fatal("untrusted sidecar accepted")
			}
			profile := t.TempDir()
			if _, err := newInstallerWithProfile(profile, "darwin-x64", SecurityCandidateProfile, manifest, executable, "darwin"); err == nil {
				t.Fatal("sidecar failure fell back to production")
			}
			entries, err := os.ReadDir(profile)
			if err != nil || len(entries) != 0 {
				t.Fatal("invalid sidecar caused profile writes")
			}
		})
	}
}

func TestCandidateMaterializesVerifiedSnapshotAndRejectsPrivateTampering(t *testing.T) {
	installer, sidecar, expected := candidateInstallerFixture(t)
	if installer.RuntimeProfile != SecurityCandidateProfile || installer.InputDigest != resourceDigest(expected) {
		t.Fatal("candidate audit identity missing")
	}
	if installer.PlatformRoot() != filepath.Join(installer.Root, "runtime", "profiles", SecurityCandidateProfile, installer.Platform.ID) {
		t.Fatal("candidate shared the production runtime root")
	}
	for name, original := range expected {
		if err := os.WriteFile(filepath.Join(sidecar, name), []byte("changed after verification"), 0600); err != nil {
			t.Fatal(err)
		}
		actual, err := os.ReadFile(filepath.Join(installer.Resources, name))
		if err != nil || !bytes.Equal(actual, original) {
			t.Fatal("materialized bytes followed changed sidecar")
		}
	}
	if err := os.WriteFile(filepath.Join(installer.Resources, "recipe.json"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	installer.Commands = commandFunc(func(context.Context, string, []string, string, map[string]string, func(string) error) error {
		t.Fatal("tampered private inputs reached a process")
		return nil
	})
	if _, err := installer.InstallTool(context.Background(), "python", 1, "official", nil); !errors.Is(err, ErrResources) {
		t.Fatal("private tampering accepted", err)
	}
	if _, err := os.Stat(installer.PlatformRoot()); !os.IsNotExist(err) {
		t.Fatal("failed verification wrote a runtime generation")
	}
}

func TestCandidateRootsAndReadyMarkersCannotReuseProductionArtifacts(t *testing.T) {
	installer, _, _ := candidateInstallerFixture(t)
	production, err := NewInstaller(installer.Root, installer.Platform.ID)
	if err != nil {
		t.Fatal(err)
	}
	if production.PlatformRoot() == installer.PlatformRoot() {
		t.Fatal("runtime roots collided")
	}
	for _, component := range []string{"python", "ffmpeg", "model"} {
		directory, err := installer.generation(component, 1)
		if err != nil || !strings.HasPrefix(directory, installer.PlatformRoot()+string(filepath.Separator)) {
			t.Fatal("generation escaped profile root", err)
		}
		resolved := filepath.Join(directory, "artifact")
		version := installer.Manifest.RuntimeVersion
		if component == "model" {
			version = installer.Manifest.ModelSetVersion
			if err := os.Mkdir(resolved, 0700); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(resolved, []byte("verified"), 0700); err != nil {
			t.Fatal(err)
		}
		root, err := installer.openGeneration(component, 1)
		if err != nil {
			t.Fatal(err)
		}
		artifact, err := installer.publish(root, component, 1, resolved, version)
		if err != nil {
			t.Fatal(err)
		}
		if !installer.artifactBound(component, artifact) {
			t.Fatal("own verified artifact rejected")
		}
		content, err := root.ReadFile("ready.json")
		if err != nil {
			t.Fatal(err)
		}
		var marker readyMarker
		if err := json.Unmarshal(content, &marker); err != nil {
			t.Fatal(err)
		}
		if marker.RuntimeProfile != installer.RuntimeProfile || marker.InputDigest != installer.InputDigest {
			t.Fatal("ready marker lost input binding")
		}
		marker.InputDigest = strings.Repeat("0", 64)
		content, err = json.Marshal(marker)
		if err != nil {
			t.Fatal(err)
		}
		if err := atomicJSON(root, "ready.json", content); err != nil {
			t.Fatal(err)
		}
		root.Close()
		state := domain.NewRuntimeComponent(component, "managed", "")
		state.Artifact, state.Availability = &artifact, "ready"
		state, err = installer.Reconcile(state, domain.RuntimeOverview{}, domain.RuntimeSettings{})
		if err != nil || state.Artifact != nil || state.Operation.Phase != "artifact_unverified" {
			t.Fatal("wrong input binding accepted", err)
		}
		legacy := domain.RuntimeOverview{RuntimeVersion: version, PythonPath: &resolved, ModelsRoot: &resolved, InstallRoot: &directory}
		state, err = installer.Reconcile(domain.NewRuntimeComponent(component, "managed", ""), legacy, domain.RuntimeSettings{})
		if err != nil || state.Artifact != nil {
			t.Fatal("candidate adopted an unbound legacy artifact", err)
		}
	}
	downloads, err := installer.downloads()
	if err != nil {
		t.Fatal(err)
	}
	downloads.Close()
	if err := installer.AppendLog("python", "synthetic_status"); err != nil {
		t.Fatal(err)
	}
	if text, err := installer.Log("python"); err != nil || !strings.Contains(text, "synthetic_status") {
		t.Fatal("candidate log unavailable", err)
	}
	if _, err := os.Stat(production.PlatformRoot()); !os.IsNotExist(err) {
		t.Fatal("candidate wrote production runtime state")
	}
}

func TestRuntimeResourcesRejectUnexpectedWheel(t *testing.T) {
	directory := t.TempDir()
	materialized, err := MaterializeResources(directory)
	if err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(materialized, "unknown-1-py3-none-any.whl")
	if err := os.WriteFile(unknown, []byte("untrusted"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeResources(directory); err == nil {
		t.Fatal("unexpected wheel accepted in the verified resource directory")
	}
	if content, err := os.ReadFile(unknown); err != nil || string(content) != "untrusted" {
		t.Fatal("unknown file was overwritten or removed")
	}
}

func TestRuntimeResourcesRejectLinkedResourceDirectory(t *testing.T) {
	directory := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(directory, "resources")); err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeResources(directory); err == nil {
		t.Fatal("linked resource directory accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("resource materialization wrote through the link")
	}
}
