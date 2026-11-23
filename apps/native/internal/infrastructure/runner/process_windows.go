package runner

import (
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

type processControl struct {
	sharedStop
	job     windows.Handle
	pid     uint32
	stopped bool
}

func prepareProcess(cmd *exec.Cmd) (*processControl, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, ErrStart
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, ErrStart
	}
	// Suspend before user code can spawn children; attach the job before resume.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW, HideWindow: true}
	return &processControl{job: job}, nil
}
func (c *processControl) started(p *os.Process) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pid = uint32(p.Pid)
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, c.pid)
	if err != nil {
		return ErrStart
	}
	defer windows.CloseHandle(process)
	if windows.AssignProcessToJobObject(c.job, process) != nil {
		return ErrStart
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return ErrStart
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != c.pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return ErrStart
		}
		_, err = windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		if err != nil {
			return ErrStart
		}
		return nil
	}
	return ErrStart
}
func (c *processControl) stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped || c.job == 0 {
		return nil
	}
	if windows.TerminateJobObject(c.job, 1) != nil {
		return ErrProcess
	}
	c.stopped = true
	return nil
}
func (c *processControl) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.job != 0 {
		windows.CloseHandle(c.job)
		c.job = 0
	}
}
