package runner

import (
	"bytes"
	"errors"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Linux supports CI execution only. /proc exports USER_HZ=100 on these test
// architectures; this does not add a Linux product/runtime distribution.
func readIdentity(pid uint32) (Identity, error) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return Identity{}, ErrIdentity
	}
	base := "/proc/" + strconv.FormatUint(uint64(pid), 10)
	stat, err := os.ReadFile(base + "/stat")
	if errors.Is(err, os.ErrNotExist) {
		return Identity{}, ErrProcessGone
	}
	if err != nil {
		return Identity{}, ErrIdentity
	}
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 {
		return Identity{}, ErrIdentity
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) < 20 {
		return Identity{}, ErrIdentity
	}
	if fields[0] == "Z" {
		return Identity{}, ErrProcessGone
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return Identity{}, ErrIdentity
	}
	system, err := os.ReadFile("/proc/stat")
	if err != nil {
		return Identity{}, ErrIdentity
	}
	var boot uint64
	for _, line := range strings.Split(string(system), "\n") {
		if strings.HasPrefix(line, "btime ") {
			boot, err = strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "btime ")), 10, 64)
		}
	}
	if err != nil || boot == 0 {
		return Identity{}, ErrIdentity
	}
	executable, err := os.Readlink(base + "/exe")
	if err != nil {
		return Identity{}, ErrIdentity
	}
	command, err := os.ReadFile(base + "/cmdline")
	if err != nil || len(command) == 0 {
		return Identity{}, ErrIdentity
	}
	args := strings.Split(strings.TrimSuffix(string(command), "\x00"), "\x00")
	return Identity{Version: 1, StartTime: boot + ticks/100, Executable: executable, Command: args}, nil
}
func terminateVerified(pid uint32, identity Identity) error {
	current, err := readIdentity(pid)
	if errors.Is(err, ErrProcessGone) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.StartTime != identity.StartTime || current.Executable != identity.Executable {
		return nil
	}
	group, err := syscall.Getpgid(int(pid))
	if err != nil || group != int(pid) {
		return ErrIdentity
	}
	if err := syscall.Kill(-int(pid), syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return ErrProcess
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		next, err := readIdentity(pid)
		if errors.Is(err, ErrProcessGone) || (err == nil && next.StartTime != identity.StartTime) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return ErrProcess
}
