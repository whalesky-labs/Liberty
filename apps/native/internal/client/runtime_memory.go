package client

import (
	"runtime/metrics"
	"strings"
	"time"
)

// RuntimeMemory counts mutually exclusive Go memory classes. Released heap
// pages and allocations outside the Go runtime are outside this breakdown.
type RuntimeMemory struct {
	Objects, Free, Unused, Stacks, Metadata, Other uint64
}

type RuntimeMemorySampler struct {
	samples []metrics.Sample
	at      time.Time
	value   RuntimeMemory
}

func (s *RuntimeMemorySampler) Read(now time.Time) RuntimeMemory {
	if now.Sub(s.at) < ProcessMetricsInterval {
		return s.value
	}
	if s.samples == nil {
		for _, desc := range metrics.All() {
			if strings.HasPrefix(desc.Name, "/memory/classes/") && desc.Name != "/memory/classes/total:bytes" && desc.Name != "/memory/classes/heap/released:bytes" {
				s.samples = append(s.samples, metrics.Sample{Name: desc.Name})
			}
		}
	}
	metrics.Read(s.samples)
	var m RuntimeMemory
	for _, sample := range s.samples {
		bytes := sample.Value.Uint64()
		switch sample.Name {
		case "/memory/classes/heap/objects:bytes":
			m.Objects = bytes
		case "/memory/classes/heap/free:bytes":
			m.Free = bytes
		case "/memory/classes/heap/unused:bytes":
			m.Unused = bytes
		case "/memory/classes/heap/stacks:bytes", "/memory/classes/os-stacks:bytes":
			m.Stacks += bytes
		default:
			if strings.HasPrefix(sample.Name, "/memory/classes/metadata/") {
				m.Metadata += bytes
			} else {
				m.Other += bytes
			}
		}
	}
	s.value, s.at = m, now
	return m
}
