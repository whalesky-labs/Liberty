//go:build !darwin && !windows

package system

import (
	"context"
	"liberty.local/liberty/internal/domain"
)

type MetricsSampler struct{}

func (*MetricsSampler) Sample(context.Context) (domain.ProcessMetrics, error) {
	return domain.ProcessMetrics{}, errProcessMetrics
}
