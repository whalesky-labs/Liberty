package domain

type ASRResourceBudget struct {
	MaxConcurrency   uint32
	ThreadsPerRunner uint32
}

// ResolveASRResourceBudget matches domain/asr_resources.rs: reserve 2 GiB,
// estimate 4 GiB per runner, and divide CPUs across admitted workers.
func ResolveASRResourceBudget(concurrency, threads, cpus uint32, memoryBytes uint64) ASRResourceBudget {
	const gib = uint64(1024 * 1024 * 1024)
	cpus = max(1, cpus)
	available := uint64(0)
	if memoryBytes > 2*gib {
		available = memoryBytes - 2*gib
	}
	workers := uint32(min(uint64(max(1, min(concurrency, 8))), max(1, available/(4*gib)), uint64(cpus)))
	perRunner := cpus / workers
	if threads != 0 {
		perRunner = min(threads, perRunner)
	}
	return ASRResourceBudget{workers, max(1, min(perRunner, 32))}
}
