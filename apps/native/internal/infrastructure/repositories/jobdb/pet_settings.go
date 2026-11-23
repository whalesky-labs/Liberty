package jobdb

import (
	"context"
	"database/sql"
	"liberty.local/liberty/internal/domain"
)

func (s *Store) PetProfile(ctx context.Context, now string) (domain.PetProfile, error) {
	var out domain.PetProfile
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if e := ensurePet(ctx, tx, now); e != nil {
			return e
		}
		var e error
		out, e = loadPetProfile(ctx, tx)
		if e == nil {
			stage := domain.PetStage(out.Stage)
			out.LevelSnapshot.CurrentStage = out.Stage
			out.LevelSnapshot.CurrentStageLabelZh = stage.LabelZh
			out.LevelSnapshot.CurrentStageLabelEn = stage.LabelEn
		}
		return e
	})
	return out, err
}
func (s *Store) PetSettings(ctx context.Context, now string) (domain.PetSettings, error) {
	var out domain.PetSettings
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if e := ensurePet(ctx, tx, now); e != nil {
			return e
		}
		return tx.QueryRowContext(ctx, `SELECT pet_id,desktop_enabled,always_on_top,muted,focus_mode_enabled,proactive_level,last_window_x,last_window_y,updated_at FROM pet_settings WHERE pet_id='default-pet'`).Scan(&out.PetID, &out.DesktopEnabled, &out.AlwaysOnTop, &out.Muted, &out.FocusModeEnabled, &out.ProactiveLevel, &out.LastWindowX, &out.LastWindowY, &out.UpdatedAt)
	})
	if err != nil {
		return out, ErrDatabase
	}
	return out, nil
}
func (s *Store) SavePetSettings(ctx context.Context, value domain.PetSettings, now string) (domain.PetSettings, error) {
	value.PetID = domain.PetID
	value.ProactiveLevel = min(max(value.ProactiveLevel, 0), 3)
	value.UpdatedAt = now
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if e := ensurePet(ctx, tx, now); e != nil {
			return e
		}
		return tx.QueryRowContext(ctx, `UPDATE pet_settings SET desktop_enabled=?,always_on_top=?,muted=?,focus_mode_enabled=?,proactive_level=?,updated_at=? WHERE pet_id=? RETURNING last_window_x,last_window_y`, value.DesktopEnabled, value.AlwaysOnTop, value.Muted, value.FocusModeEnabled, value.ProactiveLevel, now, value.PetID).Scan(&value.LastWindowX, &value.LastWindowY)
	})
	if err != nil {
		return domain.PetSettings{}, ErrDatabase
	}
	return value, err
}

func (s *Store) SavePetPosition(ctx context.Context, x, y int, now string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := ensurePet(ctx, tx, now); err != nil {
			return err
		}
		return execute(ctx, tx, `UPDATE pet_settings SET last_window_x=?,last_window_y=?,updated_at=? WHERE pet_id='default-pet'`, x, y, now)
	})
}
func (s *Store) PetEvents(ctx context.Context, limit int) ([]domain.PetEventLedgerEntry, error) {
	limit = min(max(limit, 1), 100)
	rows, err := s.db.QueryContext(ctx, `SELECT id,pet_id,event_type,event_source,event_value,event_time,metadata FROM pet_event_ledger WHERE pet_id='default-pet' ORDER BY datetime(event_time) DESC,event_time DESC LIMIT ?`, limit)
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	out := []domain.PetEventLedgerEntry{}
	for rows.Next() {
		var v domain.PetEventLedgerEntry
		if rows.Scan(&v.ID, &v.PetID, &v.EventType, &v.EventSource, &v.EventValue, &v.EventTime, &v.Metadata) != nil {
			return nil, ErrDatabase
		}
		out = append(out, v)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return out, nil
}
func (s *Store) PetCosmetics(ctx context.Context) ([]domain.PetCosmeticUnlock, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,pet_id,cosmetic_type,cosmetic_key,unlocked_at,equipped FROM pet_cosmetic_unlocks WHERE pet_id='default-pet' ORDER BY datetime(unlocked_at) DESC,unlocked_at DESC`)
	if err != nil {
		return nil, ErrDatabase
	}
	defer rows.Close()
	out := []domain.PetCosmeticUnlock{}
	for rows.Next() {
		var v domain.PetCosmeticUnlock
		if rows.Scan(&v.ID, &v.PetID, &v.CosmeticType, &v.CosmeticKey, &v.UnlockedAt, &v.Equipped) != nil {
			return nil, ErrDatabase
		}
		out = append(out, v)
	}
	if rows.Err() != nil {
		return nil, ErrDatabase
	}
	return out, nil
}
func (s *Store) ensurePetOperation(ctx context.Context, now string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if e := ensurePet(ctx, tx, now); e != nil {
			return e
		}
		return ensurePetStore(ctx, tx, now)
	})
}
func (s *Store) EnsurePet(ctx context.Context, now string) error {
	return s.ensurePetOperation(ctx, now)
}
