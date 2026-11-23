package runner

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
)

var ErrIdentity = errors.New("runner_identity_unverified")
var ErrProcessGone = errors.New("runner_process_gone")

// Identity keeps the exact persisted Rust v1 shape for rollback compatibility.
// It stays inside the execution service and is never included in diagnostics.
type Identity struct {
	Version    uint8    `json:"version"`
	StartTime  uint64   `json:"start_time"`
	Executable string   `json:"executable"`
	Command    []string `json:"command"`
}

func CaptureIdentity(pid uint32) (string, error) {
	identity, err := readIdentity(pid)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(identity)
	if err != nil {
		return "", ErrIdentity
	}
	return string(data), nil
}
func identityMatches(persisted string, current Identity) (bool, error) {
	var expected Identity
	decoder := json.NewDecoder(bytes.NewBufferString(persisted))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&expected); err == nil {
		if decoder.Decode(new(any)) != io.EOF || expected.Version != 1 {
			return false, ErrIdentity
		}
		return reflect.DeepEqual(expected, current), nil
	}
	// Match legacy five-part identities only with the same explicit attempt.
	parts := strings.Split(persisted, "|")
	if len(parts) != 5 {
		return false, ErrIdentity
	}
	attempt, err := strconv.ParseUint(parts[3], 10, 64)
	if err != nil {
		return false, ErrIdentity
	}
	lease, err := strconv.ParseUint(parts[4], 10, 64)
	if err != nil {
		return false, ErrIdentity
	}
	executable := current.Executable == parts[0] || (len(current.Command) > 0 && current.Command[0] == parts[0])
	script, job := false, false
	for i, arg := range current.Command {
		if arg == parts[1] {
			script = true
		}
		if arg == "--job-dir" && i+1 < len(current.Command) {
			path := current.Command[i+1]
			job = strings.Contains(path, parts[2]) && strings.Contains(path, "attempt-"+strconv.FormatUint(attempt, 10)+"-"+strconv.FormatUint(lease, 10))
		}
	}
	return executable && script && job, nil
}

// TerminatePersisted refuses a missing/invalid identity and never signals a
// reused PID. Platform adapters recheck identity immediately before signalling.
func TerminatePersisted(pid *uint32, persisted *string) error {
	if pid == nil {
		return nil
	}
	if persisted == nil || strings.TrimSpace(*persisted) == "" {
		return ErrIdentity
	}
	current, err := readIdentity(*pid)
	if errors.Is(err, ErrProcessGone) {
		return nil
	}
	if err != nil {
		return err
	}
	matches, err := identityMatches(*persisted, current)
	if err != nil {
		return err
	}
	if !matches {
		return nil
	}
	return terminateVerified(*pid, current)
}
