package system

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
	"liberty.local/liberty/internal/domain"
)

type MetricsSampler struct{ metricsHistory }

// rusage_info_v0 is the public libproc ABI from sys/resource.h (96 bytes on
// both supported macOS architectures). CPU counters use Mach time units; footprint
// is the kernel's current physical-memory charge, including compressed memory.
type darwinUsage struct {
	uuid                                                         [16]byte
	userTime, systemTime, idleWakeups, interruptWakeups, pageins uint64
	wiredSize, residentSize, footprint, startTime, exitTime      uint64
}

type darwinUsageAPI struct {
	read         func(int32, int32, *darwinUsage) int32
	numer, denom uint32
}

func (api darwinUsageAPI) nanoseconds(ticks uint64) uint64 {
	return ticks/uint64(api.denom)*uint64(api.numer) + ticks%uint64(api.denom)*uint64(api.numer)/uint64(api.denom)
}

var loadProcessUsage = sync.OnceValues(func() (darwinUsageAPI, error) {
	lib, err := purego.Dlopen("/usr/lib/libproc.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return darwinUsageAPI{}, errProcessMetrics
	}
	symbol, err := purego.Dlsym(lib, "proc_pid_rusage")
	if err != nil {
		_ = purego.Dlclose(lib)
		return darwinUsageAPI{}, errProcessMetrics
	}
	api := darwinUsageAPI{}
	purego.RegisterFunc(&api.read, symbol)
	timeSymbol, err := purego.Dlsym(purego.RTLD_DEFAULT, "mach_timebase_info")
	if err != nil {
		return darwinUsageAPI{}, errProcessMetrics
	}
	var timebase func(*struct{ numer, denom uint32 }) int32
	purego.RegisterFunc(&timebase, timeSymbol)
	var base struct{ numer, denom uint32 }
	if timebase(&base) != 0 || base.numer == 0 || base.denom == 0 {
		return darwinUsageAPI{}, errProcessMetrics
	}
	api.numer, api.denom = base.numer, base.denom
	// The library remains loaded for the lifetime of the registered function.
	return api, nil
})

func (sampler *MetricsSampler) Sample(ctx context.Context) (domain.ProcessMetrics, error) {
	current, err := readProcessUsage(ctx)
	if err != nil {
		sampler.reset()
		return domain.ProcessMetrics{}, err
	}
	return sampler.sample(os.Getpid(), time.Now(), current, domain.ProcessMemoryFootprint)
}

func readProcessUsage(ctx context.Context) (map[int]processUsage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	api, err := loadProcessUsage()
	if err != nil {
		return nil, err
	}
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, errProcessMetrics
	}
	parents := make(map[int]int, len(processes))
	started := make(map[int]time.Time, len(processes))
	for _, process := range processes {
		if process.Proc.P_stat == 5 {
			continue
		}
		pid := int(process.Proc.P_pid)
		parents[pid] = int(process.Eproc.Ppid)
		started[pid] = time.Unix(process.Proc.P_starttime.Sec, int64(process.Proc.P_starttime.Usec)*1000)
	}
	current := make(map[int]processUsage)
	for _, pid := range processTree(os.Getpid(), parents) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var value darwinUsage
		if api.read(int32(pid), 0, &value) != 0 {
			// Children may exit between enumeration and sampling.
			if pid != os.Getpid() && unix.Kill(pid, 0) == unix.ESRCH {
				continue
			}
			return nil, errProcessMetrics
		}
		current[pid] = processUsage{started: started[pid], cpuNS: api.nanoseconds(value.userTime + value.systemTime), memoryBytes: value.footprint}
	}
	return current, ctx.Err()
}
