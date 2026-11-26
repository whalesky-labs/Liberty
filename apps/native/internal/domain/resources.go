package domain

import (
	"errors"
	"strings"
)

var (
	ErrResourceInvalid     = errors.New("resource_invalid")
	ErrResourceMissing     = errors.New("resource_not_found")
	ErrBuiltinTemplate     = errors.New("builtin_template_readonly")
	ErrCredentialRequired  = errors.New("credential_required")
	ErrCredentialMigration = errors.New("credential_migration_required")
	ErrCredentialStore     = errors.New("credential_store_unavailable")
	ErrCredentialCleanup   = errors.New("credential_cleanup_pending")
	ErrCommitUnknown       = errors.New("save_outcome_unknown")
	ErrSettingsConflict    = errors.New("settings_conflict")
)

// Secret deliberately cannot be serialized or printed. It is passed only to
// the credential and request adapters; public metadata contains presence flags.
type Secret struct{ value string }

func NewSecret(value string) Secret           { return Secret{value: strings.TrimSpace(value)} }
func (s Secret) Reveal() string               { return s.value }
func (s Secret) Empty() bool                  { return s.value == "" }
func (s Secret) String() string               { return "[credential]" }
func (s Secret) GoString() string             { return "[credential]" }
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`null`), nil }

type CredentialAction string

const (
	KeepCredential  CredentialAction = "keep"
	SetCredential   CredentialAction = "set"
	ClearCredential CredentialAction = "clear"
)

type CredentialUpdate struct {
	Action CredentialAction
	Value  Secret
}

func (u CredentialUpdate) Validate() error {
	switch u.Action {
	case "", KeepCredential, ClearCredential:
		return nil
	case SetCredential:
		if !u.Value.Empty() {
			return nil
		}
	}
	return ErrCredentialRequired
}

type AIModel struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	BaseURL           string `json:"baseUrl"`
	Model             string `json:"model"`
	Enabled           bool   `json:"enabled"`
	IsDefault         bool   `json:"isDefault"`
	CredentialPresent bool   `json:"credentialPresent"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt"`
}

func (m AIModel) Validate() error {
	if strings.TrimSpace(m.ID) == "" || strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.BaseURL) == "" || strings.TrimSpace(m.Model) == "" {
		return ErrResourceInvalid
	}
	return nil
}

type SummaryTemplate struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	Prompt           string `json:"prompt"`
	IncludeSpeaker   bool   `json:"includeSpeakerByDefault"`
	IncludeTimestamp bool   `json:"includeTimestampByDefault"`
	Builtin          bool   `json:"builtin"`
	CreatedAt        string `json:"createdAt"`
	UpdatedAt        string `json:"updatedAt"`
}

func (v SummaryTemplate) Validate() error {
	if strings.TrimSpace(v.ID) == "" || strings.TrimSpace(v.Name) == "" || strings.TrimSpace(v.Prompt) == "" {
		return ErrResourceInvalid
	}
	return nil
}

type MeetingMember struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Department string `json:"department"`
	SortOrder  int64  `json:"sortOrder"`
	IsRecorder bool   `json:"isRecorder"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

func (v MeetingMember) Validate() error {
	if strings.TrimSpace(v.ID) == "" || strings.TrimSpace(v.Name) == "" {
		return ErrResourceInvalid
	}
	return nil
}

type MemberImportResult struct{ Created, Updated int }

// MemberImport is a validated batch owned by the application boundary. Its
// private storage cannot be mutated by adapters after validation.
type MemberImport struct{ values []MeetingMember }

func (batch MemberImport) Members() []MeetingMember {
	return append([]MeetingMember(nil), batch.values...)
}

func NewMemberImport(values []MeetingMember) (MemberImport, error) {
	if len(values) == 0 {
		return MemberImport{}, ErrResourceInvalid
	}
	values = append([]MeetingMember(nil), values...)
	seen := make(map[string]bool, len(values))
	recorders := 0
	for i := range values {
		v := &values[i]
		v.Name, v.Department = strings.TrimSpace(v.Name), strings.TrimSpace(v.Department)
		if v.Validate() != nil || seen[v.Name] {
			return MemberImport{}, ErrResourceInvalid
		}
		seen[v.Name] = true
		if v.IsRecorder {
			recorders++
		}
	}
	if recorders > 1 {
		return MemberImport{}, ErrResourceInvalid
	}
	return MemberImport{values: values}, nil
}
