package schema

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func BackupCredentialReferenced(ctx context.Context, database, reference string) (bool, error) {
	if ctx.Err() != nil || !filepath.IsAbs(database) || reference == "" {
		return false, ErrBackup
	}
	info, err := os.Lstat(database)
	if err != nil || !info.Mode().IsRegular() {
		return false, ErrBackup
	}
	entries, err := os.ReadDir(filepath.Dir(database))
	if err != nil {
		return false, ErrBackup
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return false, ErrBackup
		}
		name := entry.Name()
		if !strings.HasPrefix(name, filepath.Base(database)+".pre-") || !strings.HasSuffix(name, ".bak") || strings.ContainsAny(name, `/\:`) {
			continue
		}
		path := filepath.Join(filepath.Dir(database), name)
		backupInfo, err := os.Lstat(path)
		if err != nil || !backupInfo.Mode().IsRegular() || backupInfo.Size() == 0 || os.SameFile(info, backupInfo) {
			return false, ErrBackup
		}
		found, err := backupCredentialReferenced(ctx, path, reference)
		if err != nil || found {
			return found, err
		}
	}
	return false, nil
}

func backupCredentialReferenced(ctx context.Context, path, reference string) (found bool, readErr error) {
	db, err := sql.Open("sqlite", URI(path, url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "trusted_schema(0)", "busy_timeout(5000)"}}))
	if err != nil {
		return false, ErrBackup
	}
	db.SetMaxOpenConns(1)
	defer func() {
		if db.Close() != nil {
			found, readErr = false, ErrBackup
		}
	}()
	connection, err := db.Conn(ctx)
	if err != nil {
		return false, ErrBackup
	}
	defer func() {
		if connection.Close() != nil {
			found, readErr = false, ErrBackup
		}
	}()
	if _, err := connection.ExecContext(ctx, "BEGIN"); err != nil {
		return false, ErrBackup
	}
	defer connection.ExecContext(context.Background(), "ROLLBACK")
	var integrity string
	if connection.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity) != nil || integrity != "ok" {
		return false, ErrBackup
	}
	storedVersion, err := version(ctx, connection)
	if err != nil {
		return false, ErrBackup
	}
	references, err := snapshotCredentialReferences(ctx, connection, storedVersion)
	if err != nil {
		return false, err
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		return false, ErrBackup
	}
	for _, candidate := range references {
		if candidate == reference {
			return true, nil
		}
	}
	return false, nil
}

func snapshotCredentialReferences(ctx context.Context, connection *sql.Conn, storedVersion int) ([]string, error) {
	var references []string
	for _, column := range [][2]string{{"ai_model_configs", "api_key_ref"}, {"app_settings", "api_token_ref"}} {
		var present bool
		if connection.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pragma_table_info(?) WHERE name=?)", column[0], column[1]).Scan(&present) != nil || (!present && storedVersion >= 3) {
			return nil, ErrBackup
		}
		if !present {
			continue
		}
		rows, err := connection.QueryContext(ctx, "SELECT DISTINCT "+column[1]+" FROM "+column[0]+" WHERE "+column[1]+"<>''")
		if err != nil {
			return nil, ErrBackup
		}
		for rows.Next() {
			var reference string
			if rows.Scan(&reference) != nil {
				rows.Close()
				return nil, ErrBackup
			}
			references = append(references, reference)
		}
		readErr := rows.Err()
		if rows.Close() != nil || readErr != nil {
			return nil, ErrBackup
		}
	}
	return references, nil
}
