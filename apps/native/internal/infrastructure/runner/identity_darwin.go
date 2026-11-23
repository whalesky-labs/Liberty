package runner

import (
	"bytes"
	"encoding/binary"
	"errors"
	"golang.org/x/sys/unix"
	"syscall"
	"time"
)

func readIdentity(pid uint32) (Identity, error) {
	if pid == 0 || pid > 1<<31-1 {
		return Identity{}, ErrIdentity
	}
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.pid", int(pid))
	if errors.Is(err, unix.ESRCH) || (err == nil && len(processes) == 0) {
		return Identity{}, ErrProcessGone
	}
	if err != nil {
		return Identity{}, ErrIdentity
	}
	info := processes[0]
	if info.Proc.P_stat == 5 {
		return Identity{}, ErrProcessGone
	}
	data, err := unix.SysctlRaw("kern.procargs2", int(pid))
	if err != nil || len(data) < 4 {
		return Identity{}, ErrIdentity
	}
	argc := int(binary.LittleEndian.Uint32(data[:4]))
	data = data[4:]
	end := bytes.IndexByte(data, 0)
	if end < 0 || argc < 1 || argc > 16384 {
		return Identity{}, ErrIdentity
	}
	executable := string(data[:end])
	data = bytes.TrimLeft(data[end:], "\x00")
	args := []string{}
	for i := 0; i < argc; i++ {
		end = bytes.IndexByte(data, 0)
		if end < 0 {
			return Identity{}, ErrIdentity
		}
		args = append(args, string(data[:end]))
		data = data[end+1:]
	}
	return Identity{Version: 1, StartTime: uint64(info.Proc.P_starttime.Sec), Executable: executable, Command: args}, nil
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
	if err != nil {
		return ErrIdentity
	}
	if group != int(pid) {
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
