package system

import (
	"errors"
	"time"

	"liberty.local/liberty/internal/domain"
)

var errProcessMetrics = errors.New("process_metrics_unavailable")

// Each counter belongs to one process lifetime; a reused PID must not inherit
// another process's CPU time. OS adapters convert cumulative CPU time to ns.
type processUsage struct {
	started            time.Time
	cpuNS, memoryBytes uint64
}

type metricsHistory struct {
	previous  map[int]processUsage
	sampledAt time.Time
}

func (h *metricsHistory) reset() { *h = metricsHistory{} }

func (h *metricsHistory) sample(root int, now time.Time, current map[int]processUsage, kind domain.ProcessMemoryKind) (domain.ProcessMetrics, error) {
	if _, exists := current[root]; !exists {
		h.reset()
		return domain.ProcessMetrics{}, errProcessMetrics
	}
	result := domain.ProcessMetrics{MemoryKind: kind, SampledAt: now}
	elapsed := now.Sub(h.sampledAt)
	// A resumed or failed sampler first establishes a fresh baseline instead of
	// presenting a long-term average as the current one-second CPU usage.
	known := !h.sampledAt.IsZero() && elapsed > 0 && elapsed <= 3*time.Second
	var cpuNS uint64
	for pid, usage := range current {
		result.MemoryBytes += usage.memoryBytes
		previous, found := h.previous[pid]
		switch {
		case found && usage.started.Equal(previous.started) && usage.cpuNS >= previous.cpuNS:
			cpuNS += usage.cpuNS - previous.cpuNS
		case !usage.started.Before(h.sampledAt) && !usage.started.After(now):
			// Include a child started since the last sample on its first sighting.
			cpuNS += usage.cpuNS
		default:
			known = false
		}
	}
	if known {
		percent := float64(cpuNS) / float64(elapsed) * 100
		result.CPUPercent = &percent
	}
	h.previous, h.sampledAt = current, now
	return result, nil
}

func processTree(root int, parents map[int]int) []int {
	if _, exists := parents[root]; !exists {
		return nil
	}
	children := make(map[int][]int)
	for pid, parent := range parents {
		children[parent] = append(children[parent], pid)
	}
	result := []int{root}
	seen := map[int]bool{root: true}
	for index := 0; index < len(result); index++ {
		for _, child := range children[result[index]] {
			if !seen[child] {
				seen[child] = true
				result = append(result, child)
			}
		}
	}
	return result
}
