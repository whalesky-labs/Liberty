//go:build !darwin && !windows

package system

func MemoryBytes() uint64 { return 0 }
