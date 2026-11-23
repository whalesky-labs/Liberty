package jobdb

import (
	"context"
	"database/sql"
	"liberty.local/liberty/internal/application"
)

func (s *Store) JobDefaults(ctx context.Context) (application.JobDefaults, error) {
	value := application.JobDefaults{Hotwords: "SeACo-Paraformer, FunASR, 会议纪要", SummaryTemplate: "表格版会议纪要"}
	err := s.db.QueryRowContext(ctx, `SELECT default_hotwords,summary_template FROM app_settings WHERE id=1`).Scan(&value.Hotwords, &value.SummaryTemplate)
	if err == sql.ErrNoRows {
		return value, nil
	}
	if err != nil {
		return value, ErrDatabase
	}
	return value, nil
}

// ProcessingMode is a public metadata read. It never opens the system
// credential store, which matters because queue polling is frequent.
func (s *Store) ProcessingMode(ctx context.Context) (string, error) {
	var mode string
	err := s.db.QueryRowContext(ctx, `SELECT processing_mode FROM app_settings WHERE id=1`).Scan(&mode)
	if err == sql.ErrNoRows {
		return "local", nil
	}
	if err != nil {
		return "", ErrDatabase
	}
	if mode == "remote" {
		return "remote", nil
	}
	return "local", nil
}
