package system

import "golang.org/x/sys/unix"

func MemoryBytes() uint64 {
	value, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return value
}
