package schema

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"liberty.local/liberty/internal/domain"
)

const maxRestoreCredentialJournalSize = 64 << 20

type restoreCredentialJournal struct {
	Version    int      `json:"version"`
	Database   string   `json:"database"`
	References []string `json:"references"`
}

type restoreCredentialRecord struct {
	path    string
	data    []byte
	info    os.FileInfo
	journal restoreCredentialJournal
}

func restoreCredentialReferences(ctx context.Context, connection *sql.Conn, storedVersion int) ([]string, error) {
	references, err := snapshotCredentialReferences(ctx, connection, storedVersion)
	if err != nil {
		return nil, ErrRestore
	}
	var present bool
	if connection.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='credential_cleanup_intents')").Scan(&present) != nil || (!present && storedVersion >= 10) {
		return nil, ErrRestore
	}
	if present {
		rows, err := connection.QueryContext(ctx, "SELECT credential_reference FROM credential_cleanup_intents")
		if err != nil {
			return nil, ErrRestore
		}
		for rows.Next() {
			var reference string
			if rows.Scan(&reference) != nil || reference == "" {
				rows.Close()
				return nil, ErrRestore
			}
			references = append(references, reference)
		}
		readErr := rows.Err()
		if rows.Close() != nil || readErr != nil {
			return nil, ErrRestore
		}
	}
	if connection.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='app_meta')").Scan(&present) != nil {
		return nil, ErrRestore
	}
	if present {
		var text string
		err := connection.QueryRowContext(ctx, "SELECT value FROM app_meta WHERE key=?", journalKey).Scan(&text)
		if err != nil && err != sql.ErrNoRows {
			return nil, ErrRestore
		}
		if err == nil {
			var journal credentialJournal
			if json.Unmarshal([]byte(text), &journal) != nil {
				return nil, ErrRestore
			}
			for _, entry := range journal.Entries {
				if entry.Reference == "" {
					return nil, ErrRestore
				}
				references = append(references, entry.Reference)
				if entry.Previous != "" {
					references = append(references, entry.Previous)
				}
			}
		}
	}
	unique := make(map[string]bool, len(references))
	for _, reference := range references {
		if !validRestoreCredentialReference(reference) {
			return nil, ErrRestore
		}
		unique[reference] = true
	}
	references = make([]string, 0, len(unique))
	for reference := range unique {
		references = append(references, reference)
	}
	sort.Strings(references)
	return references, nil
}

func validRestoreCredentialReference(reference string) bool {
	return reference != "" && len(reference) < 2048 && utf8.ValidString(reference) && !strings.ContainsAny(reference, "\x00\r\n")
}

func writeRestoreCredentialJournal(ctx context.Context, database string, references []string) error {
	if ctx.Err() != nil || !filepath.IsAbs(database) {
		return ErrRestore
	}
	for _, reference := range references {
		if !validRestoreCredentialReference(reference) {
			return ErrRestore
		}
	}
	journal := restoreCredentialJournal{Version: 1, Database: filepath.Base(database), References: references}
	data, err := json.Marshal(journal)
	if err != nil || len(data) > maxRestoreCredentialJournalSize {
		return ErrRestore
	}
	if _, err := decodeRestoreCredentialJournal(data, journal.Database); err != nil {
		return err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return ErrRestore
	}
	final := filepath.Join(filepath.Dir(database), journal.Database+".restore-credentials-"+hex.EncodeToString(random[:])+".json")
	if _, err := os.Lstat(final); !os.IsNotExist(err) {
		return ErrRestore
	}
	file, err := os.CreateTemp(filepath.Dir(database), "."+journal.Database+".restore-credentials-*.tmp")
	if err != nil {
		return ErrRestore
	}
	defer os.Remove(file.Name())
	written, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || written != len(data) || syncErr != nil || closeErr != nil || ctx.Err() != nil {
		return ErrRestore
	}
	if publishRestoreCredentialJournal(file.Name(), final) != nil {
		return ErrRestore
	}
	return nil
}

func decodeRestoreCredentialJournal(data []byte, database string) (restoreCredentialJournal, error) {
	var journal restoreCredentialJournal
	if !utf8.Valid(data) {
		return journal, ErrRestore
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return journal, ErrRestore
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return journal, ErrRestore
		}
		seen[key] = true
		switch key {
		case "version":
			err = decoder.Decode(&journal.Version)
		case "database":
			err = decoder.Decode(&journal.Database)
		case "references":
			err = decoder.Decode(&journal.References)
		default:
			return journal, ErrRestore
		}
		if err != nil {
			return journal, ErrRestore
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') || len(seen) != 3 || journal.Version != 1 || journal.Database != database || journal.References == nil {
		return journal, ErrRestore
	}
	if _, err := decoder.Token(); err != io.EOF {
		return journal, ErrRestore
	}
	for index, reference := range journal.References {
		if !validRestoreCredentialReference(reference) || (index > 0 && reference <= journal.References[index-1]) {
			return journal, ErrRestore
		}
	}
	return journal, nil
}

func readRestoreCredentialRecord(path, database string) (restoreCredentialRecord, error) {
	var record restoreCredentialRecord
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > maxRestoreCredentialJournalSize {
		return record, ErrRestore
	}
	file, err := os.Open(path)
	if err != nil {
		return record, ErrRestore
	}
	opened, statErr := file.Stat()
	data, readErr := io.ReadAll(io.LimitReader(file, maxRestoreCredentialJournalSize+1))
	closeErr := file.Close()
	if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || readErr != nil || closeErr != nil || len(data) > maxRestoreCredentialJournalSize {
		return record, ErrRestore
	}
	journal, err := decodeRestoreCredentialJournal(data, database)
	if err != nil {
		return record, err
	}
	return restoreCredentialRecord{path: path, info: info, data: data, journal: journal}, nil
}

func readRestoreCredentialJournals(ctx context.Context, database string) ([]restoreCredentialRecord, error) {
	if ctx.Err() != nil || !filepath.IsAbs(database) {
		return nil, ErrRestore
	}
	info, err := os.Lstat(database)
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrRestore
	}
	entries, err := os.ReadDir(filepath.Dir(database))
	if err != nil {
		return nil, ErrRestore
	}
	var records []restoreCredentialRecord
	prefix := filepath.Base(database) + ".restore-credentials-"
	for _, entry := range entries {
		if ctx.Err() != nil {
			return nil, ErrRestore
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		identifier := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".json")
		decoded, err := hex.DecodeString(identifier)
		if err != nil || len(decoded) != 16 || hex.EncodeToString(decoded) != identifier || name != prefix+identifier+".json" || strings.ContainsAny(name, `/\:`) {
			return nil, ErrRestore
		}
		record, err := readRestoreCredentialRecord(filepath.Join(filepath.Dir(database), name), filepath.Base(database))
		if err != nil || os.SameFile(info, record.info) {
			return nil, ErrRestore
		}
		records = append(records, record)
	}
	return records, nil
}

func importRestoreCredentialJournals(ctx context.Context, connection *sql.Conn, database string) ([]restoreCredentialRecord, error) {
	records, err := readRestoreCredentialJournals(ctx, database)
	if err != nil || len(records) == 0 {
		return nil, err
	}
	var actual string
	if connection.QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name='main'").Scan(&actual) != nil {
		return nil, ErrRestore
	}
	expectedInfo, expectedErr := os.Lstat(database)
	actualInfo, actualErr := os.Lstat(actual)
	if expectedErr != nil || actualErr != nil || !actualInfo.Mode().IsRegular() || !os.SameFile(expectedInfo, actualInfo) {
		return nil, ErrRestore
	}
	storedVersion, err := version(ctx, connection)
	if err != nil {
		return nil, ErrRestore
	}
	if storedVersion < 10 {
		return nil, nil
	}
	var synchronous int
	if connection.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous) != nil {
		return nil, ErrRestore
	}
	if _, err := connection.ExecContext(ctx, "PRAGMA synchronous=FULL"); err != nil {
		return nil, ErrRestore
	}
	err = transaction(ctx, connection, func() error {
		now := domain.PersistedUTC(time.Now())
		for _, record := range records {
			for _, reference := range record.journal.References {
				digest := sha256.Sum256([]byte(reference))
				_, err := connection.ExecContext(ctx, `INSERT INTO credential_cleanup_intents(id,model_id,credential_reference,operation,attempt,last_error,created_at,updated_at)
 SELECT ?, '', ?, 'retire', 0, NULL, ?, ?
 WHERE NOT EXISTS(SELECT 1 FROM ai_model_configs WHERE api_key_ref=? UNION ALL SELECT 1 FROM app_settings WHERE api_token_ref=?)
 ON CONFLICT(credential_reference) DO NOTHING`, "restore-"+hex.EncodeToString(digest[:]), reference, now, now, reference, reference)
				if err != nil {
					return ErrRestore
				}
			}
		}
		return nil
	})
	if err == nil {
		err = checkpointRestoreWAL(ctx, connection)
	}
	_, resetErr := connection.ExecContext(context.Background(), "PRAGMA synchronous="+strconv.Itoa(synchronous))
	if err != nil || resetErr != nil {
		return nil, ErrRestore
	}
	return records, nil
}

func checkpointRestoreWAL(ctx context.Context, connection *sql.Conn) error {
	var busy, frames, checkpointed int
	if connection.QueryRowContext(ctx, "PRAGMA main.wal_checkpoint(TRUNCATE)").Scan(&busy, &frames, &checkpointed) != nil || busy != 0 || frames != checkpointed {
		return ErrRestore
	}
	return nil
}

func syncRestoredDatabase(database string) error {
	file, err := os.OpenFile(database, os.O_RDWR, 0)
	if err != nil {
		return ErrRestore
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil || closeErr != nil {
		return ErrRestore
	}
	return nil
}

func removeRestoreCredentialJournals(ctx context.Context, records []restoreCredentialRecord) error {
	for _, record := range records {
		current, err := readRestoreCredentialRecord(record.path, record.journal.Database)
		if ctx.Err() != nil || err != nil || !os.SameFile(record.info, current.info) || !bytes.Equal(record.data, current.data) {
			return ErrRestore
		}
	}
	for _, record := range records {
		if os.Remove(record.path) != nil {
			return ErrRestore
		}
	}
	return nil
}

func RecoverRestoreCredentials(ctx context.Context, db *sql.DB, database string) error {
	connection, err := db.Conn(ctx)
	if err != nil {
		return ErrRestore
	}
	records, err := importRestoreCredentialJournals(ctx, connection, database)
	closeErr := connection.Close()
	if err != nil || closeErr != nil {
		return ErrRestore
	}
	if len(records) != 0 && syncRestoredDatabase(database) != nil {
		return ErrRestore
	}
	return removeRestoreCredentialJournals(ctx, records)
}
