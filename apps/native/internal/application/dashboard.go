package application

import (
	"context"
	"time"

	"liberty.local/liberty/internal/domain"
)

type DashboardReader interface {
	Dashboard(context.Context, string, time.Time) (domain.DashboardOverview, error)
}

type Dashboard struct {
	reader DashboardReader
	now    func() time.Time
}

func NewDashboard(reader DashboardReader, now func() time.Time) *Dashboard {
	return &Dashboard{reader: reader, now: now}
}

func (d *Dashboard) Overview(ctx context.Context, period string) (domain.DashboardOverview, error) {
	return d.reader.Dashboard(ctx, period, d.now())
}
