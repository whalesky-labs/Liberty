package runner

import (
	"context"
	"golang.org/x/sys/windows"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func terminateWindowsTree(pid uint32) error {
	directory, err := windows.GetSystemDirectory()
	if err != nil {
		return ErrProcess
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(directory, "taskkill.exe"), "/PID", strconv.FormatUint(uint64(pid), 10), "/T", "/F")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if cmd.Run() != nil {
		return ErrProcess
	}
	return nil
}
