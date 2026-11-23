package system

import (
	"bytes"
	"context"
	"liberty.local/liberty/internal/domain"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type MetricsSampler struct{}

func (*MetricsSampler) Sample(ctx context.Context) (domain.ProcessMetrics, error) {
	command := exec.CommandContext(ctx, "/bin/ps", "-axo", "pid=,ppid=,%cpu=,rss=")
	command.Env = []string{"LC_ALL=C"}
	var output bytes.Buffer
	command.Stdout = &output
	if err := command.Run(); err != nil {
		return domain.ProcessMetrics{}, errProcessMetrics
	}
	parents := map[int]int{}
	metrics := map[int]domain.ProcessMetrics{}
	for _, line := range strings.Split(output.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 4 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		cpu, cpuErr := strconv.ParseFloat(fields[2], 64)
		rss, rssErr := strconv.ParseUint(fields[3], 10, 64)
		if pidErr != nil || parentErr != nil || cpuErr != nil || rssErr != nil || pid == command.Process.Pid {
			continue
		}
		parents[pid] = parent
		metrics[pid] = domain.ProcessMetrics{CPUPercent: &cpu, ResidentBytes: rss * 1024}
	}
	members := processTree(os.Getpid(), parents)
	if len(members) == 0 {
		return domain.ProcessMetrics{}, errProcessMetrics
	}
	cpu := 0.0
	result := domain.ProcessMetrics{CPUPercent: &cpu}
	for _, pid := range members {
		cpu += *metrics[pid].CPUPercent
		result.ResidentBytes += metrics[pid].ResidentBytes
	}
	return result, nil
}
