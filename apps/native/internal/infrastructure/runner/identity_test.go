package runner

import (
	"encoding/json"
	"testing"
)

func TestIdentityRejectsPIDReuseAndRequiresExactLegacyAttempt(t *testing.T) {
	current := Identity{Version: 1, StartTime: 123, Executable: "/synthetic/python", Command: []string{"/synthetic/python", "/synthetic/runner.py", "--job-dir", "/synthetic/job-1700000000000-1/attempts/attempt-2-3"}}
	data, _ := json.Marshal(current)
	if matches, err := identityMatches(string(data), current); err != nil || !matches {
		t.Fatal("matching identity rejected")
	}
	reused := current
	reused.StartTime++
	if matches, err := identityMatches(string(data), reused); err != nil || matches {
		t.Fatal("reused pid accepted")
	}
	for _, invalid := range []string{`{}`, `{"version":2}`, string(data) + ` {}`, "invalid"} {
		if matches, err := identityMatches(invalid, current); err == nil || matches {
			t.Fatal("invalid identity accepted")
		}
	}
	if matches, err := identityMatches("/synthetic/python|/synthetic/runner.py|job-1700000000000-1|2|3", current); err != nil || !matches {
		t.Fatal("legacy identity rejected")
	}
	if matches, err := identityMatches("/synthetic/python|/synthetic/runner.py|job-1700000000000-1|2|4", current); err != nil || matches {
		t.Fatal("legacy stale attempt accepted")
	}
}
