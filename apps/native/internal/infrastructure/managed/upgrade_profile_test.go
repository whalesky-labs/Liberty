package managed

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFunASRUpgradeProfileKeepsProductionAndRejectsMixedInputs(t *testing.T) {
	m, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	selected, candidate, err := m.withProfile(FunASRUpgradeProfile)
	if err != nil || candidate == nil || selected.RuntimeVersion != "2026.11.25-funasr-1.4.29-candidate.1" {
		t.Fatal("upgrade not selectable", err)
	}
	if selected.ModelSetVersion != m.ModelSetVersion || !reflect.DeepEqual(selected.LiveASR, m.LiveASR) || m.RuntimeVersion != "2026.11.22-python311-security.1" {
		t.Fatal("candidate modified production models or runtime")
	}
	wheel := false
	for _, file := range candidate.Files {
		wheel = wheel || file.Name == "funasr-1.4.29+liberty.local1-py3-none-any.whl"
	}
	if !wheel {
		t.Fatal("wrong candidate wheel")
	}
	changed := *candidate
	changed.Files = m.CandidateProfiles[SecurityCandidateProfile].Files
	m.CandidateProfiles[FunASRUpgradeProfile] = changed
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseManifest(raw); err == nil {
		t.Fatal("mixed sidecar accepted")
	}
}
