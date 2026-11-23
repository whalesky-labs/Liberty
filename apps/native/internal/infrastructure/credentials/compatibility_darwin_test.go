//go:build credentials_acceptance

package credentials

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os/exec"
	"strings"
	"testing"

	"liberty.local/liberty/internal/domain"
)

func TestSystemCredentialReadableByLegacySecurityCLI(t *testing.T) {
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	store := &System{service: "LibertyNativeAcceptance-" + hex.EncodeToString(nonce[:])}
	key := "synthetic-legacy-account"
	ctx := context.Background()
	t.Cleanup(func() {
		if store.Delete(ctx, key) != nil {
			t.Error("synthetic cleanup failed")
		}
	})
	value := domain.NewSecret("synthetic-interoperability")
	if store.Set(ctx, key, value) != nil {
		t.Fatal("native credential write failed")
	}
	// Same command used by the Rust client. Capture only; never log its output.
	output, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", store.service, "-a", key, "-w").Output()
	if err != nil {
		t.Fatal("legacy credential reader unavailable")
	}
	if strings.TrimRight(string(output), "\r\n") != value.Reveal() {
		t.Fatal("legacy credential payload differs")
	}
	clear(output)
}
