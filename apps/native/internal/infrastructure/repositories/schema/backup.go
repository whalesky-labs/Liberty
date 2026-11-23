package schema

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"modernc.org/sqlite"
)

// URI correctly escapes spaces, #, ?, Unicode and Windows drive letters.
func URI(path string, query url.Values) string {
	name := filepath.ToSlash(path)
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	u := url.URL{Scheme: "file", Path: name, RawQuery: query.Encode()}
	return u.String()
}

func backup(ctx context.Context, c *sql.Conn, path string, v int, purpose string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", ErrBackup
	}
	f, err := os.CreateTemp(filepath.Dir(path), fmt.Sprintf("%s.%s-v%d-*.bak", filepath.Base(path), purpose, v))
	if err != nil {
		return "", ErrBackup
	}
	name := f.Name()
	if f.Close() != nil {
		os.Remove(name)
		return "", ErrBackup
	}
	success := false
	defer func() {
		if !success {
			os.Remove(name)
		}
	}()
	err = c.Raw(func(raw any) error {
		driver, ok := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return ErrBackup
		}
		b, e := driver.NewBackup(URI(name, url.Values{"mode": {"rw"}}))
		if e != nil {
			return ErrBackup
		}
		for {
			if e = ctx.Err(); e != nil {
				b.Finish()
				return e
			}
			more, e := b.Step(256)
			if e != nil {
				b.Finish()
				return ErrBackup
			}
			if !more {
				break
			}
		}
		if b.Finish() != nil {
			return ErrBackup
		}
		return nil
	})
	if err != nil {
		return "", ErrBackup
	}
	// Verify the artifact itself, not merely the backup API's return code.
	if err := verifyBackup(ctx, name, v); err != nil {
		return "", ErrBackup
	}
	file, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return "", ErrBackup
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil || closeErr != nil {
		return "", ErrBackup
	}
	if directory, err := os.Open(filepath.Dir(name)); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	success = true
	return name, nil
}
