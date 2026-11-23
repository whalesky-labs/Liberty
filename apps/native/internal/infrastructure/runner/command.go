package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/diagnostics"
)

// RunCommand executes installer/validator commands with the same process-tree
// ownership as ASR. Both output streams are bounded diagnostics, not JSON Lines.
func RunCommand(ctx context.Context, executable string, args []string, directory string, environment map[string]string, log func(string) error) error {
	if !filepath.IsAbs(executable) || !filepath.IsAbs(directory) {
		return ErrStart
	}
	info, err := os.Stat(executable)
	if err != nil || !info.Mode().IsRegular() {
		return ErrStart
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = directory
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, ok := environment[key]; !ok {
			command.Env = append(command.Env, entry)
		}
	}
	for key, value := range environment {
		command.Env = append(command.Env, key+"="+value)
	}
	control, err := prepareProcess(command)
	if err != nil {
		return err
	}
	defer control.close()
	command.Cancel = control.stop
	command.WaitDelay = time.Second
	lines := make(chan streamLine, 32)
	emit := func(line streamLine) error {
		select {
		case lines <- line:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	out, stderr := &lineWriter{emit: emit, stderr: true}, &lineWriter{emit: emit, stderr: true}
	command.Stdout = out
	command.Stderr = stderr
	if command.Start() != nil {
		return ErrStart
	}
	if control.started(command.Process) != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return ErrStart
	}
	done := make(chan error, 1)
	go func() {
		err := command.Wait()
		if flushErr := out.flush(); flushErr != nil && err == nil {
			err = flushErr
		}
		if flushErr := stderr.flush(); flushErr != nil && err == nil {
			err = flushErr
		}
		done <- err
	}()
	apply := func(line streamLine) error {
		if log != nil {
			return log(diagnostics.Text(string(line.data), 4096))
		}
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			stopErr := control.stop()
			<-done
			if stopErr != nil {
				return domain.ErrTerminationUnverified
			}
			return ctx.Err()
		case line := <-lines:
			if err := apply(line); err != nil {
				cancel()
				stopErr := control.stop()
				<-done
				if stopErr != nil {
					return domain.ErrTerminationUnverified
				}
				return err
			}
		case err := <-done:
			if control.stop() != nil {
				return domain.ErrTerminationUnverified
			}
			for len(lines) > 0 {
				if err := apply(<-lines); err != nil {
					return err
				}
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return ErrProcess
			}
			return nil
		}
	}
}
