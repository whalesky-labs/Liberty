//go:build !darwin && !windows

package credentials

import "liberty.local/liberty/internal/domain"

func (s *System) get(string) (domain.Secret, error) {
	return domain.Secret{}, domain.ErrCredentialStore
}
func (s *System) set(string, domain.Secret) error { return domain.ErrCredentialStore }
func (s *System) delete(string) error             { return domain.ErrCredentialStore }
