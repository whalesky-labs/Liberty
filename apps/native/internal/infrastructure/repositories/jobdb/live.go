package jobdb

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"liberty.local/liberty/internal/domain"
)

const liveSessionSQL = `SELECT id,state,source_language,target_language,translate_enabled,auto_detect_language,model_config_id,error_code,created_at,updated_at,
 (SELECT COUNT(*) FROM live_segments p WHERE p.session_id=live_sessions.id) FROM live_sessions`

func scanLive(row rowScanner) (domain.LiveSession, error) {
	var v domain.LiveSession
	err := row.Scan(&v.ID, &v.State, &v.SourceLanguage, &v.TargetLanguage, &v.Translate, &v.AutoDetect, &v.ModelID, &v.ErrorCode, &v.CreatedAt, &v.UpdatedAt, &v.SegmentCount)
	if errors.Is(err, sql.ErrNoRows) {
		return v, domain.ErrResourceMissing
	}
	if err != nil {
		return v, ErrDatabase
	}
	return v, nil
}

func (s *Store) CreateLive(ctx context.Context, v domain.LiveSession) error {
	if v.ID == "" || len(v.ID) > 128 || v.State != "preparing" {
		return domain.ErrLiveInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrDatabase
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO live_sessions(id,state,source_language,target_language,translate_enabled,auto_detect_language,model_config_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, v.ID, v.State, v.SourceLanguage, v.TargetLanguage, v.Translate, v.AutoDetect, v.ModelID, v.CreatedAt, v.UpdatedAt); err != nil {
		return ErrDatabase
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO live_session_events(session_id,state,created_at) VALUES(?,?,?)`, v.ID, v.State, v.CreatedAt); err != nil {
		return ErrDatabase
	}
	if tx.Commit() != nil {
		return ErrDatabase
	}
	return nil
}

func liveStateAllowed(from, to string) bool {
	switch to {
	case "listening":
		return from == "preparing" || from == "paused"
	case "paused":
		return from == "listening"
	case "stopping":
		return from == "preparing" || from == "listening" || from == "paused"
	case "completed":
		return from == "stopping"
	case "failed", "interrupted":
		return domain.LiveActive(from)
	}
	return false
}

func (s *Store) SetLiveState(ctx context.Context, id, state, code, stamp string) error {
	if len(code) > 128 || strings.ContainsAny(code, "\r\n") {
		return domain.ErrLiveInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrDatabase
	}
	defer tx.Rollback()
	var previous string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM live_sessions WHERE id=?`, id).Scan(&previous); err != nil {
		return ErrDatabase
	}
	if previous == state {
		return nil
	}
	if !liveStateAllowed(previous, state) {
		return domain.ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE live_sessions SET state=?,error_code=?,updated_at=? WHERE id=?`, state, code, stamp, id); err != nil {
		return ErrDatabase
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO live_session_events(session_id,state,error_code,created_at) VALUES(?,?,?,?)`, id, state, code, stamp); err != nil {
		return ErrDatabase
	}
	if tx.Commit() != nil {
		return ErrDatabase
	}
	return nil
}

func (s *Store) SaveLiveSegment(ctx context.Context, v domain.LiveSegment) error {
	if v.Validate() != nil || (v.TranslationState != "pending" && v.TranslationState != "disabled") {
		return domain.ErrLiveInput
	}
	r, err := s.db.ExecContext(ctx, `INSERT INTO live_segments(session_id,segment_id,revision,start_ms,end_ms,original_text,translation_state)
 SELECT ?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM live_sessions WHERE id=? AND state IN ('listening','paused','stopping'))
 ON CONFLICT(session_id,segment_id) DO UPDATE SET revision=excluded.revision,start_ms=excluded.start_ms,end_ms=excluded.end_ms,original_text=excluded.original_text,translated_text='',translation_state=excluded.translation_state,translation_ms=0 WHERE excluded.revision>live_segments.revision`, v.SessionID, v.ID, v.Revision, v.StartMS, v.EndMS, v.Text, v.TranslationState, v.SessionID)
	if err != nil {
		return ErrDatabase
	}
	n, err := r.RowsAffected()
	if err != nil {
		return ErrDatabase
	}
	if n != 1 {
		return domain.ErrConflict
	}
	return nil
}

func (s *Store) SaveLiveTranslation(ctx context.Context, v domain.LiveSegment) error {
	if v.Validate() != nil || (v.TranslationState != "completed" && v.TranslationState != "failed") || (v.TranslationState == "completed" && strings.TrimSpace(v.Translation) == "") || v.TranslationMS < 0 {
		return domain.ErrLiveInput
	}
	r, err := s.db.ExecContext(ctx, `UPDATE live_segments SET translated_text=?,translation_state=?,translation_ms=? WHERE session_id=? AND segment_id=? AND revision=? AND original_text=? AND translation_state!='disabled'`, v.Translation, v.TranslationState, v.TranslationMS, v.SessionID, v.ID, v.Revision, v.Text)
	if err != nil {
		return ErrDatabase
	}
	n, err := r.RowsAffected()
	if err != nil {
		return ErrDatabase
	}
	if n != 1 {
		return domain.ErrConflict
	}
	return nil
}

func (s *Store) EditLiveSegment(ctx context.Context, v domain.LiveSegment) error {
	if v.Validate() != nil || (v.TranslationState != "pending" && v.TranslationState != "completed" && v.TranslationState != "disabled") {
		return domain.ErrLiveInput
	}
	r, err := s.db.ExecContext(ctx, `UPDATE live_segments SET revision=revision+1,original_text=?,translated_text=?,translation_state=?,translation_ms=0 WHERE session_id=? AND segment_id=? AND revision=? AND EXISTS(SELECT 1 FROM live_sessions WHERE id=? AND state NOT IN ('preparing','listening','paused','stopping'))`, v.Text, v.Translation, v.TranslationState, v.SessionID, v.ID, v.Revision, v.SessionID)
	if err != nil {
		return ErrDatabase
	}
	n, err := r.RowsAffected()
	if err != nil {
		return ErrDatabase
	}
	if n != 1 {
		return domain.ErrConflict
	}
	return nil
}

func (s *Store) LiveSession(ctx context.Context, id string) (domain.LiveSession, error) {
	return scanLive(s.db.QueryRowContext(ctx, liveSessionSQL+` WHERE id=?`, id))
}
func (s *Store) LiveSessions(ctx context.Context, offset int) ([]domain.LiveSession, error) {
	if offset < 0 {
		return nil, domain.ErrLiveInput
	}
	rows, err := s.db.QueryContext(ctx, liveSessionSQL+` ORDER BY created_at DESC,id DESC LIMIT 20 OFFSET ?`, offset)
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	values := []domain.LiveSession{}
	for rows.Next() {
		v, e := scanLive(rows)
		if e != nil {
			return nil, e
		}
		values = append(values, v)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return values, nil
}

const liveSegmentColumns = `session_id,segment_id,revision,start_ms,end_ms,original_text,translated_text,translation_state,translation_ms`

func scanLiveSegment(row rowScanner) (domain.LiveSegment, error) {
	var v domain.LiveSegment
	if row.Scan(&v.SessionID, &v.ID, &v.Revision, &v.StartMS, &v.EndMS, &v.Text, &v.Translation, &v.TranslationState, &v.TranslationMS) != nil {
		return v, ErrDatabase
	}
	if v.Validate() != nil {
		return v, ErrDatabase
	}
	return v, nil
}
func (s *Store) liveSegments(ctx context.Context, id string, after int64, limit int, pending bool) ([]domain.LiveSegment, error) {
	if after < 0 || limit < 1 || limit > 500 {
		return nil, domain.ErrLiveInput
	}
	query := `SELECT ` + liveSegmentColumns + ` FROM live_segments WHERE session_id=? AND segment_id>?`
	if pending {
		query += ` AND translation_state IN ('pending','failed')`
	}
	query += ` ORDER BY segment_id LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, id, after, limit)
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	values := []domain.LiveSegment{}
	for rows.Next() {
		v, e := scanLiveSegment(rows)
		if e != nil {
			return nil, e
		}
		values = append(values, v)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return values, nil
}
func (s *Store) LiveSegments(ctx context.Context, id string, after int64, limit int) ([]domain.LiveSegment, error) {
	return s.liveSegments(ctx, id, after, limit, false)
}
func (s *Store) PendingLiveSegments(ctx context.Context, id string, after int64, limit int) ([]domain.LiveSegment, error) {
	return s.liveSegments(ctx, id, after, limit, true)
}

func (s *Store) RecoverLive(ctx context.Context, stamp string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrDatabase
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO live_session_events(session_id,state,error_code,created_at) SELECT id,'interrupted','live_session_interrupted',? FROM live_sessions WHERE state IN ('preparing','listening','paused','stopping')`, stamp); err != nil {
		return ErrDatabase
	}
	if _, err = tx.ExecContext(ctx, `UPDATE live_sessions SET state='interrupted',error_code='live_session_interrupted',updated_at=? WHERE state IN ('preparing','listening','paused','stopping')`, stamp); err != nil {
		return ErrDatabase
	}
	if tx.Commit() != nil {
		return ErrDatabase
	}
	return nil
}
func (s *Store) DeleteLive(ctx context.Context, id string) error {
	r, err := s.db.ExecContext(ctx, `DELETE FROM live_sessions WHERE id=? AND state NOT IN ('preparing','listening','paused','stopping')`, id)
	if err != nil {
		return ErrDatabase
	}
	n, err := r.RowsAffected()
	if err != nil {
		return ErrDatabase
	}
	if n != 1 {
		return domain.ErrConflict
	}
	return nil
}
