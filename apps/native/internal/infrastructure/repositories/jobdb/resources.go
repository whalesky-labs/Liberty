package jobdb

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/repositories/schema"
)

type rowScanner interface{ Scan(...any) error }

const modelColumns = `id,name,base_url,model,enabled,is_default,created_at,updated_at,COALESCE(api_key_ref,''),TRIM(api_key)<>''`

func scanModel(row rowScanner) (application.ModelRecord, error) {
	var r application.ModelRecord
	err := row.Scan(&r.Metadata.ID, &r.Metadata.Name, &r.Metadata.BaseURL, &r.Metadata.Model, &r.Metadata.Enabled, &r.Metadata.IsDefault, &r.Metadata.CreatedAt, &r.Metadata.UpdatedAt, &r.Reference, &r.LegacyPresent)
	if err == sql.ErrNoRows {
		return r, domain.ErrResourceMissing
	}
	if err != nil {
		return r, ErrDatabase
	}
	return r, nil
}
func (s *Store) Models(ctx context.Context) ([]application.ModelRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+modelColumns+` FROM ai_model_configs ORDER BY datetime(updated_at) DESC,updated_at DESC`)
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	result := []application.ModelRecord{}
	for rows.Next() {
		r, err := scanModel(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return result, nil
}
func (s *Store) Model(ctx context.Context, id string) (application.ModelRecord, error) {
	return scanModel(s.db.QueryRowContext(ctx, `SELECT `+modelColumns+` FROM ai_model_configs WHERE id=?`, id))
}
func (s *Store) PublishModel(ctx context.Context, r application.ModelRecord, expected string, retire *application.CredentialCleanup) error {
	if err := r.Metadata.Validate(); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		old, err := scanModel(tx.QueryRowContext(ctx, `SELECT `+modelColumns+` FROM ai_model_configs WHERE id=?`, r.Metadata.ID))
		if err != nil && err != domain.ErrResourceMissing {
			return err
		}
		if old.Reference != expected {
			return domain.ErrConflict
		}
		if old.LegacyPresent {
			return domain.ErrCredentialMigration
		}
		m := r.Metadata
		if m.IsDefault {
			if err := execute(ctx, tx, `UPDATE ai_model_configs SET is_default=0`); err != nil {
				return err
			}
		}
		if err := execute(ctx, tx, `INSERT INTO ai_model_configs(id,name,base_url,api_key,api_key_ref,model,enabled,is_default,created_at,updated_at) VALUES(?,?,?,'',?,?,?,?,?,?)
 ON CONFLICT(id) DO UPDATE SET name=excluded.name,base_url=excluded.base_url,api_key='',api_key_ref=excluded.api_key_ref,model=excluded.model,enabled=excluded.enabled,is_default=excluded.is_default,updated_at=excluded.updated_at`, m.ID, m.Name, m.BaseURL, r.Reference, m.Model, m.Enabled, m.IsDefault, m.CreatedAt, m.UpdatedAt); err != nil {
			return err
		}
		return publishCredential(ctx, tx, r.Reference, retire)
	})
}
func (s *Store) RemoveModel(ctx context.Context, id, expected string, retire *application.CredentialCleanup) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		old, err := scanModel(tx.QueryRowContext(ctx, `SELECT `+modelColumns+` FROM ai_model_configs WHERE id=?`, id))
		if err != nil && err != domain.ErrResourceMissing {
			return err
		}
		if old.Reference != expected {
			return domain.ErrConflict
		}
		if err := execute(ctx, tx, `DELETE FROM ai_model_configs WHERE id=?`, id); err != nil {
			return err
		}
		return publishCredential(ctx, tx, "", retire)
	})
}
func rememberCredential(ctx context.Context, tx *sql.Tx, intent application.CredentialCleanup) error {
	if intent.Reference == "" {
		return nil
	}
	now := domain.PersistedUTC(time.Now())
	return execute(ctx, tx, `INSERT INTO credential_cleanup_intents(id,model_id,credential_reference,operation,attempt,last_error,created_at,updated_at) VALUES(?,?,?,?,0,NULL,?,?)
 ON CONFLICT(credential_reference) DO UPDATE SET model_id=excluded.model_id,operation=excluded.operation,updated_at=excluded.updated_at`, intent.ID, intent.Owner, intent.Reference, intent.Operation, now, now)
}
func publishCredential(ctx context.Context, tx *sql.Tx, reference string, retire *application.CredentialCleanup) error {
	if reference != "" {
		if err := execute(ctx, tx, `DELETE FROM credential_cleanup_intents WHERE credential_reference=?`, reference); err != nil {
			return err
		}
	}
	if retire != nil {
		return rememberCredential(ctx, tx, *retire)
	}
	return nil
}
func (s *Store) RememberCredential(ctx context.Context, intent application.CredentialCleanup) error {
	return s.transaction(ctx, func(tx *sql.Tx) error { return rememberCredential(ctx, tx, intent) })
}
func (s *Store) PendingCredentials(ctx context.Context) ([]application.CredentialCleanup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,model_id,credential_reference,operation FROM credential_cleanup_intents ORDER BY created_at,id`)
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	result := []application.CredentialCleanup{}
	for rows.Next() {
		var r application.CredentialCleanup
		if rows.Scan(&r.ID, &r.Owner, &r.Reference, &r.Operation) != nil {
			return nil, ErrDatabase
		}
		result = append(result, r)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return result, nil
}
func (s *Store) CredentialReferenced(ctx context.Context, reference string) (bool, error) {
	var found bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_model_configs WHERE api_key_ref=? UNION ALL SELECT 1 FROM app_settings WHERE api_token_ref=?)`, reference, reference).Scan(&found)
	if err != nil {
		return false, ErrDatabase
	}
	return found, nil
}
func (s *Store) BackupCredentialReferenced(ctx context.Context, reference string) (bool, error) {
	return schema.BackupCredentialReferenced(ctx, s.path, reference)
}
func (s *Store) FinishCredential(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM credential_cleanup_intents WHERE id=?`, id)
	if err != nil {
		return ErrDatabase
	}
	return nil
}
func (s *Store) FailCredential(ctx context.Context, id string) error {
	// Never persist an OS error which could include a key, account or secret.
	_, err := s.db.ExecContext(ctx, `UPDATE credential_cleanup_intents SET attempt=attempt+1,last_error='credential_store_unavailable',updated_at=? WHERE id=?`, domain.PersistedUTC(time.Now()), id)
	if err != nil {
		return ErrDatabase
	}
	return nil
}

func (s *Store) Templates(ctx context.Context) ([]domain.SummaryTemplate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,description,prompt,include_speaker_by_default,include_timestamp_by_default,builtin,created_at,updated_at FROM ai_summary_templates ORDER BY builtin DESC,datetime(updated_at) DESC,updated_at DESC`)
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	values := []domain.SummaryTemplate{}
	for rows.Next() {
		var r domain.SummaryTemplate
		if rows.Scan(&r.ID, &r.Name, &r.Description, &r.Prompt, &r.IncludeSpeaker, &r.IncludeTimestamp, &r.Builtin, &r.CreatedAt, &r.UpdatedAt) != nil {
			return nil, ErrDatabase
		}
		values = append(values, r)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return values, nil
}
func (s *Store) SaveTemplate(ctx context.Context, v domain.SummaryTemplate) error {
	if err := v.Validate(); err != nil {
		return err
	}
	if v.Builtin {
		return domain.ErrBuiltinTemplate
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var builtin bool
		err := tx.QueryRowContext(ctx, `SELECT builtin FROM ai_summary_templates WHERE id=?`, v.ID).Scan(&builtin)
		if err != nil && err != sql.ErrNoRows {
			return ErrDatabase
		}
		if builtin {
			return domain.ErrBuiltinTemplate
		}
		return execute(ctx, tx, `INSERT INTO ai_summary_templates(id,name,description,prompt,include_speaker_by_default,include_timestamp_by_default,builtin,created_at,updated_at) VALUES(?,?,?,?,?,?,0,?,?)
 ON CONFLICT(id) DO UPDATE SET name=excluded.name,description=excluded.description,prompt=excluded.prompt,include_speaker_by_default=excluded.include_speaker_by_default,include_timestamp_by_default=excluded.include_timestamp_by_default,updated_at=excluded.updated_at`, v.ID, v.Name, v.Description, v.Prompt, v.IncludeSpeaker, v.IncludeTimestamp, v.CreatedAt, v.UpdatedAt)
	})
}
func (s *Store) RemoveTemplate(ctx context.Context, id string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var builtin bool
		err := tx.QueryRowContext(ctx, `SELECT builtin FROM ai_summary_templates WHERE id=?`, id).Scan(&builtin)
		if err != nil && err != sql.ErrNoRows {
			return ErrDatabase
		}
		if builtin {
			return domain.ErrBuiltinTemplate
		}
		return execute(ctx, tx, `DELETE FROM ai_summary_templates WHERE id=?`, id)
	})
}
func (s *Store) Members(ctx context.Context) ([]domain.MeetingMember, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,department,sort_order,is_recorder,created_at,updated_at FROM meeting_members ORDER BY sort_order ASC,datetime(updated_at) DESC,updated_at DESC,name COLLATE NOCASE ASC`)
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	values := []domain.MeetingMember{}
	for rows.Next() {
		var r domain.MeetingMember
		if rows.Scan(&r.ID, &r.Name, &r.Department, &r.SortOrder, &r.IsRecorder, &r.CreatedAt, &r.UpdatedAt) != nil {
			return nil, ErrDatabase
		}
		values = append(values, r)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return values, nil
}
func saveMember(ctx context.Context, tx *sql.Tx, v domain.MeetingMember) error {
	if err := v.Validate(); err != nil {
		return err
	}
	if v.IsRecorder {
		if err := execute(ctx, tx, `UPDATE meeting_members SET is_recorder=0,updated_at=? WHERE id<>? AND is_recorder<>0`, v.UpdatedAt, v.ID); err != nil {
			return err
		}
	}
	return execute(ctx, tx, `INSERT INTO meeting_members(id,name,department,sort_order,is_recorder,created_at,updated_at) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(id) DO UPDATE SET name=excluded.name,department=excluded.department,sort_order=excluded.sort_order,is_recorder=excluded.is_recorder,updated_at=excluded.updated_at`, v.ID, strings.TrimSpace(v.Name), strings.TrimSpace(v.Department), v.SortOrder, v.IsRecorder, v.CreatedAt, v.UpdatedAt)
}
func (s *Store) SaveMember(ctx context.Context, v domain.MeetingMember) error {
	return s.transaction(ctx, func(tx *sql.Tx) error { return saveMember(ctx, tx, v) })
}
func (s *Store) RemoveMember(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM meeting_members WHERE id=?`, id)
	if err != nil {
		return ErrDatabase
	}
	return nil
}
func (s *Store) ImportMembers(ctx context.Context, values []domain.MeetingMember) (domain.MemberImportResult, error) {
	result := domain.MemberImportResult{}
	if err := domain.ValidateMemberImport(values); err != nil {
		return result, err
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id,name,created_at FROM meeting_members`)
		if err != nil {
			return ErrDatabase
		}
		type identity struct{ id, created string }
		existing := make(map[string]identity)
		for rows.Next() {
			var name string
			var row identity
			if rows.Scan(&row.id, &name, &row.created) != nil {
				rows.Close()
				return ErrDatabase
			}
			existing[strings.TrimSpace(name)] = row
		}
		rowErr := rows.Err()
		rows.Close()
		if rowErr != nil {
			return ErrDatabase
		}
		for _, value := range values {
			key := strings.TrimSpace(value.Name)
			if old, ok := existing[key]; ok {
				value.ID = old.id
				value.CreatedAt = old.created
				result.Updated++
			} else {
				result.Created++
			}
			if err := saveMember(ctx, tx, value); err != nil {
				return err
			}
			existing[key] = identity{value.ID, value.CreatedAt}
		}
		return nil
	})
	if err != nil {
		return domain.MemberImportResult{}, err
	}
	return result, nil
}
