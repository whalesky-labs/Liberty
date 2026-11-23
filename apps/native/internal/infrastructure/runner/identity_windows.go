package runner

import (
	"errors"
	"golang.org/x/sys/windows"
	"unsafe"
)

func readIdentity(pid uint32) (Identity, error) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return Identity{}, ErrProcessGone
	}
	if err != nil {
		return Identity{}, ErrIdentity
	}
	defer windows.CloseHandle(process)
	var code uint32
	if windows.GetExitCodeProcess(process, &code) != nil {
		return Identity{}, ErrIdentity
	}
	if code != 259 {
		return Identity{}, ErrProcessGone
	}
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(process, &created, &exited, &kernel, &user) != nil {
		return Identity{}, ErrIdentity
	}
	var name [32768]uint16
	size := uint32(len(name))
	if windows.QueryFullProcessImageName(process, 0, &name[0], &size) != nil {
		return Identity{}, ErrIdentity
	}
	// ProcessCommandLineInformation returns a UNICODE_STRING and its contents in
	// the supplied buffer. Validate the pointer and length before accessing it.
	var required uint32
	_ = windows.NtQueryInformationProcess(process, 60, nil, 0, &required)
	if required < uint32(unsafe.Sizeof(windows.NTUnicodeString{})) || required > 131072 {
		return Identity{}, ErrIdentity
	}
	data := make([]byte, required)
	if windows.NtQueryInformationProcess(process, 60, unsafe.Pointer(&data[0]), required, &required) != nil {
		return Identity{}, ErrIdentity
	}
	text := (*windows.NTUnicodeString)(unsafe.Pointer(&data[0]))
	start, base := uintptr(unsafe.Pointer(text.Buffer)), uintptr(unsafe.Pointer(&data[0]))
	if text.Length%2 != 0 || start < base || start-base > uintptr(len(data)) || uintptr(text.Length) > uintptr(len(data))-(start-base) {
		return Identity{}, ErrIdentity
	}
	command := windows.UTF16ToString(unsafe.Slice(text.Buffer, int(text.Length)/2))
	args, err := windows.DecomposeCommandLine(command)
	if err != nil || len(args) == 0 {
		return Identity{}, ErrIdentity
	}
	return Identity{Version: 1, StartTime: uint64(created.Nanoseconds() / 1e9), Executable: windows.UTF16ToString(name[:size]), Command: args}, nil
}
func terminateVerified(pid uint32, identity Identity) error {
	// Keep an OS reference alive throughout taskkill so the PID cannot be
	// recycled between identity verification and descendant termination.
	process, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if errors.Is(openErr, windows.ERROR_INVALID_PARAMETER) {
		return nil
	}
	if openErr != nil {
		return ErrIdentity
	}
	defer windows.CloseHandle(process)
	// New Go children belong to kill-on-close Job Objects. A persisted legacy
	// process needs taskkill's descendant traversal, with identity checked first.
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
	return terminateWindowsTree(pid)
}
