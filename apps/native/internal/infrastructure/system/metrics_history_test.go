package system

import (
	"liberty.local/liberty/internal/domain"
	"math"
	"testing"
	"time"
)

func TestMetricsUseIntervalCountersAndProcessLifetimes(t *testing.T) {
	base := time.Unix(1000, 0)
	started := base.Add(-time.Minute)
	usage := func(start time.Time, seconds float64, memory uint64) processUsage {
		return processUsage{start, uint64(seconds * 1e9), memory}
	}
	h := metricsHistory{}
	value, err := h.sample(1, base, map[int]processUsage{1: usage(started, 5, 100)}, domain.ProcessMemoryFootprint)
	if err != nil || value.CPUPercent != nil || value.MemoryBytes != 100 {
		t.Fatal("first sample invented CPU", value, err)
	}
	value, err = h.sample(1, base.Add(time.Second), map[int]processUsage{1: usage(started, 5.25, 110), 2: usage(base.Add(100*time.Millisecond), .5, 200)}, domain.ProcessMemoryFootprint)
	if err != nil || value.CPUPercent == nil || math.Abs(*value.CPUPercent-75) > .001 || value.MemoryBytes != 310 {
		t.Fatal("new descendant not included", value, err)
	}
	value, _ = h.sample(1, base.Add(2*time.Second), map[int]processUsage{1: usage(started, 7.25, 110)}, domain.ProcessMemoryFootprint)
	if value.CPUPercent == nil || *value.CPUPercent != 200 || value.MemoryBytes != 110 {
		t.Fatal("multicore CPU or exited process counted incorrectly", value)
	}
	// PID reuse is a new lifetime, never a subtraction against the old counter.
	value, _ = h.sample(1, base.Add(3*time.Second), map[int]processUsage{1: usage(base.Add(2500*time.Millisecond), .5, 120)}, domain.ProcessMemoryFootprint)
	if value.CPUPercent == nil || *value.CPUPercent != 50 {
		t.Fatal("reused PID corrupted CPU", value)
	}
	value, _ = h.sample(1, base.Add(10*time.Second), map[int]processUsage{1: usage(base.Add(2500*time.Millisecond), 1, 120)}, domain.ProcessMemoryFootprint)
	if value.CPUPercent != nil {
		t.Fatal("resume displayed a stale interval average")
	}
	value, _ = h.sample(1, base.Add(11*time.Second), map[int]processUsage{1: usage(base.Add(2500*time.Millisecond), .5, 120)}, domain.ProcessMemoryFootprint)
	if value.CPUPercent != nil {
		t.Fatal("decreasing counter became a real CPU sample")
	}
	if _, err = h.sample(1, base.Add(12*time.Second), map[int]processUsage{}, domain.ProcessMemoryFootprint); err == nil {
		t.Fatal("missing root accepted")
	}
	value, _ = h.sample(1, base.Add(13*time.Second), map[int]processUsage{1: usage(started, 8, 90)}, domain.ProcessMemoryFootprint)
	if value.CPUPercent != nil {
		t.Fatal("failed sample retained old baseline")
	}
}
