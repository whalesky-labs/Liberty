//go:build compatibility

package jobdb

import (
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// Frozen Rust samples record UTC instants and local calendar outcomes in
	// UTC+08:00. Replay that input context on every host; production date rules
	// and the untagged timezone tests continue to use the user's local zone.
	time.Local = time.FixedZone("native-golden-UTC+08", 8*60*60)
	os.Exit(m.Run())
}
