//go:build runtime_acceptance

package managed

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"liberty.local/liberty/internal/infrastructure/profile"
)

func TestInstalledRuntimeInventoryAcceptance(t *testing.T) {
	directory := os.Getenv("LIBERTY_NATIVE_RUNTIME_ACCEPTANCE_DIR")
	profileID := os.Getenv("LIBERTY_NATIVE_RUNTIME_ACCEPTANCE_PROFILE")
	if profileID == "" {
		profileID = ProductionRuntimeProfile
	}
	if !filepath.IsAbs(directory) || (profileID != SecurityCandidateProfile && profileID != ProductionRuntimeProfile) {
		t.Fatal("explicit isolated runtime acceptance required")
	}
	marker, err := os.ReadFile(filepath.Join(directory, ".native-runtime-acceptance"))
	if err != nil || string(marker) != "isolated-runtime-acceptance-v1\n" {
		t.Fatal("candidate runtime acceptance marker required")
	}
	owner, err := profile.Acquire(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	installer, err := NewInstallerWithProfile(owner.Directory(), CurrentPlatform(), profileID)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := installer.openGeneration("python", 1)
	if err != nil {
		t.Fatal("accepted candidate interpreter unavailable")
	}
	python := findCandidate(generation, generation.Name(), installer.Platform.PythonCandidates)
	generation.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if python == "" || installer.validate(ctx, "python", python, true, nil) != nil {
		t.Fatal("candidate inventory requires a validated installed interpreter")
	}
	inputDigest := installer.InputDigest
	if profileID == ProductionRuntimeProfile {
		if err := installer.ensureProductionVendor(ctx, python, nil); err != nil {
			t.Fatal("production installed inventory requires verified vendor inputs", err)
		}
		inputs := make(map[string][]byte, len(runtimeInputNames()))
		for _, name := range runtimeInputNames() {
			if content, ok := installer.inputs[name]; ok {
				inputs[name] = content
			} else {
				inputs[name] = (*installer.productionVendor.Load())[name]
			}
		}
		inputDigest = resourceDigest(inputs)
	}
	if err := installer.Commands.Run(ctx, python, []string{"-I", "-m", "pip", "--isolated", "check"}, installer.Resources, installer.environment(), nil); err != nil {
		t.Fatal("candidate installed dependencies failed pip check", err)
	}
	filename := filepath.Join(owner.Directory(), "installed-inventory.json")
	script := "import importlib.metadata,json,pathlib,sys\ncomponents=sorted([{'name':distribution.metadata['Name'],'version':distribution.version} for distribution in importlib.metadata.distributions()],key=lambda component:component['name'].lower())\npathlib.Path(sys.argv[1]).write_text(json.dumps({'runtimeProfile':sys.argv[2],'runtimeInputSha256':sys.argv[3],'platform':sys.argv[4],'productionSourceSha256':sys.argv[5],'components':components},sort_keys=True)+'\\n',encoding='utf-8')"
	if err := installer.Commands.Run(ctx, python, []string{"-I", "-c", script, filename, installer.RuntimeProfile, inputDigest, installer.Platform.ID, installer.InputDigest}, installer.Resources, installer.environment(), nil); err != nil {
		t.Fatal("candidate installed inventory could not be collected", err)
	}
	var inventory struct {
		RuntimeProfile string `json:"runtimeProfile"`
		InputDigest    string `json:"runtimeInputSha256"`
		SourceDigest   string `json:"productionSourceSha256"`
		Platform       string `json:"platform"`
		Components     []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"components"`
	}
	data, err := os.ReadFile(filename)
	if err != nil || json.Unmarshal(data, &inventory) != nil || inventory.RuntimeProfile != installer.RuntimeProfile || inventory.InputDigest != inputDigest || inventory.SourceDigest != installer.InputDigest || inventory.Platform != installer.Platform.ID || len(inventory.Components) < 80 {
		t.Fatal("candidate installed inventory is incomplete or unbound")
	}
	t.Logf("candidate installed inventory collected: %d distributions", len(inventory.Components))
}
