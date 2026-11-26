package domain

import (
	"errors"
	"fmt"
	"math"
	"time"
)

const (
	FarmEmpty      = "empty"
	FarmPlanted    = "planted"
	FarmNeedsWater = "needs_water"
	FarmMature     = "mature"
	WorkIdle       = "idle"
	WorkRunning    = "running"
	WorkNeedsCare  = "needsCare"
	WorkClaimable  = "claimable"
)

var (
	ErrPlotOccupied     = errors.New("这块地还不能播种。")
	ErrPlotNotThirsty   = errors.New("这块地暂时不需要浇水。")
	ErrCropNotMature    = errors.New("作物还没有成熟。")
	ErrWorkNotIdle      = errors.New("这个岗位还没有空闲。")
	ErrWorkNotNeedsCare = errors.New("这个岗位暂时不需要照看。")
	ErrWorkNotClaimable = errors.New("这个岗位还不能领奖。")
)

type MissingSeed struct{ Crop FarmCropConfig }

func (e *MissingSeed) Error() string {
	return fmt.Sprintf("缺少%s种子，请先去宠物商店购买。", e.Crop.NameZh)
}

func WorkNextStage(now time.Time, duration, care int64) (string, error) {
	if care < 0 || duration < 0 {
		return "", ErrPet
	}
	seconds := max(duration/max(care+1, 1), int64(60))
	return PersistedUTC(now.Add(time.Duration(seconds) * time.Second)), nil
}

func WorkDue(target, now string) (bool, error) {
	seconds, err := workSecondsUntil(target, now)
	if err != nil {
		return false, err
	}
	return seconds == 0, nil
}

func workSecondsUntil(target, now string) (int64, error) {
	t, err := time.Parse(time.RFC3339Nano, target)
	if err != nil {
		return 0, fmt.Errorf("时间格式无效: %w", err)
	}
	n, err := time.Parse(time.RFC3339Nano, now)
	if err != nil {
		return 0, fmt.Errorf("时间格式无效: %w", err)
	}
	return max(int64(t.Sub(n)/time.Second), int64(0)), nil
}

func workElapsed(now, started string) (int64, error) {
	t, err := time.Parse(time.RFC3339Nano, started)
	if err != nil {
		return 0, err
	}
	n, err := time.Parse(time.RFC3339Nano, now)
	if err != nil {
		return 0, err
	}
	return max(int64(n.Sub(t)/time.Second), int64(0)), nil
}

func WorkRewards(primary string, quantity int64, bonus *string, chance int64, event string) []FarmRewardItem {
	values := []FarmRewardItem{{ItemKey: primary, Quantity: max(quantity, int64(1)), RewardType: "primary"}}
	if bonus != nil && deterministicNumber(event+":bonus", 100) < chance {
		values = append(values, FarmRewardItem{ItemKey: *bonus, Quantity: 1, RewardType: "bonus"})
	}
	return values
}

func WorkLP(minimum, maximum int64, event string) int64 {
	low, high := min(minimum, maximum), max(minimum, maximum)
	if high <= 0 || high-low == math.MaxInt64 {
		return 0
	}
	span := high - low + 1
	return low + deterministicNumber(event, span)
}

func deterministicNumber(seed string, modulo int64) int64 {
	if modulo <= 1 {
		return 0
	}
	var hash int64
	for _, b := range []byte(seed) {
		hash = hash*31 + int64(b)
	}
	if hash == math.MinInt64 { // Rust i64::abs panics for this one value; define a deterministic safe result.
		return 0
	}
	if hash < 0 {
		hash = -hash
	}
	return hash % modulo
}

func EnrichFarmPlot(v *FarmPlot, now string) {
	crop, ok := FindFarmCrop(v.CropKey)
	if v.Status == FarmEmpty || !ok {
		v.ProgressRatio = 0
		v.RemainingSeconds = 0
		return
	}
	v.Crop = &crop
	if v.PlantedAt != nil {
		elapsed, err := workElapsed(now, *v.PlantedAt)
		if err == nil && crop.DurationSeconds > 0 {
			v.ProgressRatio = math.Min(1, math.Max(0, float64(elapsed)/float64(crop.DurationSeconds)))
		}
	}
	if v.NextCareAt != nil {
		v.RemainingSeconds, _ = workSecondsUntil(*v.NextCareAt, now)
	}
}

func FindFarmCrop(key string) (FarmCropConfig, bool) {
	for _, v := range FarmCropCatalog() {
		if v.CropKey == key {
			return v, true
		}
	}
	return FarmCropConfig{}, false
}

func FarmStatus(plots []FarmPlot) string {
	for _, v := range plots {
		if v.Status == FarmMature {
			return WorkClaimable
		}
	}
	for _, v := range plots {
		if v.Status == FarmNeedsWater {
			return WorkNeedsCare
		}
	}
	for _, v := range plots {
		if v.Status == FarmPlanted {
			return WorkRunning
		}
	}
	return WorkIdle
}

func FarmSummary(plots []FarmPlot) WorkMapSummary {
	v := WorkMapSummary{Status: FarmStatus(plots)}
	for _, p := range plots {
		if p.Status != FarmEmpty {
			v.ActivePlots++
		}
		if p.Status == FarmNeedsWater {
			v.NeedsCarePlots++
		}
		if p.Status == FarmMature {
			v.MaturePlots++
		}
	}
	return v
}

func EnrichWorkTask(v *WorkGameTask, now string) {
	game, ok := FindWorkGame(v.GameKey)
	if !ok {
		return
	}
	var job *WorkGameJobConfig
	for i := range game.Jobs {
		j := game.Jobs[i]
		if j.JobKey == v.JobKey || (v.JobKey == "" && j.SlotIndex == v.SlotIndex) {
			job = &j
			break
		}
	}
	v.Job = job
	if v.Status == WorkIdle || job == nil {
		v.ProgressRatio = 0
		v.RemainingSeconds = 0
		return
	}
	if v.StartedAt != nil {
		elapsed, err := workElapsed(now, *v.StartedAt)
		if err == nil && job.DurationSeconds > 0 {
			v.ProgressRatio = math.Min(1, math.Max(0, float64(elapsed)/float64(job.DurationSeconds)))
		}
	}
	at := v.ClaimableAt
	if at == nil {
		at = v.NextCareAt
	}
	if at != nil {
		v.RemainingSeconds, _ = workSecondsUntil(*at, now)
	}
}

func WorkStatus(tasks []WorkGameTask) string {
	for _, v := range tasks {
		if v.Status == WorkClaimable {
			return WorkClaimable
		}
	}
	for _, v := range tasks {
		if v.Status == WorkNeedsCare {
			return WorkNeedsCare
		}
	}
	for _, v := range tasks {
		if v.Status == WorkRunning {
			return WorkRunning
		}
	}
	return WorkIdle
}

func WorkSummary(tasks []WorkGameTask) WorkMapSummary {
	v := WorkMapSummary{Status: WorkStatus(tasks)}
	for _, t := range tasks {
		if t.Status != WorkIdle {
			v.ActivePlots++
		}
		if t.Status == WorkNeedsCare {
			v.NeedsCarePlots++
		}
		if t.Status == WorkClaimable {
			v.MaturePlots++
		}
	}
	return v
}

func FindWorkGame(key string) (WorkGameDefinition, bool) {
	for _, g := range WorkGameCatalog() {
		if g.Key == key {
			return g, true
		}
	}
	return WorkGameDefinition{}, false
}

func FindWorkJob(gameKey, jobKey string) (WorkGameJobConfig, bool) {
	g, ok := FindWorkGame(gameKey)
	if !ok {
		return WorkGameJobConfig{}, false
	}
	for _, j := range g.Jobs {
		if j.JobKey == jobKey {
			return j, true
		}
	}
	return WorkGameJobConfig{}, false
}
