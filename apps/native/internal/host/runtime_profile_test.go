package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestUnverifiedRuntimeProfileCannotCreateDatabaseOrAcquireOwnership(t *testing.T) {
	for _, runtimeProfile := range []string{"unknown", "security-candidate"} {
		t.Run(runtimeProfile, func(t *testing.T) {
			directory := t.TempDir()
			client, _, err := OpenHandoverWithRuntimeProfile(context.Background(), filepath.Join(directory, "liberty.sqlite3"), true, runtimeProfile)
			if client != nil {
				client.Close()
				t.Fatal("unverified runtime opened a profile")
			}
			if err == nil {
				t.Fatal("unverified runtime accepted")
			}
			entries, readErr := os.ReadDir(directory)
			if readErr != nil || len(entries) != 0 {
				t.Fatal("unverified runtime touched profile", readErr)
			}
		})
	}
}
