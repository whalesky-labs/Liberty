package runner

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestInstallerCommandRedactsBothStreamsAndFlushesFinalLine(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	lines := []string{}
	err = RunCommand(context.Background(), exe, nil, t.TempDir(), map[string]string{"LIBERTY_RUNNER_TEST_CHILD": "command"}, func(line string) error { lines = append(lines, line); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatal("command streams not fully drained")
	}
	for _, line := range lines {
		if strings.Contains(line, "hidden") || strings.Contains(line, "private.wav") {
			t.Fatal("command diagnostic leaked")
		}
	}
}
func TestInstallerCommandCancellationAndLogFailureJoinProcess(t *testing.T) {
	for _, callbackFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "callback"}[callbackFailure], func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			failure := errors.New("synthetic_log_failure")
			err = RunCommand(ctx, exe, nil, t.TempDir(), map[string]string{"LIBERTY_RUNNER_TEST_CHILD": "command_wait"}, func(string) error {
				if callbackFailure {
					return failure
				}
				cancel()
				return nil
			})
			want := error(context.Canceled)
			if callbackFailure {
				want = failure
			}
			if !errors.Is(err, want) {
				t.Fatalf("got %v want %v", err, want)
			}
		})
	}
}
func TestInstallerCommandFailureCannotReportSuccess(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := RunCommand(context.Background(), exe, nil, t.TempDir(), map[string]string{"LIBERTY_RUNNER_TEST_CHILD": "failure"}, nil); !errors.Is(err, ErrProcess) {
		t.Fatal("failed validator reported success")
	}
}
