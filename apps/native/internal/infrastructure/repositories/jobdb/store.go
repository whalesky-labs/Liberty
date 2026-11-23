// Package jobdb writes the original schema through transactional, lease-fenced
// operations. Profile selection and migration must be explicit.
package jobdb

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/repositories/legacydb"
	"liberty.local/liberty/internal/infrastructure/repositories/schema"
	_ "modernc.org/sqlite"
)

var ErrDatabase = errors.New("database_write_failed")

type Store struct {
	db       *sql.DB
	path     string
	owner    *profile.Lock
	once     sync.Once
	closeErr error
}

// Open acquires shared profile ownership BEFORE opening SQLite in rw mode.
// Only an existing schema-v10 file is accepted. No implicit create, migration,
// default-profile discovery, or credential access occurs here.
func Open(ctx context.Context, database string) (*Store, error) {
	if !filepath.IsAbs(database) {
		return nil, ErrDatabase
	}
	info, err := os.Lstat(database)
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrDatabase
	}
	owner, err := profile.Acquire(filepath.Dir(database))
	if err != nil {
		return nil, err
	}
	path := filepath.Join(owner.Directory(), filepath.Base(database))
	reader, err := legacydb.Open(ctx, path)
	if err != nil {
		owner.Close()
		return nil, err
	}
	if reader.Close() != nil {
		owner.Close()
		return nil, ErrDatabase
	}
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	u.RawQuery = url.Values{"mode": {"rw"}, "_txlock": {"immediate"}, "_pragma": {"foreign_keys(1)", "busy_timeout(5000)", "synchronous(FULL)"}}.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		owner.Close()
		return nil, ErrDatabase
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: path, owner: owner}
	var version string
	if db.QueryRowContext(ctx, "SELECT value FROM app_meta WHERE key='schema_version'").Scan(&version) != nil || version != legacydb.SchemaVersion {
		s.Close()
		return nil, legacydb.ErrSchema
	}
	if err := schema.RecoverRestoreCredentials(ctx, db, path); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// OpenPrepared explicitly initializes or upgrades a selected profile. It never
// discovers the user's default directory. Ownership precedes file creation,
// migration, credential access and the writable connection.
func OpenPrepared(ctx context.Context, database string, vault schema.Credentials, create bool) (*Store, schema.Result, error) {
	return openPrepared(ctx, database, vault, create, false)
}

func OpenHandover(ctx context.Context, database string, vault schema.Credentials, prepare bool) (*Store, schema.Result, error) {
	return openPrepared(ctx, database, vault, prepare, true)
}

func openPrepared(ctx context.Context, database string, vault schema.Credentials, create, handover bool) (*Store, schema.Result, error) {
	var result schema.Result
	if !filepath.IsAbs(database) {
		return nil, result, ErrDatabase
	}
	owner, err := profile.Acquire(filepath.Dir(database))
	if err != nil {
		return nil, result, err
	}
	success := false
	defer func() {
		if !success {
			owner.Close()
		}
	}()
	path := filepath.Join(owner.Directory(), filepath.Base(database))
	info, err := os.Lstat(path)
	if os.IsNotExist(err) && create {
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return nil, result, ErrDatabase
		}
		if f.Close() != nil {
			return nil, result, ErrDatabase
		}
	} else if err != nil || !info.Mode().IsRegular() {
		return nil, result, ErrDatabase
	}
	// No write pragmas until Apply has inspected the stored version.
	db, err := sql.Open("sqlite", schema.URI(path, url.Values{"mode": {"rw"}, "_txlock": {"immediate"}, "_pragma": {"foreign_keys(1)", "busy_timeout(5000)", "synchronous(FULL)"}}))
	if err != nil {
		return nil, result, ErrDatabase
	}
	db.SetMaxOpenConns(1)
	if handover {
		result, err = schema.ApplyHandover(ctx, db, path, vault, create)
	} else {
		result, err = schema.Apply(ctx, db, path, vault)
	}
	if err != nil {
		db.Close()
		return nil, result, err
	}
	if err := schema.RecoverRestoreCredentials(ctx, db, path); err != nil {
		db.Close()
		return nil, result, err
	}
	store := &Store{db: db, path: path, owner: owner}
	success = true
	return store, result, nil
}

func (s *Store) Close() error {
	s.once.Do(func() {
		// sql.DB.Close waits for active operations before releasing ownership.
		if s.db.Close() != nil {
			s.closeErr = ErrDatabase
		}
		if s.owner.Close() != nil {
			s.closeErr = profile.ErrLock
		}
	})
	return s.closeErr
}

func (s *Store) transaction(ctx context.Context, apply func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrDatabase
	}
	defer tx.Rollback()
	if err := apply(tx); err != nil {
		return err
	}
	if tx.Commit() != nil {
		return ErrDatabase
	}
	return nil
}
