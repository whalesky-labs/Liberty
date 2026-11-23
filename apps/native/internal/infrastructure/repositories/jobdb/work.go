package jobdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"liberty.local/liberty/internal/domain"
)

const (
	farmEmpty      = "empty"
	farmPlanted    = "planted"
	farmNeedsWater = "needs_water"
	farmMature     = "mature"
	workIdle       = "idle"
	workRunning    = "running"
	workNeedsCare  = "needsCare"
	workClaimable  = "claimable"
)

func formatWorkTime(now time.Time) string {
	return domain.PersistedUTC(now)
}

func errorsText(message string) error { return errors.New(message) }

func ensureWorkPet(ctx context.Context, tx *sql.Tx, now string) error {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM pet_profile WHERE id='default-pet'`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ensurePet(ctx, tx, now)
	}
	if err != nil {
		return ErrDatabase
	}
	return nil
}

func (s *Store) FarmState(ctx context.Context, now time.Time) (domain.FarmState, error) {
	var value domain.FarmState
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		value, err = farmState(ctx, tx, formatWorkTime(now))
		return err
	})
	return value, err
}

func (s *Store) WorkMarketState(ctx context.Context, now time.Time) (domain.WorkMarketState, error) {
	var value domain.WorkMarketState
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		value, err = workMarketState(ctx, tx, formatWorkTime(now))
		return err
	})
	return value, err
}

func (s *Store) HarvestLedger(ctx context.Context, limit int) ([]domain.FarmHarvestLedgerEntry, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return listHarvests(ctx, tx, limit)
}

func (s *Store) PlantCrop(ctx context.Context, plotID, cropKey string, now time.Time) (domain.FarmState, error) {
	var state domain.FarmState
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		nowText := formatWorkTime(now)
		if err := ensureFarmPlots(ctx, tx, nowText); err != nil {
			return err
		}
		if err := refreshFarmPlots(ctx, tx, nowText); err != nil {
			return err
		}
		plot, err := loadFarmPlot(ctx, tx, plotID)
		if err != nil {
			return err
		}
		if plot.Status != farmEmpty {
			return errorsText("这块地还不能播种。")
		}
		crop, ok := findFarmCrop(cropKey)
		if !ok {
			return errorsText("作物不存在。")
		}
		if _, err := consumePetItem(ctx, tx, crop.SeedItemKey, 1, nowText); err != nil {
			return fmt.Errorf("缺少%s，请先去宠物商店购买。", crop.NameZh+"种子")
		}
		next, err := workNextStage(now, crop.DurationSeconds, crop.WaterRequired)
		if err != nil {
			return err
		}
		if err := execute(ctx, tx, `UPDATE farm_plots SET crop_key=?,status=?,stage_index=0,planted_at=?,last_watered_at=NULL,next_care_at=?,mature_at=NULL,updated_at=? WHERE id=?`, crop.CropKey, farmPlanted, nowText, next, nowText, plotID); err != nil {
			return err
		}
		state, err = farmState(ctx, tx, nowText)
		return err
	})
	return state, err
}

func (s *Store) WaterPlot(ctx context.Context, plotID string, now time.Time) (domain.FarmState, error) {
	var state domain.FarmState
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		nowText := formatWorkTime(now)
		if err := ensureFarmPlots(ctx, tx, nowText); err != nil {
			return err
		}
		if err := refreshFarmPlots(ctx, tx, nowText); err != nil {
			return err
		}
		plot, err := loadFarmPlot(ctx, tx, plotID)
		if err != nil {
			return err
		}
		if plot.Status != farmNeedsWater {
			return errorsText("这块地暂时不需要浇水。")
		}
		crop, ok := findFarmCrop(plot.CropKey)
		if !ok {
			return errorsText("作物配置不存在。")
		}
		nextStage := plot.StageIndex + 1
		next, err := workNextStage(now, crop.DurationSeconds, crop.WaterRequired)
		if err != nil {
			return err
		}
		var mature any
		if nextStage >= crop.WaterRequired {
			mature = next
		}
		if err := execute(ctx, tx, `UPDATE farm_plots SET status=?,stage_index=?,last_watered_at=?,next_care_at=?,mature_at=?,updated_at=? WHERE id=?`, farmPlanted, nextStage, nowText, next, mature, nowText, plotID); err != nil {
			return err
		}
		state, err = farmState(ctx, tx, nowText)
		return err
	})
	return state, err
}

func (s *Store) HarvestPlot(ctx context.Context, plotID string, now time.Time) (domain.FarmHarvestResult, error) {
	var result domain.FarmHarvestResult
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		nowText := formatWorkTime(now)
		if err := ensureFarmPlots(ctx, tx, nowText); err != nil {
			return err
		}
		if err := refreshFarmPlots(ctx, tx, nowText); err != nil {
			return err
		}
		if err := ensureWorkPet(ctx, tx, nowText); err != nil {
			return err
		}
		if err := ensurePetStore(ctx, tx, nowText); err != nil {
			return err
		}
		plot, err := loadFarmPlot(ctx, tx, plotID)
		if err != nil {
			return err
		}
		if plot.Status != farmMature {
			return errorsText("作物还没有成熟。")
		}
		crop, ok := findFarmCrop(plot.CropKey)
		if !ok {
			return errorsText("作物配置不存在。")
		}
		if plot.PlantedAt == nil || strings.TrimSpace(*plot.PlantedAt) == "" {
			return errorsText("这轮种植缺少开始时间，不能领取奖励。")
		}
		eventKey := plot.ID + ":" + crop.CropKey + ":" + *plot.PlantedAt
		metadata := crop.CropKey
		claimed, err := claimPetEvent(ctx, tx, "farm_harvest", eventKey, &metadata, nowText)
		if err != nil {
			return err
		}
		ledgerID := "farm-harvest:" + eventKey
		var harvest domain.FarmHarvestLedgerEntry
		if !claimed {
			harvest, err = loadHarvestEntry(ctx, tx, ledgerID)
			if err != nil {
				return err
			}
			if harvest.ID == "" {
				return errorsText("这轮收获已经领取，无法恢复原收获记录。")
			}
			if err := clearFarmPlot(ctx, tx, plot.ID, nowText); err != nil {
				return err
			}
			result.State, err = farmState(ctx, tx, nowText)
			result.Harvest = harvest
			return err
		}
		rewards := cropRewards(crop, eventKey)
		lp := workLP(crop.LPMin, crop.LPMax, eventKey+":lp")
		for _, reward := range rewards {
			item, found := domain.FindPetItem(reward.ItemKey)
			if !found {
				return fmt.Errorf("宠物商品不存在: %s", reward.ItemKey)
			}
			if err := grantPetItem(ctx, tx, item, reward.Quantity, "farm_harvest", nowText); err != nil {
				return err
			}
		}
		if lp > 0 {
			if _, err := grantPetReward(ctx, tx, "farm_harvest", eventKey, lp, &metadata, nowText); err != nil {
				return err
			}
		}
		harvest = domain.FarmHarvestLedgerEntry{ID: ledgerID, PlotID: plot.ID, CropKey: crop.CropKey, Rewards: rewards, LPReward: lp, CreatedAt: nowText}
		if err := insertHarvestEntry(ctx, tx, harvest); err != nil {
			return err
		}
		if err := clearFarmPlot(ctx, tx, plot.ID, nowText); err != nil {
			return err
		}
		result.State, err = farmState(ctx, tx, nowText)
		result.Harvest = harvest
		return err
	})
	return result, err
}

func (s *Store) WorkGameState(ctx context.Context, gameKey string, now time.Time) (domain.WorkGameState, error) {
	var state domain.WorkGameState
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		state, err = workGameState(ctx, tx, gameKey, formatWorkTime(now))
		return err
	})
	return state, err
}

func (s *Store) StartWorkTask(ctx context.Context, gameKey, taskID, jobKey string, now time.Time) (domain.WorkGameState, error) {
	var state domain.WorkGameState
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		nowText := formatWorkTime(now)
		if err := ensureWorkTasks(ctx, tx, nowText); err != nil {
			return err
		}
		if err := refreshWorkTasks(ctx, tx, nowText); err != nil {
			return err
		}
		task, err := loadWorkTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		if task.GameKey != gameKey {
			return errorsText("岗位不属于当前地图。")
		}
		if task.Status != workIdle {
			return errorsText("这个岗位还没有空闲。")
		}
		job, ok := findWorkJob(gameKey, jobKey)
		if !ok {
			return errorsText("岗位配置不存在。")
		}
		if job.SlotIndex != task.SlotIndex {
			return errorsText("岗位配置和当前热区不匹配。")
		}
		next, err := workNextStage(now, job.DurationSeconds, job.CareRequired)
		if err != nil {
			return err
		}
		if err := execute(ctx, tx, `UPDATE work_game_tasks SET job_key=?,status=?,stage_index=0,started_at=?,last_cared_at=NULL,next_care_at=?,claimable_at=NULL,updated_at=? WHERE id=?`, job.JobKey, workRunning, nowText, next, nowText, taskID); err != nil {
			return err
		}
		state, err = workGameState(ctx, tx, gameKey, nowText)
		return err
	})
	return state, err
}

func (s *Store) CareWorkTask(ctx context.Context, gameKey, taskID string, now time.Time) (domain.WorkGameState, error) {
	var state domain.WorkGameState
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		nowText := formatWorkTime(now)
		if err := ensureWorkTasks(ctx, tx, nowText); err != nil {
			return err
		}
		if err := refreshWorkTasks(ctx, tx, nowText); err != nil {
			return err
		}
		task, err := loadWorkTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		if task.GameKey != gameKey {
			return errorsText("岗位不属于当前地图。")
		}
		if task.Status != workNeedsCare {
			return errorsText("这个岗位暂时不需要照看。")
		}
		job, ok := findWorkJob(gameKey, task.JobKey)
		if !ok {
			return errorsText("岗位配置不存在。")
		}
		nextStage := task.StageIndex + 1
		next, err := workNextStage(now, job.DurationSeconds, job.CareRequired)
		if err != nil {
			return err
		}
		var careAt, claimAt any
		if nextStage >= job.CareRequired {
			claimAt = next
		} else {
			careAt = next
		}
		if err := execute(ctx, tx, `UPDATE work_game_tasks SET status=?,stage_index=?,last_cared_at=?,next_care_at=?,claimable_at=?,updated_at=? WHERE id=?`, workRunning, nextStage, nowText, careAt, claimAt, nowText, taskID); err != nil {
			return err
		}
		state, err = workGameState(ctx, tx, gameKey, nowText)
		return err
	})
	return state, err
}

func (s *Store) ClaimWorkTask(ctx context.Context, gameKey, taskID string, now time.Time) (domain.WorkGameClaimResult, error) {
	var result domain.WorkGameClaimResult
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		nowText := formatWorkTime(now)
		if err := ensureWorkTasks(ctx, tx, nowText); err != nil {
			return err
		}
		if err := refreshWorkTasks(ctx, tx, nowText); err != nil {
			return err
		}
		if err := ensureWorkPet(ctx, tx, nowText); err != nil {
			return err
		}
		if err := ensurePetStore(ctx, tx, nowText); err != nil {
			return err
		}
		task, err := loadWorkTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		if task.GameKey != gameKey {
			return errorsText("岗位不属于当前地图。")
		}
		if task.Status != workClaimable {
			return errorsText("这个岗位还不能领奖。")
		}
		job, ok := findWorkJob(gameKey, task.JobKey)
		if !ok {
			return errorsText("岗位配置不存在。")
		}
		if task.StartedAt == nil || strings.TrimSpace(*task.StartedAt) == "" {
			return errorsText("这轮工作缺少开始时间，不能领取奖励。")
		}
		eventKey := task.ID + ":" + job.JobKey + ":" + *task.StartedAt
		metadata := gameKey
		claimed, err := claimPetEvent(ctx, tx, "work_game_reward", eventKey, &metadata, nowText)
		if err != nil {
			return err
		}
		ledgerID := "work-game-reward:" + eventKey
		var reward domain.WorkGameRewardLedgerEntry
		if !claimed {
			reward, err = loadWorkRewardEntry(ctx, tx, ledgerID)
			if err != nil {
				return err
			}
			if reward.ID == "" {
				return errorsText("这轮工作已经领奖，无法恢复原奖励记录。")
			}
			if err := clearWorkTask(ctx, tx, task.ID, nowText); err != nil {
				return err
			}
			result.State, err = workGameState(ctx, tx, gameKey, nowText)
			result.Reward = reward
			return err
		}
		rewards := jobRewards(job, eventKey)
		lp := workLP(job.LPMin, job.LPMax, eventKey+":lp")
		for _, itemReward := range rewards {
			item, found := domain.FindPetItem(itemReward.ItemKey)
			if !found {
				return fmt.Errorf("宠物商品不存在: %s", itemReward.ItemKey)
			}
			if err := grantPetItem(ctx, tx, item, itemReward.Quantity, "work_game_reward", nowText); err != nil {
				return err
			}
		}
		if lp > 0 {
			if _, err := grantPetReward(ctx, tx, "work_game_reward", eventKey, lp, &metadata, nowText); err != nil {
				return err
			}
		}
		reward = domain.WorkGameRewardLedgerEntry{ID: ledgerID, GameKey: gameKey, TaskID: task.ID, JobKey: job.JobKey, Rewards: rewards, LPReward: lp, CreatedAt: nowText}
		if err := insertWorkRewardEntry(ctx, tx, reward); err != nil {
			return err
		}
		if err := clearWorkTask(ctx, tx, task.ID, nowText); err != nil {
			return err
		}
		result.State, err = workGameState(ctx, tx, gameKey, nowText)
		result.Reward = reward
		return err
	})
	return result, err
}

func farmState(ctx context.Context, tx *sql.Tx, now string) (domain.FarmState, error) {
	if err := ensureFarmPlots(ctx, tx, now); err != nil {
		return domain.FarmState{}, err
	}
	if err := refreshFarmPlots(ctx, tx, now); err != nil {
		return domain.FarmState{}, err
	}
	plots, err := listFarmPlots(ctx, tx, now)
	if err != nil {
		return domain.FarmState{}, err
	}
	harvests, err := listHarvests(ctx, tx, 12)
	if err != nil {
		return domain.FarmState{}, err
	}
	crops := domain.FarmCropCatalog()
	return domain.FarmState{Plots: plots, Crops: crops, Harvests: harvests, MapStatus: farmStatus(plots), UpdatedAt: now}, nil
}

func workMarketState(ctx context.Context, tx *sql.Tx, now string) (domain.WorkMarketState, error) {
	farm, err := farmState(ctx, tx, now)
	if err != nil {
		return domain.WorkMarketState{}, err
	}
	maps := []domain.WorkMap{{ID: "farm", NameZh: "农场种菜", NameEn: "Farm", DescriptionZh: "播种、浇水、等待成熟，收获宠物商品。", DescriptionEn: "Plant, water, wait, and harvest pet items.", Category: "farm", Status: farm.MapStatus, Route: "/farm", Outputs: []string{"food", "lp", "tool", "gift_box"}, Enabled: true, Summary: farmSummary(farm.Plots)}}
	games, err := workMaps(ctx, tx, now)
	if err != nil {
		return domain.WorkMarketState{}, err
	}
	maps = append(maps, games...)
	return domain.WorkMarketState{Maps: maps, UpdatedAt: now}, nil
}

func workGameState(ctx context.Context, tx *sql.Tx, key, now string) (domain.WorkGameState, error) {
	game, ok := findWorkGame(key)
	if !ok {
		return domain.WorkGameState{}, errorsText("打工地图不存在。")
	}
	if err := ensureWorkTasks(ctx, tx, now); err != nil {
		return domain.WorkGameState{}, err
	}
	if err := refreshWorkTasks(ctx, tx, now); err != nil {
		return domain.WorkGameState{}, err
	}
	tasks, err := listWorkTasks(ctx, tx, key, now)
	if err != nil {
		return domain.WorkGameState{}, err
	}
	rewards, err := listWorkRewards(ctx, tx, key, 12)
	if err != nil {
		return domain.WorkGameState{}, err
	}
	return domain.WorkGameState{GameKey: game.Key, NameZh: game.NameZh, NameEn: game.NameEn, DescriptionZh: game.DescriptionZh, DescriptionEn: game.DescriptionEn, MapStatus: workStatus(tasks), Tasks: tasks, Jobs: append([]domain.WorkGameJobConfig(nil), game.Jobs...), Rewards: rewards, UpdatedAt: now}, nil
}
func workMaps(ctx context.Context, tx *sql.Tx, now string) ([]domain.WorkMap, error) {
	if err := ensureWorkTasks(ctx, tx, now); err != nil {
		return nil, err
	}
	if err := refreshWorkTasks(ctx, tx, now); err != nil {
		return nil, err
	}
	games := domain.WorkGameCatalog()
	values := make([]domain.WorkMap, 0, len(games))
	for _, game := range games {
		tasks, err := listWorkTasks(ctx, tx, game.Key, now)
		if err != nil {
			return nil, err
		}
		summary := workSummary(tasks)
		values = append(values, domain.WorkMap{ID: game.Key, NameZh: game.NameZh, NameEn: game.NameEn, DescriptionZh: game.DescriptionZh, DescriptionEn: game.DescriptionEn, Category: game.Key, Status: summary.Status, Route: "/work-game/" + game.Key, Outputs: append([]string(nil), game.Outputs...), Enabled: true, Summary: summary})
	}
	return values, nil
}

func ensureFarmPlots(ctx context.Context, tx *sql.Tx, now string) error {
	for i := int64(1); i <= 3; i++ {
		if err := execute(ctx, tx, `INSERT OR IGNORE INTO farm_plots(id,plot_index,crop_key,status,stage_index,updated_at) VALUES(?,?, '', ?,0,?)`, fmt.Sprintf("farm-plot-%d", i), i, farmEmpty, now); err != nil {
			return err
		}
	}
	return nil
}
func ensureWorkTasks(ctx context.Context, tx *sql.Tx, now string) error {
	for _, g := range domain.WorkGameCatalog() {
		for _, j := range g.Jobs {
			id := fmt.Sprintf("%s-slot-%d", g.Key, j.SlotIndex)
			if err := execute(ctx, tx, `INSERT OR IGNORE INTO work_game_tasks(id,game_key,slot_index,job_key,status,stage_index,updated_at) VALUES(?,?,?,'',?,0,?)`, id, g.Key, j.SlotIndex, workIdle, now); err != nil {
				return err
			}
		}
	}
	return nil
}
func refreshFarmPlots(ctx context.Context, tx *sql.Tx, now string) error {
	values, err := rawFarmPlots(ctx, tx)
	if err != nil {
		return err
	}
	for _, v := range values {
		if v.Status != farmPlanted || v.NextCareAt == nil {
			continue
		}
		due, err := workDue(*v.NextCareAt, now)
		if err != nil {
			return err
		}
		if !due {
			continue
		}
		status := farmNeedsWater
		if v.MatureAt != nil {
			status = farmMature
		}
		if err := execute(ctx, tx, `UPDATE farm_plots SET status=?,updated_at=? WHERE id=?`, status, now, v.ID); err != nil {
			return err
		}
	}
	return nil
}
func refreshWorkTasks(ctx context.Context, tx *sql.Tx, now string) error {
	values, err := rawWorkTasks(ctx, tx, "")
	if err != nil {
		return err
	}
	for _, v := range values {
		if v.Status != workRunning {
			continue
		}
		at, status := v.NextCareAt, workNeedsCare
		if v.ClaimableAt != nil {
			at, status = v.ClaimableAt, workClaimable
		}
		if at == nil {
			continue
		}
		due, err := workDue(*at, now)
		if err != nil {
			return err
		}
		if due {
			if err := execute(ctx, tx, `UPDATE work_game_tasks SET status=?,updated_at=? WHERE id=?`, status, now, v.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func rawFarmPlots(ctx context.Context, tx *sql.Tx) ([]domain.FarmPlot, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,plot_index,crop_key,status,stage_index,planted_at,last_watered_at,next_care_at,mature_at,updated_at FROM farm_plots ORDER BY plot_index`)
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	values := []domain.FarmPlot{}
	for rows.Next() {
		var v domain.FarmPlot
		if rows.Scan(&v.ID, &v.PlotIndex, &v.CropKey, &v.Status, &v.StageIndex, &v.PlantedAt, &v.LastWateredAt, &v.NextCareAt, &v.MatureAt, &v.UpdatedAt) != nil {
			return nil, ErrDatabase
		}
		values = append(values, v)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return values, nil
}
func loadFarmPlot(ctx context.Context, tx *sql.Tx, id string) (domain.FarmPlot, error) {
	var v domain.FarmPlot
	err := tx.QueryRowContext(ctx, `SELECT id,plot_index,crop_key,status,stage_index,planted_at,last_watered_at,next_care_at,mature_at,updated_at FROM farm_plots WHERE id=?`, id).Scan(&v.ID, &v.PlotIndex, &v.CropKey, &v.Status, &v.StageIndex, &v.PlantedAt, &v.LastWateredAt, &v.NextCareAt, &v.MatureAt, &v.UpdatedAt)
	if err == sql.ErrNoRows {
		return v, errorsText("地块不存在。")
	}
	if err != nil {
		return v, ErrDatabase
	}
	return v, nil
}
func listFarmPlots(ctx context.Context, tx *sql.Tx, now string) ([]domain.FarmPlot, error) {
	values, err := rawFarmPlots(ctx, tx)
	if err != nil {
		return nil, err
	}
	for i := range values {
		enrichFarmPlot(&values[i], now)
	}
	return values, nil
}
func enrichFarmPlot(v *domain.FarmPlot, now string) {
	crop, ok := findFarmCrop(v.CropKey)
	if v.Status == farmEmpty || !ok {
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
func findFarmCrop(key string) (domain.FarmCropConfig, bool) {
	for _, v := range domain.FarmCropCatalog() {
		if v.CropKey == key {
			return v, true
		}
	}
	return domain.FarmCropConfig{}, false
}
func farmStatus(plots []domain.FarmPlot) string {
	for _, v := range plots {
		if v.Status == farmMature {
			return workClaimable
		}
	}
	for _, v := range plots {
		if v.Status == farmNeedsWater {
			return workNeedsCare
		}
	}
	for _, v := range plots {
		if v.Status == farmPlanted {
			return workRunning
		}
	}
	return workIdle
}
func farmSummary(plots []domain.FarmPlot) domain.WorkMapSummary {
	v := domain.WorkMapSummary{Status: farmStatus(plots)}
	for _, p := range plots {
		if p.Status != farmEmpty {
			v.ActivePlots++
		}
		if p.Status == farmNeedsWater {
			v.NeedsCarePlots++
		}
		if p.Status == farmMature {
			v.MaturePlots++
		}
	}
	return v
}
func clearFarmPlot(ctx context.Context, tx *sql.Tx, id, now string) error {
	return execute(ctx, tx, `UPDATE farm_plots SET crop_key='',status=?,stage_index=0,planted_at=NULL,last_watered_at=NULL,next_care_at=NULL,mature_at=NULL,updated_at=? WHERE id=?`, farmEmpty, now, id)
}
func insertHarvestEntry(ctx context.Context, tx *sql.Tx, v domain.FarmHarvestLedgerEntry) error {
	data, err := json.Marshal(v.Rewards)
	if err != nil {
		return ErrDatabase
	}
	return execute(ctx, tx, `INSERT INTO farm_harvest_ledger(id,plot_id,crop_key,rewards_json,lp_reward,created_at) VALUES(?,?,?,?,?,?)`, v.ID, v.PlotID, v.CropKey, string(data), v.LPReward, v.CreatedAt)
}
func loadHarvestEntry(ctx context.Context, tx *sql.Tx, id string) (domain.FarmHarvestLedgerEntry, error) {
	var v domain.FarmHarvestLedgerEntry
	var data string
	err := tx.QueryRowContext(ctx, `SELECT id,plot_id,crop_key,rewards_json,lp_reward,created_at FROM farm_harvest_ledger WHERE id=?`, id).Scan(&v.ID, &v.PlotID, &v.CropKey, &data, &v.LPReward, &v.CreatedAt)
	if err == sql.ErrNoRows {
		return v, nil
	}
	if err != nil {
		return v, ErrDatabase
	}
	_ = json.Unmarshal([]byte(data), &v.Rewards)
	if v.Rewards == nil {
		v.Rewards = []domain.FarmRewardItem{}
	}
	return v, nil
}
func listHarvests(ctx context.Context, tx *sql.Tx, limit int) ([]domain.FarmHarvestLedgerEntry, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,plot_id,crop_key,rewards_json,lp_reward,created_at FROM farm_harvest_ledger ORDER BY datetime(created_at) DESC,created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	values := []domain.FarmHarvestLedgerEntry{}
	for rows.Next() {
		var v domain.FarmHarvestLedgerEntry
		var data string
		if rows.Scan(&v.ID, &v.PlotID, &v.CropKey, &data, &v.LPReward, &v.CreatedAt) != nil {
			return nil, ErrDatabase
		}
		_ = json.Unmarshal([]byte(data), &v.Rewards)
		if v.Rewards == nil {
			v.Rewards = []domain.FarmRewardItem{}
		}
		values = append(values, v)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return values, nil
}

func rawWorkTasks(ctx context.Context, tx *sql.Tx, key string) ([]domain.WorkGameTask, error) {
	query := `SELECT id,game_key,slot_index,job_key,status,stage_index,started_at,last_cared_at,next_care_at,claimable_at,updated_at FROM work_game_tasks`
	args := []any{}
	if key != "" {
		query += ` WHERE game_key=?`
		args = append(args, key)
	}
	query += ` ORDER BY game_key,slot_index`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	values := []domain.WorkGameTask{}
	for rows.Next() {
		var v domain.WorkGameTask
		if rows.Scan(&v.ID, &v.GameKey, &v.SlotIndex, &v.JobKey, &v.Status, &v.StageIndex, &v.StartedAt, &v.LastCaredAt, &v.NextCareAt, &v.ClaimableAt, &v.UpdatedAt) != nil {
			return nil, ErrDatabase
		}
		values = append(values, v)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return values, nil
}
func loadWorkTask(ctx context.Context, tx *sql.Tx, id string) (domain.WorkGameTask, error) {
	var v domain.WorkGameTask
	err := tx.QueryRowContext(ctx, `SELECT id,game_key,slot_index,job_key,status,stage_index,started_at,last_cared_at,next_care_at,claimable_at,updated_at FROM work_game_tasks WHERE id=?`, id).Scan(&v.ID, &v.GameKey, &v.SlotIndex, &v.JobKey, &v.Status, &v.StageIndex, &v.StartedAt, &v.LastCaredAt, &v.NextCareAt, &v.ClaimableAt, &v.UpdatedAt)
	if err == sql.ErrNoRows {
		return v, errorsText("岗位不存在。")
	}
	if err != nil {
		return v, ErrDatabase
	}
	return v, nil
}
func listWorkTasks(ctx context.Context, tx *sql.Tx, key, now string) ([]domain.WorkGameTask, error) {
	values, err := rawWorkTasks(ctx, tx, key)
	if err != nil {
		return nil, err
	}
	for i := range values {
		enrichWorkTask(&values[i], now)
	}
	return values, nil
}
func enrichWorkTask(v *domain.WorkGameTask, now string) {
	game, ok := findWorkGame(v.GameKey)
	if !ok {
		return
	}
	var job *domain.WorkGameJobConfig
	for i := range game.Jobs {
		j := game.Jobs[i]
		if j.JobKey == v.JobKey || (v.JobKey == "" && j.SlotIndex == v.SlotIndex) {
			job = &j
			break
		}
	}
	v.Job = job
	if v.Status == workIdle || job == nil {
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
func workStatus(tasks []domain.WorkGameTask) string {
	for _, v := range tasks {
		if v.Status == workClaimable {
			return workClaimable
		}
	}
	for _, v := range tasks {
		if v.Status == workNeedsCare {
			return workNeedsCare
		}
	}
	for _, v := range tasks {
		if v.Status == workRunning {
			return workRunning
		}
	}
	return workIdle
}
func workSummary(tasks []domain.WorkGameTask) domain.WorkMapSummary {
	v := domain.WorkMapSummary{Status: workStatus(tasks)}
	for _, t := range tasks {
		if t.Status != workIdle {
			v.ActivePlots++
		}
		if t.Status == workNeedsCare {
			v.NeedsCarePlots++
		}
		if t.Status == workClaimable {
			v.MaturePlots++
		}
	}
	return v
}
func findWorkGame(key string) (domain.WorkGameDefinition, bool) {
	for _, g := range domain.WorkGameCatalog() {
		if g.Key == key {
			return g, true
		}
	}
	return domain.WorkGameDefinition{}, false
}
func findWorkJob(gameKey, jobKey string) (domain.WorkGameJobConfig, bool) {
	g, ok := findWorkGame(gameKey)
	if !ok {
		return domain.WorkGameJobConfig{}, false
	}
	for _, j := range g.Jobs {
		if j.JobKey == jobKey {
			return j, true
		}
	}
	return domain.WorkGameJobConfig{}, false
}
func clearWorkTask(ctx context.Context, tx *sql.Tx, id, now string) error {
	return execute(ctx, tx, `UPDATE work_game_tasks SET job_key='',status=?,stage_index=0,started_at=NULL,last_cared_at=NULL,next_care_at=NULL,claimable_at=NULL,updated_at=? WHERE id=?`, workIdle, now, id)
}
func insertWorkRewardEntry(ctx context.Context, tx *sql.Tx, v domain.WorkGameRewardLedgerEntry) error {
	data, err := json.Marshal(v.Rewards)
	if err != nil {
		return ErrDatabase
	}
	return execute(ctx, tx, `INSERT INTO work_game_reward_ledger(id,game_key,task_id,job_key,rewards_json,lp_reward,created_at) VALUES(?,?,?,?,?,?,?)`, v.ID, v.GameKey, v.TaskID, v.JobKey, string(data), v.LPReward, v.CreatedAt)
}
func loadWorkRewardEntry(ctx context.Context, tx *sql.Tx, id string) (domain.WorkGameRewardLedgerEntry, error) {
	var v domain.WorkGameRewardLedgerEntry
	var data string
	err := tx.QueryRowContext(ctx, `SELECT id,game_key,task_id,job_key,rewards_json,lp_reward,created_at FROM work_game_reward_ledger WHERE id=?`, id).Scan(&v.ID, &v.GameKey, &v.TaskID, &v.JobKey, &data, &v.LPReward, &v.CreatedAt)
	if err == sql.ErrNoRows {
		return v, nil
	}
	if err != nil {
		return v, ErrDatabase
	}
	_ = json.Unmarshal([]byte(data), &v.Rewards)
	if v.Rewards == nil {
		v.Rewards = []domain.WorkGameRewardItem{}
	}
	return v, nil
}
func listWorkRewards(ctx context.Context, tx *sql.Tx, key string, limit int) ([]domain.WorkGameRewardLedgerEntry, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,game_key,task_id,job_key,rewards_json,lp_reward,created_at FROM work_game_reward_ledger WHERE game_key=? ORDER BY datetime(created_at) DESC,created_at DESC LIMIT ?`, key, limit)
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	values := []domain.WorkGameRewardLedgerEntry{}
	for rows.Next() {
		var v domain.WorkGameRewardLedgerEntry
		var data string
		if rows.Scan(&v.ID, &v.GameKey, &v.TaskID, &v.JobKey, &data, &v.LPReward, &v.CreatedAt) != nil {
			return nil, ErrDatabase
		}
		_ = json.Unmarshal([]byte(data), &v.Rewards)
		if v.Rewards == nil {
			v.Rewards = []domain.WorkGameRewardItem{}
		}
		values = append(values, v)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return values, nil
}

func workNextStage(now time.Time, duration, care int64) (string, error) {
	if care < 0 || duration < 0 {
		return "", domain.ErrPet
	}
	seconds := max(duration/max(care+1, 1), int64(60))
	return formatWorkTime(now.Add(time.Duration(seconds) * time.Second)), nil
}
func workDue(target, now string) (bool, error) {
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
func cropRewards(c domain.FarmCropConfig, event string) []domain.FarmRewardItem {
	return workRewards(c.PrimaryRewardItemKey, c.PrimaryRewardQuantity, c.BonusRewardItemKey, c.BonusChancePercent, event)
}
func jobRewards(j domain.WorkGameJobConfig, event string) []domain.WorkGameRewardItem {
	return workRewards(j.PrimaryRewardItemKey, j.PrimaryRewardQuantity, j.BonusRewardItemKey, j.BonusChancePercent, event)
}
func workRewards(primary string, quantity int64, bonus *string, chance int64, event string) []domain.FarmRewardItem {
	values := []domain.FarmRewardItem{{ItemKey: primary, Quantity: max(quantity, int64(1)), RewardType: "primary"}}
	if bonus != nil && deterministicNumber(event+":bonus", 100) < chance {
		values = append(values, domain.FarmRewardItem{ItemKey: *bonus, Quantity: 1, RewardType: "bonus"})
	}
	return values
}
func workLP(minimum, maximum int64, event string) int64 {
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
