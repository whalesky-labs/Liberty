//go:build darwin || linux

package runner

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type processControl struct {
	sharedStop
	pid     int
	stopped bool
}

func prepareProcess(cmd *exec.Cmd) (*processControl, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &processControl{}, nil
}
func (c *processControl) started(p *os.Process) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pid = p.Pid
	return nil
}
func (c *processControl) stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pid == 0 || c.stopped {
		return nil
	}
	err := syscall.Kill(-c.pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		c.stopped = true
		return nil
	}
	if err != nil {
		return ErrProcess
	}
	c.stopped = true
	return nil
}
func (c *processControl) close() { _ = c.stop() }
