package system

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestMetricsChild(t *testing.T) {
	if os.Getenv("LIBERTY_METRICS_TEST_CHILD") != "1" {
		return
	}
	memory := make([]byte, 32<<20)
	for i := range memory {
		memory[i] = byte(i)
	}
	fmt.Println("ready")
	reader := bufio.NewReader(os.Stdin)
	if _, err := reader.ReadString('\n'); err != nil {
		os.Exit(2)
	}
	fmt.Println("busy")
	until := time.Now().Add(700 * time.Millisecond)
	var count uint64
	for time.Now().Before(until) {
		count++
	}
	fmt.Println("done")
	_, _ = reader.ReadString('\n')
	runtime.KeepAlive(memory)
	runtime.KeepAlive(count)
	os.Exit(0)
}

func TestLiveMetricsIncludeChildCPUAndMemory(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("supported desktop platforms only")
	}
	sampler := &MetricsSampler{}
	baseline, err := sampler.Sample(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMetricsChild$")
	cmd.Env = append(os.Environ(), "LIBERTY_METRICS_TEST_CHILD=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	reader := bufio.NewReader(stdout)
	if line, err := reader.ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatal("child did not allocate test memory", line, err)
	}
	before, err := sampler.Sample(context.Background())
	if err != nil || before.MemoryBytes < baseline.MemoryBytes+16<<20 {
		t.Fatal("child memory was not included", before, baseline, err)
	}
	if _, err = io.WriteString(stdin, "run\n"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"busy\n", "done\n"} {
		if line, err := reader.ReadString('\n'); err != nil || line != want {
			t.Fatal("child workload failed", line, err)
		}
	}
	after, err := sampler.Sample(context.Background())
	if err != nil || after.CPUPercent == nil || *after.CPUPercent < 1 {
		t.Fatal("child CPU was not included", after, err)
	}
	t.Logf("child workload: CPU %.1f%%, memory delta %.1f MiB", *after.CPUPercent, float64(after.MemoryBytes-baseline.MemoryBytes)/(1<<20))
	if _, err = io.WriteString(stdin, "exit\n"); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	final, err := sampler.Sample(context.Background())
	if err != nil || final.MemoryBytes+16<<20 > after.MemoryBytes {
		t.Fatal("exited child memory remained in metrics", final, after, err)
	}
}
