// Package credentials uses the same service, target names and UTF-8 payloads
// as the Rust client. No file or plaintext database fallback is provided.
package credentials

import (
	"context"
	"strings"

	"liberty.local/liberty/internal/domain"
)

const serviceName = "LibertyDesktop"

type System struct{ service string }

func NewSystem() *System { return &System{service: serviceName} }
func validKey(key string) bool {
	return key != "" && len(key) < 2048 && !strings.ContainsAny(key, "\x00\r\n")
}
func (s *System) Get(ctx context.Context, key string) (domain.Secret, error) {
	if err := ctx.Err(); err != nil {
		return domain.Secret{}, err
	}
	if !validKey(key) {
		return domain.Secret{}, domain.ErrCredentialStore
	}
	return s.get(key)
}
func (s *System) Set(ctx context.Context, key string, value domain.Secret) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validKey(key) || value.Empty() || len(value.Reveal()) > 2560 || strings.ContainsRune(value.Reveal(), 0) {
		return domain.ErrCredentialStore
	}
	return s.set(key, value)
}
func (s *System) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validKey(key) {
		return domain.ErrCredentialStore
	}
	return s.delete(key)
}
