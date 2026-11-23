package domain

import "testing"

func TestPetGeneratedCatalogAndLevelStages(t *testing.T) {
	if got := len(PetCatalog()); got != 77 {
		t.Fatalf("catalog count=%d", got)
	}
	for level, stage := range map[int64]string{1: "first_meet", 11: "familiar", 31: "steady_companion", 61: "grow_together", 101: "tacit_bond", 141: "deep_bond", 181: "long_company", 221: "bond_forever"} {
		if got := PetLevel(PetExperienceForLevel(level)).CurrentStage; got != stage {
			t.Fatalf("level %d stage=%s, want %s", level, got, stage)
		}
	}
	if got := PetLevel(0); got.Level != 1 || got.NextLevelRequired != PetLevelRequirement(1) {
		t.Fatalf("level one snapshot: %+v", got)
	}
	if got := PetLevel(PetExperienceForLevel(255)); !got.IsMaxLevel || got.NextStage != nil {
		t.Fatalf("max snapshot: %+v", got)
	}
}

func TestPetProfileRetainsKnownStageAndNormalizesLegacyAliases(t *testing.T) {
	p := PetNormalizeProfile(PetProfile{Level: 3, Experience: 0, Stage: "mature"}, true)
	if p.Level != 3 || p.Experience != PetExperienceForLevel(3) || p.Stage != "deep_bond" {
		t.Fatalf("legacy floor: %+v", p)
	}
}
