package system

import (
	"context"
	"runtime"
	"slices"
	"testing"
)

func TestProcessTreeIncludesOnlyRootAndDescendants(t *testing.T) {
	parents := map[int]int{1: 0, 2: 1, 3: 2, 4: 1, 5: 9, 9: 5}
	members := processTree(2, parents)
	slices.Sort(members)
	if !slices.Equal(members, []int{2, 3}) || len(processTree(8, parents)) != 0 {
		t.Fatal(members)
	}
}

func TestProcessMetricsReadCurrentProcess(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("platform sampler unavailable")
	}
	sampler := &MetricsSampler{}
	for index := 0; index < 2; index++ {
		value, err := sampler.Sample(context.Background())
		if err != nil || value.ResidentBytes == 0 || index == 1 && (value.CPUPercent == nil || *value.CPUPercent < 0) {
			t.Fatal("invalid live process sample", err)
		}
	}
}
