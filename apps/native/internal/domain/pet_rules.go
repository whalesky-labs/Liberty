package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

var ErrPet = errors.New("pet_operation_invalid")

const PetID = "default-pet"
const PetGiftBox = "gift-box-tool"
const PetMakeupTicket = "gem-ticket-tool"

var petCatalog = decodePetCatalog()
var petStages = decodePetStages()

func decodePetCatalog() []PetStoreCatalogItem {
	var items []PetStoreCatalogItem
	if json.Unmarshal([]byte(petCatalogJSON), &items) != nil {
		panic("invalid generated pet catalog")
	}
	return items
}
func decodePetStages() []PetStageThreshold {
	var stages []PetStageThreshold
	if json.Unmarshal([]byte(petStagesJSON), &stages) != nil {
		panic("invalid generated pet stages")
	}
	return stages
}
func PetCatalog() []PetStoreCatalogItem { return append([]PetStoreCatalogItem(nil), petCatalog...) }
func PetStages() []PetStageThreshold    { return append([]PetStageThreshold(nil), petStages...) }
func FindPetItem(key string) (PetStoreCatalogItem, bool) {
	for _, item := range petCatalog {
		if item.ItemKey == key {
			return item, true
		}
	}
	return PetStoreCatalogItem{}, false
}
func PetExperienceForLevel(level int64) int64 {
	var xp int64
	for n := int64(1); n < min(max(level, 1), 255); n++ {
		xp += PetLevelRequirement(n)
	}
	return xp
}
func PetLevel(experience int64) PetLevelSnapshot {
	xp := max(experience, 0)
	s := PetLevelSnapshot{Level: 1, TotalExperience: xp}
	for s.Level < 255 && xp >= PetLevelRequirement(s.Level) {
		xp -= PetLevelRequirement(s.Level)
		s.Level++
	}
	s.CurrentLevelExp, s.NextLevelRequired, s.IsMaxLevel = xp, PetLevelRequirement(s.Level), s.Level == 255
	s.ProgressRatio = 1
	if s.NextLevelRequired > 0 {
		s.ProgressRatio = min(float64(xp)/float64(s.NextLevelRequired), 1)
	}
	for _, stage := range petStages {
		if s.Level >= stage.Level {
			s.CurrentStage, s.CurrentStageLabelZh, s.CurrentStageLabelEn = stage.Stage, stage.LabelZh, stage.LabelEn
		} else {
			next, level := stage.Stage, stage.Level
			s.NextStage, s.NextStageLevel = &next, &level
			break
		}
	}
	return s
}
func PetStage(stage string) PetStageThreshold {
	switch stage {
	case "baby":
		stage = "first_meet"
	case "growing":
		stage = "grow_together"
	case "mature":
		stage = "deep_bond"
	}
	for _, s := range petStages {
		if s.Stage == stage {
			return s
		}
	}
	return petStages[0]
}
func PetNormalizeProfile(p PetProfile, keepStage bool) PetProfile {
	s := PetLevel(p.Experience)
	if p.Level > s.Level {
		p.Experience = PetExperienceForLevel(p.Level)
		s = PetLevel(p.Experience)
	}
	p.LevelSnapshot, p.Level = s, s.Level
	stage := PetStage(p.Stage)
	known := p.Stage == "baby" || p.Stage == "growing" || p.Stage == "mature"
	for _, threshold := range petStages {
		known = known || threshold.Stage == p.Stage
	}
	if !keepStage || !known {
		p.Stage = s.CurrentStage
	} else {
		p.Stage = stage.Stage
	}
	return p
}
func PetGrowthMultiplier(level int64) float64 {
	switch {
	case level <= 30:
		return 1
	case level <= 60:
		return 1.15
	case level <= 100:
		return 1.3
	case level <= 140:
		return 1.45
	case level <= 180:
		return 1.6
	case level <= 220:
		return 1.75
	default:
		return 2
	}
}
func PetLPMultiplier(level int64) float64 {
	switch {
	case level <= 60:
		return 1
	case level <= 140:
		return 1.15
	case level <= 220:
		return 1.3
	default:
		return 1.45
	}
}
func PetScale(base int64, multiplier float64) int64 {
	if base <= 0 {
		return 0
	}
	return int64(math.Min(math.Round(float64(base)*multiplier), float64(math.MaxInt64)))
}
func PetDuplicateCompensation(item PetStoreCatalogItem) int64 {
	if item.ItemType == "none" {
		return 0
	}
	if item.ItemType == "none" || item.Slot == "consumable" {
		return 0
	}
	if item.PriceLP > 0 {
		return min(max(item.PriceLP/4, 10), 160)
	}
	return map[string]int64{"first_meet": 10, "familiar": 16, "grow_together": 24, "deep_bond": 36, "bond_forever": 64}[item.Rarity]
}
func PetLockReason(item PetStoreCatalogItem, p PetProfile, counters []PetMilestoneCounter) (string, string) {
	if item.LevelGate > 1 && p.Level < item.LevelGate {
		return fmt.Sprintf("需要达到 %d 级。", item.LevelGate), fmt.Sprintf("Requires level %d.", item.LevelGate)
	}
	if item.StageGate != "" && PetStage(p.Stage).Level < PetStage(item.StageGate).Level {
		s := PetStage(item.StageGate)
		return "需要进入" + s.LabelZh + "。", "Requires " + s.LabelEn + " stage."
	}
	if key, value, ok := strings.Cut(item.MilestoneGate, ":"); ok {
		required, err := strconv.ParseInt(value, 10, 64)
		if err == nil {
			var current int64
			for _, c := range counters {
				if c.CounterKey == key {
					current = c.CounterValue
				}
			}
			if current < required {
				zh, en := PetCounterLabels(key)
				return fmt.Sprintf("%s：%d/%d。", zh, current, required), fmt.Sprintf("%s: %d/%d.", en, current, required)
			}
		}
	}
	return "", ""
}
func PetCounterLabels(key string) (string, string) {
	labels := map[string][2]string{"tasks_created": {"创建任务", "Tasks created"}, "transcriptions_completed": {"完成转写", "Transcriptions completed"}, "summaries_completed": {"完成 AI 总结", "AI summaries completed"}, "exports_completed": {"导出结果", "Exports completed"}, "active_days": {"活跃天数", "Active days"}, "dark_theme_days": {"深色主题使用天数", "Dark-theme days"}, "check_in_streak": {"连续签到", "Check-in streak"}}
	if p, ok := labels[key]; ok {
		return p[0], p[1]
	}
	return "里程碑", "Milestone"
}

func PetStageRatio(stage string) float64 {
	ratios := map[string]float64{"first_meet": 1, "familiar": 1.02, "steady_companion": 1.03, "grow_together": 1.04, "tacit_bond": 1.05, "deep_bond": 1.06, "long_company": 1.07, "bond_forever": 1.08, "baby": 1, "growing": 1.04, "mature": 1.08}
	if value, ok := ratios[stage]; ok {
		return value
	}
	return 1
}

type PetWorkflowRule struct {
	Growth, LP    int64
	Mood, Counter string
}

func PetWorkflow(source string) PetWorkflowRule {
	switch source {
	case "job_created":
		return PetWorkflowRule{5, 15, "cheerful", "tasks_created"}
	case "transcription_started":
		return PetWorkflowRule{3, 5, "excited", ""}
	case "transcription_completed":
		return PetWorkflowRule{12, 40, "proud", "transcriptions_completed"}
	case "ai_summary_completed":
		return PetWorkflowRule{10, 30, "proud", "summaries_completed"}
	case "export_completed":
		return PetWorkflowRule{6, 20, "proud", "exports_completed"}
	case "daily_open":
		return PetWorkflowRule{2, 10, "idle", "active_days"}
	case "dark_theme_used":
		return PetWorkflowRule{0, 0, "idle", "dark_theme_days"}
	default:
		return PetWorkflowRule{1, 0, "idle", ""}
	}
}
