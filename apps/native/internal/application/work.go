package application

import (
	"context"
	"time"

	"liberty.local/liberty/internal/domain"
)

type WorkRepository interface {
	FarmState(context.Context, time.Time) (domain.FarmState, error)
	HarvestLedger(context.Context, int) ([]domain.FarmHarvestLedgerEntry, error)
	WorkMarketState(context.Context, time.Time) (domain.WorkMarketState, error)
	PlantCrop(context.Context, string, string, time.Time) (domain.FarmState, error)
	WaterPlot(context.Context, string, time.Time) (domain.FarmState, error)
	HarvestPlot(context.Context, string, time.Time) (domain.FarmHarvestResult, error)
	WorkGameState(context.Context, string, time.Time) (domain.WorkGameState, error)
	StartWorkTask(context.Context, string, string, string, time.Time) (domain.WorkGameState, error)
	CareWorkTask(context.Context, string, string, time.Time) (domain.WorkGameState, error)
	ClaimWorkTask(context.Context, string, string, time.Time) (domain.WorkGameClaimResult, error)
}

type Work struct {
	repository WorkRepository
	now        func() time.Time
}

func NewWork(repository WorkRepository, now func() time.Time) *Work {
	if now == nil {
		now = time.Now
	}
	return &Work{repository: repository, now: now}
}
func (w *Work) Farm(ctx context.Context) (domain.FarmState, error) {
	return w.repository.FarmState(ctx, w.now())
}

func (w *Work) HarvestLedger(ctx context.Context, limit int) ([]domain.FarmHarvestLedgerEntry, error) {
	return w.repository.HarvestLedger(ctx, limit)
}
func (w *Work) Market(ctx context.Context) (domain.WorkMarketState, error) {
	return w.repository.WorkMarketState(ctx, w.now())
}
func (w *Work) Plant(ctx context.Context, plotID, cropKey string) (domain.FarmState, error) {
	return w.repository.PlantCrop(ctx, plotID, cropKey, w.now())
}
func (w *Work) Water(ctx context.Context, plotID string) (domain.FarmState, error) {
	return w.repository.WaterPlot(ctx, plotID, w.now())
}
func (w *Work) Harvest(ctx context.Context, plotID string) (domain.FarmHarvestResult, error) {
	return w.repository.HarvestPlot(ctx, plotID, w.now())
}
func (w *Work) Game(ctx context.Context, key string) (domain.WorkGameState, error) {
	return w.repository.WorkGameState(ctx, key, w.now())
}
func (w *Work) Start(ctx context.Context, key, taskID, jobKey string) (domain.WorkGameState, error) {
	return w.repository.StartWorkTask(ctx, key, taskID, jobKey, w.now())
}
func (w *Work) Care(ctx context.Context, key, taskID string) (domain.WorkGameState, error) {
	return w.repository.CareWorkTask(ctx, key, taskID, w.now())
}
func (w *Work) Claim(ctx context.Context, key, taskID string) (domain.WorkGameClaimResult, error) {
	return w.repository.ClaimWorkTask(ctx, key, taskID, w.now())
}
