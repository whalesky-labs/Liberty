package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"sync"
	"time"

	"liberty.local/liberty/internal/domain"
)

type Credentials interface {
	Get(context.Context, string) (domain.Secret, error)
	Set(context.Context, string, domain.Secret) error
	Delete(context.Context, string) error
}

// These persistence records are private to application/infrastructure. Desktop
// adapters receive only the embedded public snapshot, never credential refs.
type ModelRecord struct {
	Metadata      domain.AIModel
	Reference     string
	LegacyPresent bool
}
type SettingsRecord struct {
	Snapshot      domain.Settings
	Reference     string
	LegacyPresent bool
}
type CredentialCleanup struct{ ID, Owner, Reference, Operation string }
type ResourceRepository interface {
	Models(context.Context) ([]ModelRecord, error)
	Model(context.Context, string) (ModelRecord, error)
	PublishModel(context.Context, ModelRecord, string, *CredentialCleanup) error
	RemoveModel(context.Context, string, string, *CredentialCleanup) error
	Settings(context.Context) (SettingsRecord, error)
	PublishSettings(context.Context, SettingsRecord, string, *CredentialCleanup) error
	Templates(context.Context) ([]domain.SummaryTemplate, error)
	SaveTemplate(context.Context, domain.SummaryTemplate) error
	RemoveTemplate(context.Context, string) error
	Members(context.Context) ([]domain.MeetingMember, error)
	SaveMember(context.Context, domain.MeetingMember) error
	RemoveMember(context.Context, string) error
	ImportMembers(context.Context, domain.MemberImport) (domain.MemberImportResult, error)
	RememberCredential(context.Context, CredentialCleanup) error
	PendingCredentials(context.Context) ([]CredentialCleanup, error)
	CredentialReferenced(context.Context, string) (bool, error)
	BackupCredentialReferenced(context.Context, string) (bool, error)
	FinishCredential(context.Context, string) error
	FailCredential(context.Context, string) error
}

// Resources serializes credential publication, retirement, and recovery. SQL
// transactions remain in the repository, and OS credential I/O stays outside
// transactions. Runtime updates independently use settings revision CAS.
type Resources struct {
	settingsChanged func(domain.Settings)
	mu              sync.Mutex
	repository      ResourceRepository
	credentials     Credentials
	now             func() time.Time
	closed          bool
}

func NewResources(repository ResourceRepository, credentials Credentials, settingsChanged ...func(domain.Settings)) *Resources {
	service := &Resources{repository: repository, credentials: credentials, now: time.Now}
	if len(settingsChanged) > 0 {
		service.settingsChanged = settingsChanged[0]
	}
	return service
}
func resourceID(prefix string) string {
	var bytes [16]byte
	_, _ = rand.Read(bytes[:])
	return prefix + "-" + hex.EncodeToString(bytes[:])
}
func (s *Resources) Models(ctx context.Context) ([]domain.AIModel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrWindowScope
	}
	records, err := s.repository.Models(ctx)
	if err != nil {
		return nil, err
	}
	values := make([]domain.AIModel, 0, len(records))
	for _, r := range records {
		value := r.Metadata
		value.CredentialPresent = r.LegacyPresent
		if !value.CredentialPresent && r.Reference != "" {
			secret, err := s.credentials.Get(ctx, r.Reference)
			if err != nil {
				return nil, err
			}
			value.CredentialPresent = !secret.Empty()
		}
		values = append(values, value)
	}
	return values, nil
}
func (s *Resources) Settings(ctx context.Context) (domain.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return domain.Settings{}, ErrWindowScope
	}
	return s.settings(ctx)
}
func (s *Resources) settings(ctx context.Context) (domain.Settings, error) {
	record, err := s.repository.Settings(ctx)
	if err != nil {
		return domain.Settings{}, err
	}
	value := record.Snapshot
	value.TokenConfigured = record.LegacyPresent
	if record.Reference != "" {
		secret, err := s.credentials.Get(ctx, record.Reference)
		if err != nil {
			return domain.Settings{}, err
		}
		value.TokenConfigured = !secret.Empty()
	}
	return value, nil
}

// Stage a durable rollback intent BEFORE writing the OS secret. Publication
// deletes this intent in the same SQLite transaction that exposes the reference.
// Thus a crash can leave an orphan secret, but never an untracked orphan.
func (s *Resources) stage(ctx context.Context, owner, base string, secret domain.Secret) (string, error) {
	reference := base + ":staged:" + resourceID("write")
	intent := CredentialCleanup{ID: resourceID("credential-cleanup"), Owner: owner, Reference: reference, Operation: "rollback"}
	if err := s.repository.RememberCredential(ctx, intent); err != nil {
		return "", err
	}
	if err := s.credentials.Set(ctx, reference, secret); err != nil {
		_ = s.recoverAfterOperation()
		return "", err
	}
	saved, err := s.credentials.Get(ctx, reference)
	if err == nil && saved.Reveal() != secret.Reveal() {
		err = domain.ErrCredentialStore
	}
	if err != nil {
		_ = s.recoverAfterOperation()
		return "", err
	}
	return reference, nil
}
func (s *Resources) retire(owner, old, next string) *CredentialCleanup {
	if old == "" || old == next {
		return nil
	}
	return &CredentialCleanup{ID: resourceID("credential-cleanup"), Owner: owner, Reference: old, Operation: "retire"}
}

// Recovery ignores an operation's canceled context: cleanup and commit outcome
// resolution must finish before the profile lock is released.
func (s *Resources) recoverAfterOperation() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.recover(ctx)
}
func (s *Resources) RecoverCredentials(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrWindowScope
	}
	return s.recover(ctx)
}
func (s *Resources) recover(ctx context.Context) error {
	pending, err := s.repository.PendingCredentials(ctx)
	if err != nil {
		return err
	}
	failed := false
	for _, intent := range pending {
		referenced, err := s.repository.CredentialReferenced(ctx, intent.Reference)
		if err != nil {
			return err
		}
		if !referenced {
			retained, err := s.repository.BackupCredentialReferenced(ctx, intent.Reference)
			if err != nil {
				return err
			}
			if retained {
				continue
			}
			if err = s.credentials.Delete(ctx, intent.Reference); err != nil {
				failed = true
				if err = s.repository.FailCredential(ctx, intent.ID); err != nil {
					return err
				}
				continue
			}
		}
		if err = s.repository.FinishCredential(ctx, intent.ID); err != nil {
			return err
		}
	}
	if failed {
		return domain.ErrCredentialCleanup
	}
	return nil
}

// MutationOutcome distinguishes a successful durable save from deferred secret
// cleanup. A caller must not revert a committed edit when CleanupPending is true.
type MutationOutcome struct{ CleanupPending bool }

func (s *Resources) SaveModel(ctx context.Context, value domain.AIModel, update domain.CredentialUpdate) (domain.AIModel, MutationOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return domain.AIModel{}, MutationOutcome{}, ErrWindowScope
	}
	if value.ID == "" {
		value.ID = resourceID("model")
	}
	if err := value.Validate(); err != nil {
		return domain.AIModel{}, MutationOutcome{}, err
	}
	if err := update.Validate(); err != nil {
		return domain.AIModel{}, MutationOutcome{}, err
	}
	old, err := s.repository.Model(ctx, value.ID)
	exists := err == nil
	if err != nil && !errors.Is(err, domain.ErrResourceMissing) {
		return domain.AIModel{}, MutationOutcome{}, err
	}
	if old.LegacyPresent {
		return domain.AIModel{}, MutationOutcome{}, domain.ErrCredentialMigration
	}
	if old.Reference == "" && update.Action != domain.SetCredential {
		return domain.AIModel{}, MutationOutcome{}, domain.ErrCredentialRequired
	}
	now := domain.PersistedUTC(s.now())
	value.CreatedAt = old.Metadata.CreatedAt
	if !exists {
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	next := old.Reference
	present := false
	if update.Action == domain.SetCredential {
		present = true
	} else if update.Action != domain.ClearCredential && next != "" {
		secret, readErr := s.credentials.Get(ctx, next)
		if readErr != nil {
			return domain.AIModel{}, MutationOutcome{}, readErr
		}
		present = !secret.Empty()
	}
	switch update.Action {
	case domain.SetCredential:
		next, err = s.stage(ctx, value.ID, "ai-model:"+value.ID+":api-key", update.Value)
	case domain.ClearCredential:
		next = ""
	}
	if err != nil {
		return domain.AIModel{}, MutationOutcome{}, err
	}
	// An explicit enabled default wins; otherwise retain the existing fallback.
	records, listErr := s.repository.Models(ctx)
	if listErr != nil {
		_ = s.recoverAfterOperation()
		return domain.AIModel{}, MutationOutcome{}, listErr
	}
	current := make([]domain.AIModel, 0, len(records)+1)
	if !exists {
		current = append(current, value)
	}
	for _, r := range records {
		if r.Metadata.ID == value.ID {
			current = append(current, value)
		} else {
			current = append(current, r.Metadata)
		}
	}
	defaultID, firstEnabled := "", ""
	if value.Enabled && value.IsDefault {
		defaultID = value.ID
	}
	for _, m := range current {
		if m.Enabled {
			if firstEnabled == "" {
				firstEnabled = m.ID
			}
			if m.IsDefault && defaultID == "" {
				defaultID = m.ID
			}
		}
	}
	if defaultID == "" {
		defaultID = firstEnabled
	}
	value.IsDefault = value.ID == defaultID
	value.CredentialPresent = false // presence is derived from the system store.
	record := ModelRecord{Metadata: value, Reference: next}
	err = s.repository.PublishModel(ctx, record, old.Reference, s.retire(value.ID, old.Reference, next))
	if err != nil {
		checkCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		actual, checkErr := s.repository.Model(checkCtx, value.ID)
		cancel()
		if checkErr != nil && !errors.Is(checkErr, domain.ErrResourceMissing) {
			return domain.AIModel{}, MutationOutcome{}, domain.ErrCommitUnknown
		}
		if checkErr != nil || actual.Reference != next || actual.Metadata != record.Metadata {
			_ = s.recoverAfterOperation()
			return domain.AIModel{}, MutationOutcome{}, err
		}
	}
	cleanupErr := s.recoverAfterOperation()
	value.CredentialPresent = present
	return value, MutationOutcome{CleanupPending: cleanupErr != nil}, nil
}
func (s *Resources) DeleteModel(ctx context.Context, id string) (MutationOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return MutationOutcome{}, ErrWindowScope
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return MutationOutcome{}, domain.ErrResourceInvalid
	}
	old, err := s.repository.Model(ctx, id)
	if errors.Is(err, domain.ErrResourceMissing) {
		return MutationOutcome{}, s.recoverAfterOperation()
	}
	if err != nil {
		return MutationOutcome{}, err
	}
	err = s.repository.RemoveModel(ctx, id, old.Reference, s.retire(id, old.Reference, ""))
	if err != nil {
		checkCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, checkErr := s.repository.Model(checkCtx, id)
		cancel()
		if checkErr == nil {
			return MutationOutcome{}, err
		}
		if !errors.Is(checkErr, domain.ErrResourceMissing) {
			return MutationOutcome{}, domain.ErrCommitUnknown
		}
	}
	return MutationOutcome{CleanupPending: s.recoverAfterOperation() != nil}, nil
}
func (s *Resources) SaveSettings(ctx context.Context, value domain.Settings, update domain.CredentialUpdate) (domain.Settings, MutationOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return domain.Settings{}, MutationOutcome{}, ErrWindowScope
	}
	if err := update.Validate(); err != nil {
		return domain.Settings{}, MutationOutcome{}, err
	}
	old, err := s.repository.Settings(ctx)
	if err != nil {
		return domain.Settings{}, MutationOutcome{}, err
	}
	if value.Revision < 0 || value.Revision == math.MaxInt64 {
		return domain.Settings{}, MutationOutcome{}, domain.ErrResourceInvalid
	}
	if old.Snapshot.Revision != value.Revision {
		current, err := s.settings(ctx)
		if err != nil {
			return domain.Settings{}, MutationOutcome{}, err
		}
		return domain.Settings{}, MutationOutcome{}, &domain.SettingsConflict{Current: current}
	}
	if old.LegacyPresent {
		return domain.Settings{}, MutationOutcome{}, domain.ErrCredentialMigration
	}
	next := old.Reference
	present := false
	if update.Action == domain.SetCredential {
		present = true
	} else if update.Action != domain.ClearCredential && next != "" {
		secret, readErr := s.credentials.Get(ctx, next)
		if readErr != nil {
			return domain.Settings{}, MutationOutcome{}, readErr
		}
		present = !secret.Empty()
	}
	switch update.Action {
	case domain.SetCredential:
		next, err = s.stage(ctx, "settings", "settings:remote-api-token", update.Value)
	case domain.ClearCredential:
		next = ""
	}
	if err != nil {
		return domain.Settings{}, MutationOutcome{}, err
	}
	value = value.Normalized()
	value.TokenConfigured = false
	record := SettingsRecord{Snapshot: value, Reference: next}
	err = s.repository.PublishSettings(ctx, record, old.Reference, s.retire("settings", old.Reference, next))
	if err != nil {
		checkCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		actual, checkErr := s.repository.Settings(checkCtx)
		cancel()
		if checkErr != nil {
			return domain.Settings{}, MutationOutcome{}, domain.ErrCommitUnknown
		}
		expected := value
		expected.Revision++
		if actual.Reference != next || actual.Snapshot != expected {
			_ = s.recoverAfterOperation()
			if errors.Is(err, domain.ErrSettingsConflict) {
				current, loadErr := s.settings(ctx)
				if loadErr == nil {
					return domain.Settings{}, MutationOutcome{}, &domain.SettingsConflict{Current: current}
				}
			}
			return domain.Settings{}, MutationOutcome{}, err
		}
	}
	value.Revision++
	value.TokenConfigured = present
	if s.settingsChanged != nil {
		s.settingsChanged(value)
	}
	return value, MutationOutcome{CleanupPending: s.recoverAfterOperation() != nil}, nil
}

func (s *Resources) Templates(ctx context.Context) ([]domain.SummaryTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrWindowScope
	}
	return s.repository.Templates(ctx)
}
func (s *Resources) SaveTemplate(ctx context.Context, value domain.SummaryTemplate) (domain.SummaryTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return domain.SummaryTemplate{}, ErrWindowScope
	}
	return s.saveTemplate(ctx, value)
}
func (s *Resources) duplicateTemplate(ctx context.Context, id, suffix string) (domain.SummaryTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return domain.SummaryTemplate{}, ErrWindowScope
	}
	values, err := s.repository.Templates(ctx)
	if err != nil {
		return domain.SummaryTemplate{}, err
	}
	for _, source := range values {
		if source.ID == id {
			source.ID, source.Builtin, source.CreatedAt, source.UpdatedAt = "", false, "", ""
			source.Name += suffix
			return s.saveTemplate(ctx, source)
		}
	}
	return domain.SummaryTemplate{}, domain.ErrResourceMissing
}
func (s *Resources) saveTemplate(ctx context.Context, value domain.SummaryTemplate) (domain.SummaryTemplate, error) {
	if value.Builtin {
		return domain.SummaryTemplate{}, domain.ErrBuiltinTemplate
	}
	if value.ID == "" {
		value.ID = resourceID("template")
	}
	value.Name = strings.TrimSpace(value.Name)
	value.Description = strings.TrimSpace(value.Description)
	value.Prompt = strings.TrimSpace(value.Prompt)
	if err := value.Validate(); err != nil {
		return domain.SummaryTemplate{}, err
	}
	now := domain.PersistedUTC(s.now())
	if value.CreatedAt == "" {
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	if err := s.repository.SaveTemplate(ctx, value); err != nil {
		return domain.SummaryTemplate{}, err
	}
	return value, nil
}
func (s *Resources) DeleteTemplate(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrWindowScope
	}
	return s.repository.RemoveTemplate(ctx, id)
}
func (s *Resources) Members(ctx context.Context) ([]domain.MeetingMember, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrWindowScope
	}
	return s.repository.Members(ctx)
}
func (s *Resources) SaveMember(ctx context.Context, value domain.MeetingMember) (domain.MeetingMember, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return domain.MeetingMember{}, ErrWindowScope
	}
	if value.ID == "" {
		value.ID = resourceID("member")
	}
	value.Name = strings.TrimSpace(value.Name)
	value.Department = strings.TrimSpace(value.Department)
	if err := value.Validate(); err != nil {
		return domain.MeetingMember{}, err
	}
	now := domain.PersistedUTC(s.now())
	if value.CreatedAt == "" {
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	if err := s.repository.SaveMember(ctx, value); err != nil {
		return domain.MeetingMember{}, err
	}
	return value, nil
}
func (s *Resources) DeleteMember(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrWindowScope
	}
	return s.repository.RemoveMember(ctx, id)
}
func (s *Resources) ImportMembers(ctx context.Context, values []domain.MeetingMember) (domain.MemberImportResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return domain.MemberImportResult{}, ErrWindowScope
	}
	values = append([]domain.MeetingMember(nil), values...)
	now := domain.PersistedUTC(s.now())
	for i := range values {
		values[i].ID = resourceID("member")
		values[i].CreatedAt = now
		values[i].UpdatedAt = now
	}
	batch, err := domain.NewMemberImport(values)
	if err != nil {
		return domain.MemberImportResult{}, err
	}
	return s.repository.ImportMembers(ctx, batch)
}

// Close waits for all credential publication and cleanup before profile ownership ends.
func (s *Resources) Close() { s.mu.Lock(); defer s.mu.Unlock(); s.closed = true }
