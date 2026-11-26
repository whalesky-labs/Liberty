package system

import (
	"context"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"liberty.local/liberty/internal/domain"
)

var workingSetInfo = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

type MetricsSampler struct{ metricsHistory }

func fileTicks(value windows.Filetime) uint64 {
	return uint64(value.HighDateTime)<<32 | uint64(value.LowDateTime)
}

func readMetrics(pid int) (processUsage, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		return processUsage{}, err
	}
	defer windows.CloseHandle(handle)
	var created, exit, kernel, user windows.Filetime
	var counters struct {
		Size, Faults               uint32
		PeakWorkingSet, WorkingSet uintptr
		Quotas                     [6]uintptr
	}
	counters.Size = uint32(unsafe.Sizeof(counters))
	if err := windows.GetProcessTimes(handle, &created, &exit, &kernel, &user); err != nil {
		return processUsage{}, err
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return processUsage{}, err
	}
	const stillActive = 259
	if exitCode != stillActive {
		return processUsage{}, windows.ERROR_INVALID_PARAMETER
	}
	ok, _, err := workingSetInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&counters)), uintptr(counters.Size))
	if ok == 0 {
		return processUsage{}, err
	}
	return processUsage{started: time.Unix(0, created.Nanoseconds()), cpuNS: (fileTicks(kernel) + fileTicks(user)) * 100, memoryBytes: uint64(counters.WorkingSet)}, nil
}

func (sampler *MetricsSampler) Sample(ctx context.Context) (domain.ProcessMetrics, error) {
	current, err := readProcessUsage(ctx)
	if err != nil {
		sampler.reset()
		return domain.ProcessMetrics{}, err
	}
	return sampler.sample(os.Getpid(), time.Now(), current, domain.ProcessMemoryWorkingSet)
}

func readProcessUsage(ctx context.Context) (map[int]processUsage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, errProcessMetrics
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{}
	entry.Size = uint32(unsafe.Sizeof(entry))
	parents := map[int]int{}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		parents[int(entry.ProcessID)] = int(entry.ParentProcessID)
	}
	if err != windows.ERROR_NO_MORE_FILES {
		return nil, errProcessMetrics
	}
	current := map[int]processUsage{}
	for _, pid := range processTree(os.Getpid(), parents) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		value, err := readMetrics(pid)
		if err != nil {
			if pid != os.Getpid() && err == windows.ERROR_INVALID_PARAMETER {
				continue
			}
			return nil, errProcessMetrics
		}
		current[pid] = value
	}
	return current, ctx.Err()
}
