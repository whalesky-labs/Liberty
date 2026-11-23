package system

import (
	"golang.org/x/sys/windows"
	"unsafe"
)

func MemoryBytes() uint64 {
	var state struct {
		Length, Load                                                                                         uint32
		Physical, AvailablePhysical, PageFile, AvailablePageFile, Virtual, AvailableVirtual, ExtendedVirtual uint64
	}
	state.Length = uint32(unsafe.Sizeof(state))
	result, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx").Call(uintptr(unsafe.Pointer(&state)))
	if result == 0 {
		return 0
	}
	return state.Physical
}
