package application

import (
	"context"
	"liberty.local/liberty/internal/domain"
	"time"
)

type ModeDashboard struct {
	local    DashboardReader
	meetings *ModeMeetings
}

func NewModeDashboard(local DashboardReader, meetings *ModeMeetings) *ModeDashboard {
	return &ModeDashboard{local, meetings}
}
func (d *ModeDashboard) Dashboard(ctx context.Context, period string, now time.Time) (domain.DashboardOverview, error) {
	mode, err := d.meetings.ProcessingMode(ctx)
	if err != nil {
		return domain.DashboardOverview{}, err
	}
	if mode == "local" {
		result, err := d.local.Dashboard(ctx, period, now)
		result.Source = "local"
		return result, err
	}
	results, err := d.meetings.remote.ListResults(ctx)
	if err != nil {
		return domain.DashboardOverview{}, err
	}
	return domain.RemoteDashboard(results, period, now)
}
