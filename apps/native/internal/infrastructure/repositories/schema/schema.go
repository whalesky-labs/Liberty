// Package schema upgrades the original SQLite format in ordered, atomic steps.
// Callers must hold the shared profile lock for the entire connection lifetime.
package schema

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"liberty.local/liberty/internal/domain"
)

var (
	ErrVersion   = errors.New("database_schema_unsupported")
	ErrMigration = errors.New("database_migration_failed")
	ErrBackup    = errors.New("database_backup_failed")
)

const handoverBackupKey = "native_handover_backup_v1"

type Credentials interface {
	Get(context.Context, string) (domain.Secret, error)
	Set(context.Context, string, domain.Secret) error
	Delete(context.Context, string) error
}

type Result struct {
	FromVersion int
	Version     int
	Backup      string
}

// Apply rejects future or malformed versions before enabling WAL or issuing
// business DDL. SQLite's backup API captures committed WAL pages as well.
func Apply(ctx context.Context, db *sql.DB, path string, vault Credentials) (Result, error) {
	return apply(ctx, db, path, vault, false, true)
}

func ApplyHandover(ctx context.Context, db *sql.DB, path string, vault Credentials, allowMigration bool) (Result, error) {
	return apply(ctx, db, path, vault, true, allowMigration)
}

func apply(ctx context.Context, db *sql.DB, path string, vault Credentials, handover, allowMigration bool) (Result, error) {
	c, err := db.Conn(ctx)
	if err != nil {
		return Result{}, ErrMigration
	}
	defer c.Close()
	v, err := version(ctx, c)
	r := Result{FromVersion: v, Version: v}
	if err != nil {
		return r, err
	}
	if !allowMigration && v != CurrentVersion {
		return r, ErrVersion
	}
	backupRequired := handover && v > 0
	markerRecorded := false
	if handover && v > 0 {
		backupPath, recorded, markerErr := readHandoverBackup(ctx, c, path, v)
		if markerErr != nil {
			return r, markerErr
		}
		markerRecorded = recorded
		backupRequired = !recorded
		r.Backup = backupPath
	}
	if handover && v == 0 {
		var tables int
		if c.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables) != nil {
			return r, ErrMigration
		}
		backupRequired = tables > 0
	}
	needsBackup := v > 0 && v < CurrentVersion && !(handover && markerRecorded)
	needsBackup = needsBackup || (handover && backupRequired)
	if needsBackup {
		purpose := "pre-migration"
		if handover {
			purpose = "pre-native"
		}
		r.Backup, err = backup(ctx, c, path, v, purpose)
		if err != nil {
			return r, err
		}
	}
	var marker string
	if handover && !markerRecorded {
		marker, err = handoverBackupValue(r.Backup, r.FromVersion, backupRequired)
		if err != nil {
			return r, ErrBackup
		}
	}
	if _, err := c.ExecContext(ctx, "PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL;"); err != nil {
		return r, ErrMigration
	}
	if marker != "" && v > 0 {
		if err := transaction(ctx, c, func() error { return recordHandoverBackup(ctx, c, marker) }); err != nil {
			return r, ErrBackup
		}
	}
	if v >= 2 {
		if err := recoverCredentials(ctx, c, vault); err != nil {
			return r, err
		}
	}
	for r.Version < CurrentVersion {
		next := r.Version + 1
		if next == 3 {
			err = migrateCredentials(ctx, c, vault)
		} else {
			err = transaction(ctx, c, func() error {
				current, e := version(ctx, c)
				if e != nil || current != next-1 {
					return ErrMigration
				}
				if e = applyStep(ctx, c, next); e != nil {
					return e
				}
				if next == 1 && marker != "" {
					if e = recordHandoverBackup(ctx, c, marker); e != nil {
						return e
					}
				}
				return recordVersion(ctx, c, next)
			})
		}
		if err != nil {
			// Credential publication may have committed v3 before cleanup failed.
			// Report the durable version, while leaving its cleanup journal intact.
			if committed, readErr := version(ctx, c); readErr == nil {
				r.Version = committed
			}
			return r, err
		}
		r.Version = next
	}
	err = transaction(ctx, c, func() error { return seedTemplates(ctx, c) })
	if err == nil && r.Backup != "" {
		r.Backup = filepath.Join(filepath.Dir(path), filepath.Base(r.Backup))
	}
	return r, err
}

func recordHandoverBackup(ctx context.Context, c *sql.Conn, marker string) error {
	if _, err := c.ExecContext(ctx, "INSERT INTO app_meta(key,value) VALUES(?,?)", handoverBackupKey, marker); err != nil {
		return ErrBackup
	}
	return nil
}

func handoverBackupValue(path string, version int, required bool) (string, error) {
	if !required {
		return "none", nil
	}
	file, err := os.Open(path)
	if err != nil {
		return "", ErrBackup
	}
	hasher := sha256.New()
	_, copyErr := io.Copy(hasher, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return "", ErrBackup
	}
	return fmt.Sprintf("%s|%d|%s", filepath.Base(path), version, hex.EncodeToString(hasher.Sum(nil))), nil
}

func readHandoverBackup(ctx context.Context, c *sql.Conn, path string, currentVersion int) (string, bool, error) {
	var marker string
	err := c.QueryRowContext(ctx, "SELECT value FROM app_meta WHERE key=?", handoverBackupKey).Scan(&marker)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, ErrMigration
	}
	if marker == "none" {
		return "", true, nil
	}
	parts := strings.Split(marker, "|")
	if len(parts) != 3 {
		return "", false, ErrBackup
	}
	filename, storedVersion, digest := parts[0], parts[1], parts[2]
	backupVersion, parseErr := strconv.Atoi(storedVersion)
	if parseErr != nil || backupVersion < 0 || backupVersion > currentVersion || filepath.Base(filename) != filename || !strings.HasPrefix(filename, fmt.Sprintf("%s.pre-native-v%d-", filepath.Base(path), backupVersion)) || !strings.HasSuffix(filename, ".bak") || len(digest) != sha256.Size*2 {
		return "", false, ErrBackup
	}
	backupPath := filepath.Join(filepath.Dir(path), filename)
	info, err := os.Lstat(backupPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return "", false, ErrBackup
	}
	file, err := os.Open(backupPath)
	if err != nil {
		return "", false, ErrBackup
	}
	hasher := sha256.New()
	_, copyErr := io.Copy(hasher, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || hex.EncodeToString(hasher.Sum(nil)) != digest {
		return "", false, ErrBackup
	}
	if err := verifyBackup(ctx, backupPath, backupVersion); err != nil {
		return "", false, err
	}
	return backupPath, true, nil
}

func verifyBackup(ctx context.Context, path string, expectedVersion int) error {
	db, err := sql.Open("sqlite", URI(path, url.Values{"mode": {"ro"}}))
	if err != nil {
		return ErrBackup
	}
	var integrity string
	if db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity) != nil || integrity != "ok" {
		db.Close()
		return ErrBackup
	}
	connection, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return ErrBackup
	}
	versionNumber, versionErr := version(ctx, connection)
	connectionErr := connection.Close()
	databaseErr := db.Close()
	if versionErr != nil || connectionErr != nil || databaseErr != nil || versionNumber != expectedVersion {
		return ErrBackup
	}
	return nil
}

func version(ctx context.Context, c *sql.Conn) (int, error) {
	var exists int
	if err := c.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='app_meta'").Scan(&exists); err != nil {
		return 0, ErrMigration
	}
	if exists == 0 {
		return 0, nil
	}
	var text string
	err := c.QueryRowContext(ctx, "SELECT value FROM app_meta WHERE key='schema_version'").Scan(&text)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, ErrMigration
	}
	v, err := strconv.Atoi(text)
	if err != nil || v < 0 || v > CurrentVersion {
		return 0, ErrVersion
	}
	return v, nil
}

func transaction(ctx context.Context, c *sql.Conn, fn func() error) error {
	if _, err := c.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		return ErrMigration
	}
	defer c.ExecContext(context.Background(), "ROLLBACK") // also releases a cancelled transaction
	if err := fn(); err != nil {
		return err
	}
	if _, err := c.ExecContext(ctx, "COMMIT"); err != nil {
		return ErrMigration
	}
	return nil
}

func recordVersion(ctx context.Context, c *sql.Conn, v int) error {
	_, err := c.ExecContext(ctx, `INSERT INTO app_meta(key,value) VALUES('schema_version',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.Itoa(v))
	if err != nil {
		return ErrMigration
	}
	return nil
}

// Only generated, single ALTER statements use this path. Batch migrations such
// as v9 deliberately keep the original transaction and fail on schema drift.
var alterColumn = regexp.MustCompile(`^ALTER TABLE ([a-z_]+) ADD COLUMN ([a-z_]+) `)

func executeBlocks(ctx context.Context, c *sql.Conn, blocks []string, optionalColumns bool) error {
	for _, statement := range blocks {
		if parts := alterColumn.FindStringSubmatch(statement); optionalColumns && parts != nil {
			var count int
			if c.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, parts[1], parts[2]).Scan(&count) != nil {
				return ErrMigration
			}
			if count != 0 {
				continue
			}
		}
		if _, err := c.ExecContext(ctx, statement); err != nil {
			return ErrMigration
		}
	}
	return nil
}

func applyStep(ctx context.Context, c *sql.Conn, v int) error {
	switch v {
	case 1:
		return executeBlocks(ctx, c, baselineSQL, false)
	case 2:
		if err := executeBlocks(ctx, c, baselineSQL, false); err != nil {
			return err
		}
		if err := executeBlocks(ctx, c, settingsUpgradeSQL, true); err != nil {
			return err
		}
		if err := once(ctx, c, "runtime_sources_migrated", func() error { return executeBlocks(ctx, c, runtimeSourcesSQL, false) }); err != nil {
			return err
		}
		return once(ctx, c, "pet_leveling_255_migrated", func() error { return migratePet(ctx, c) })
	case 4:
		return executeBlocks(ctx, c, settingsRevisionSQL, true)
	case 5:
		return executeBlocks(ctx, c, jobRunsSQL, false)
	case 6:
		return executeBlocks(ctx, c, workTablesSQL, false)
	case 7:
		return executeBlocks(ctx, c, summaryRunsSQL, true)
	case 8:
		return executeBlocks(ctx, c, jobDeletionSQL, false)
	case 9:
		return executeBlocks(ctx, c, runnerProtocolSQL, false)
	case 10:
		return executeBlocks(ctx, c, credentialCleanupSQL, false)
	case 11:
		return executeBlocks(ctx, c, liveSessionsSQL, false)
	case 12:
		return executeBlocks(ctx, c, liveAutoLanguageSQL, false)
	default:
		return ErrVersion
	}
}

func once(ctx context.Context, c *sql.Conn, key string, fn func() error) error {
	var marker string
	err := c.QueryRowContext(ctx, "SELECT value FROM app_meta WHERE key=?", key).Scan(&marker)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return ErrMigration
	}
	return fn()
}

func migratePet(ctx context.Context, c *sql.Conn) error {
	var level, experience int64
	err := c.QueryRowContext(ctx, "SELECT level,experience FROM pet_profile WHERE id='default-pet'").Scan(&level, &experience)
	if err != nil && err != sql.ErrNoRows {
		return ErrMigration
	}
	if err == nil {
		var calculated domain.DashboardCompanion
		calculated.SetExperience(experience)
		level = min(max(level, calculated.Level), 255)
		if level > calculated.Level {
			experience = 0
			for i := int64(1); i < level; i++ {
				experience += domain.PetLevelRequirement(i)
			}
		}
		stage := "first_meet"
		for _, s := range []struct {
			level int64
			key   string
		}{{11, "familiar"}, {31, "steady_companion"}, {61, "grow_together"}, {101, "tacit_bond"}, {141, "deep_bond"}, {181, "long_company"}, {221, "bond_forever"}} {
			if level >= s.level {
				stage = s.key
			}
		}
		if _, err = c.ExecContext(ctx, "UPDATE pet_profile SET level=?,experience=?,stage=?,updated_at=? WHERE id='default-pet'", level, experience, stage, domain.PersistedUTC(time.Now())); err != nil {
			return ErrMigration
		}
	}
	_, err = c.ExecContext(ctx, "INSERT OR REPLACE INTO app_meta(key,value) VALUES('pet_leveling_255_migrated','2026-05-21')")
	if err != nil {
		return ErrMigration
	}
	return nil
}

func seedTemplates(ctx context.Context, c *sql.Conn) error {
	var templates []domain.SummaryTemplate
	if json.Unmarshal([]byte(builtinTemplatesJSON), &templates) != nil {
		return ErrMigration
	}
	for _, t := range templates {
		if _, err := c.ExecContext(ctx, `INSERT OR REPLACE INTO ai_summary_templates(id,name,description,prompt,include_speaker_by_default,include_timestamp_by_default,builtin,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, t.ID, t.Name, t.Description, t.Prompt, t.IncludeSpeaker, t.IncludeTimestamp, t.Builtin, t.CreatedAt, t.UpdatedAt); err != nil {
			return ErrMigration
		}
	}
	return nil
}
