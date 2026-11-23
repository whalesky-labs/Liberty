package system

import (
	"context"
	"golang.org/x/sys/windows"
	"liberty.local/liberty/internal/domain"
	"os"
	"time"
	"unsafe"
)

var workingSetInfo = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

type processTimes struct{ created, ticks uint64 }
type MetricsSampler struct {
	previous  map[int]processTimes
	sampledAt time.Time
}

func fileTicks(value windows.Filetime) uint64 {
	return uint64(value.HighDateTime)<<32 | uint64(value.LowDateTime)
}

func readMetrics(pid int) (processTimes, uint64, bool) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		return processTimes{}, 0, false
	}
	defer windows.CloseHandle(handle)
	var created, exit, kernel, user windows.Filetime
	var counters struct {
		Size, Faults               uint32
		PeakWorkingSet, WorkingSet uintptr
		Quotas                     [6]uintptr
	}
	counters.Size = uint32(unsafe.Sizeof(counters))
	if windows.GetProcessTimes(handle, &created, &exit, &kernel, &user) != nil {
		return processTimes{}, 0, false
	}
	ok, _, _ := workingSetInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&counters)), uintptr(counters.Size))
	return processTimes{created: fileTicks(created), ticks: fileTicks(kernel) + fileTicks(user)}, uint64(counters.WorkingSet), ok != 0
}

func (sampler *MetricsSampler) Sample(ctx context.Context) (domain.ProcessMetrics, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return domain.ProcessMetrics{}, errProcessMetrics
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{}
	entry.Size = uint32(unsafe.Sizeof(entry))
	parents := map[int]int{}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		parents[int(entry.ProcessID)] = int(entry.ParentProcessID)
	}
	if err != windows.ERROR_NO_MORE_FILES {
		return domain.ProcessMetrics{}, errProcessMetrics
	}
	current := map[int]processTimes{}
	now := time.Now()
	var ticks uint64
	var result domain.ProcessMetrics
	for _, pid := range processTree(os.Getpid(), parents) {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		value, rss, ok := readMetrics(pid)
		if !ok {
			continue
		}
		current[pid] = value
		result.ResidentBytes += rss
		if prev, found := sampler.previous[pid]; found && value.created == prev.created && value.ticks >= prev.ticks {
			ticks += value.ticks - prev.ticks
		}
	}
	if _, ok := current[os.Getpid()]; !ok {
		return result, errProcessMetrics
	}
	if !sampler.sampledAt.IsZero() {
		percent := float64(ticks) / 100000 / now.Sub(sampler.sampledAt).Seconds()
		result.CPUPercent = &percent
	}
	sampler.sampledAt, sampler.previous = now, current
	return result, nil
}
