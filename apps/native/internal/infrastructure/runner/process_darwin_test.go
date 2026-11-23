package runner

import (
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/sys/unix"
	"strconv"
	"testing"
	"time"
)

func TestActualProcessIdentityAndTreeCancellation(t *testing.T) {
	spec := testSpec(t, "tree")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var childPID uint32
	var parentIdentity Identity
	_, err := Execute(ctx, spec, Hooks{
		Started: func(pid uint32) error {
			encoded, err := CaptureIdentity(pid)
			if err != nil {
				return err
			}
			return json.Unmarshal([]byte(encoded), &parentIdentity)
		},
		Event: func(event Event) error {
			value, err := strconv.ParseUint(event.Message, 10, 32)
			if err != nil {
				return err
			}
			childPID = uint32(value)
			cancel()
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) || childPID == 0 || parentIdentity.StartTime == 0 || len(parentIdentity.Command) == 0 {
		t.Fatalf("identity/tree execution failed: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	var lastErr error
	var lastStatus int8
	for time.Now().Before(deadline) {
		processes, err := unix.SysctlKinfoProcSlice("kern.proc.pid", int(childPID))
		lastErr = err
		if len(processes) > 0 {
			lastStatus = processes[0].Proc.P_stat
		}
		if errors.Is(err, unix.ESRCH) || (err == nil && (len(processes) == 0 || lastStatus == 5)) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant termination not verified: status=%d error=%v", lastStatus, lastErr)
}
