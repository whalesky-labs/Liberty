package jobdb

import (
	"context"
	"liberty.local/liberty/internal/domain"
	"strconv"
)

func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	var value string
	if s.db.QueryRowContext(ctx, "SELECT value FROM app_meta WHERE key='schema_version'").Scan(&value) != nil {
		return 0, domain.ErrDiagnostics
	}
	version, err := strconv.ParseInt(value, 10, 64)
	if err != nil || version < 0 {
		return 0, domain.ErrDiagnostics
	}
	return version, nil
}
