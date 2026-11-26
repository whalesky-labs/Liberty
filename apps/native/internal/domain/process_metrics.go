package domain

import "time"

type ProcessMemoryKind string

const (
	ProcessMemoryFootprint  ProcessMemoryKind = "footprint"
	ProcessMemoryWorkingSet ProcessMemoryKind = "working_set"
)

type ProcessMetrics struct {
	CPUPercent  *float64
	MemoryBytes uint64
	MemoryKind  ProcessMemoryKind
	SampledAt   time.Time
}
