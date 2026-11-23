package credentials

import (
	"context"
	"encoding/hex"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"liberty.local/liberty/internal/domain"
)

// Use the same executable as Rust, preserving Keychain's trusted-application
// boundary. A different Security.framework caller creates different ACLs even
// when the service/account strings match. Secret values never enter argv.
type boundedOutput struct {
	bytes    []byte
	overflow bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	size := len(p)
	remaining := 32768 - len(b.bytes)
	if size > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	b.bytes = append(b.bytes, p...)
	return size, nil
}
func (s *System) command(operation, key string, value *domain.Secret) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	arguments := []string{operation, "-s", s.service, "-a", key}
	if operation == "find-generic-password" {
		arguments = append(arguments, "-g")
	}
	if operation == "add-generic-password" {
		arguments = append(arguments, "-U", "-w")
	}
	if value != nil {
		arguments = []string{"-i"}
	}
	command := exec.CommandContext(ctx, "/usr/bin/security", arguments...)
	if value != nil {
		command.Stdin = strings.NewReader("add-generic-password -U -s " + securityQuote(s.service) + " -a " + securityQuote(key) + " -w " + securityQuote(value.Reveal()) + "\n")
	}
	var stdout, stderr boundedOutput
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	missing := strings.Contains(string(stderr.bytes), "could not be found") || strings.Contains(string(stderr.bytes), "-25300")
	defer clear(stderr.bytes)
	if err != nil || stdout.overflow || stderr.overflow {
		clear(stdout.bytes)
		if missing && operation != "add-generic-password" {
			return nil, true, nil
		}
		return nil, false, domain.ErrCredentialStore
	}
	if operation == "find-generic-password" {
		clear(stdout.bytes)
		return append([]byte(nil), stderr.bytes...), false, nil
	}
	return stdout.bytes, false, nil
}
func (s *System) get(key string) (domain.Secret, error) {
	output, missing, err := s.command("find-generic-password", key, nil)
	defer clear(output)
	if err != nil || missing {
		return domain.Secret{}, err
	}
	return parseSecurityPassword(output)
}
func (s *System) set(key string, value domain.Secret) error {
	// security(1) interactive input is line oriented. Refuse embedded command
	// separators before quoting a single command, without invoking a shell.
	if strings.ContainsAny(value.Reveal()+key+s.service, "\r\n") {
		return domain.ErrCredentialStore
	}
	output, _, err := s.command("add-generic-password", key, &value)
	clear(output)
	if err != nil {
		return err
	}
	saved, err := s.get(key)
	if err != nil || saved.Reveal() != value.Reveal() {
		return domain.ErrCredentialStore
	}
	return nil
}
func (s *System) delete(key string) error {
	output, _, err := s.command("delete-generic-password", key, nil)
	clear(output)
	return err
}

func securityQuote(value string) string {
	return "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(value) + "\""
}
func parseSecurityPassword(output []byte) (domain.Secret, error) {
	text := strings.TrimSpace(string(output))
	if !strings.HasPrefix(text, "password: ") {
		return domain.Secret{}, domain.ErrCredentialStore
	}
	text = strings.TrimPrefix(text, "password: ")
	if strings.HasPrefix(text, "0x") {
		encoded := strings.Fields(text)[0][2:]
		decoded, err := hex.DecodeString(encoded)
		defer clear(decoded)
		if err != nil || !utf8.Valid(decoded) {
			return domain.Secret{}, domain.ErrCredentialStore
		}
		return domain.NewSecret(string(decoded)), nil
	}
	if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' && utf8.ValidString(text) {
		return domain.NewSecret(text[1 : len(text)-1]), nil
	}
	return domain.Secret{}, domain.ErrCredentialStore
}
