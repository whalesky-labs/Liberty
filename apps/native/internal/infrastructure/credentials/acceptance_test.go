//go:build credentials_acceptance && (darwin || windows)

package credentials

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"liberty.local/liberty/internal/domain"
)

func TestIsolatedSystemCredentialRoundtrip(t *testing.T) {
	var id [16]byte
	_, _ = rand.Read(id[:])
	// This fresh service cannot enumerate, read, overwrite or remove a user's
	// Liberty credentials. Only the one synthetic account is ever accessed.
	store := &System{service: "LibertyNativeAcceptance-" + hex.EncodeToString(id[:])}
	ctx := context.Background()
	key := "synthetic-test-account"
	t.Cleanup(func() {
		if store.Delete(ctx, key) != nil {
			t.Error("synthetic cleanup failed")
		}
	})
	got, err := store.Get(ctx, key)
	if err != nil || !got.Empty() {
		t.Fatal("fresh account not absent")
	}
	first := domain.NewSecret("synthetic-keychain-测试")
	if store.Set(ctx, key, first) != nil {
		t.Fatal("write failed")
	}
	got, err = store.Get(ctx, key)
	if err != nil || got.Reveal() != first.Reveal() {
		t.Fatal("read differs")
	}
	second := domain.NewSecret("synthetic quote\"slash\\ $literal")
	if store.Set(ctx, key, second) != nil {
		t.Fatal("replacement failed")
	}
	got, err = store.Get(ctx, key)
	if err != nil || got.Reveal() != second.Reveal() {
		t.Fatal("replacement not durable")
	}
	if store.Delete(ctx, key) != nil || store.Delete(ctx, key) != nil {
		t.Fatal("idempotent deletion failed")
	}
	got, err = store.Get(ctx, key)
	if err != nil || !got.Empty() {
		t.Fatal("deleted value remains")
	}
}
