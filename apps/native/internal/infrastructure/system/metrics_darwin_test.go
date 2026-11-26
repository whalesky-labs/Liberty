package system

import (
	"golang.org/x/sys/unix"
	"math"
	"os"
	"testing"
	"time"
	"unsafe"
)

func TestDarwinProcessCounterUnitsMatchGetrusage(t *testing.T) {
	if unsafe.Sizeof(darwinUsage{}) != 96 {
		t.Fatal("libproc ABI size mismatch")
	}
	api, err := loadProcessUsage()
	if err != nil {
		t.Fatal(err)
	}
	var before, after darwinUsage
	var beforeRU, afterRU unix.Rusage
	if api.read(int32(os.Getpid()), 0, &before) != 0 || unix.Getrusage(unix.RUSAGE_SELF, &beforeRU) != nil {
		t.Fatal("initial process counters unavailable")
	}
	until := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(until) {
	}
	if api.read(int32(os.Getpid()), 0, &after) != 0 || unix.Getrusage(unix.RUSAGE_SELF, &afterRU) != nil {
		t.Fatal("final process counters unavailable")
	}
	measured := api.nanoseconds(after.userTime + after.systemTime - before.userTime - before.systemTime)
	timevalNS := func(v unix.Timeval) int64 { return v.Sec*1e9 + int64(v.Usec)*1000 }
	reference := timevalNS(afterRU.Utime) + timevalNS(afterRU.Stime) - timevalNS(beforeRU.Utime) - timevalNS(beforeRU.Stime)
	if measured < 1e6 || reference < 1e6 || math.Abs(float64(measured)-float64(reference)) > max(5e6, float64(reference)*.1) {
		t.Fatalf("CPU unit conversion mismatch: libproc=%d ns getrusage=%d ns timebase=%d/%d", measured, reference, api.numer, api.denom)
	}
	t.Logf("libproc CPU %.2f ms; getrusage %.2f ms; timebase %d/%d", float64(measured)/1e6, float64(reference)/1e6, api.numer, api.denom)
}
