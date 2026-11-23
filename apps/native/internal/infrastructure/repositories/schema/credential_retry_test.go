package schema

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"testing"

	"liberty.local/liberty/internal/domain"
)

func TestCredentialRetryMergesEarlierJournalRetainedByBackup(test *testing.T) {
	db, path := database(test)
	v2(test, db)
	ctx := context.Background()
	const earlierReference = "liberty:migration:synthetic-earlier-staged"
	vault := &memoryVault{values: map[string]domain.Secret{
		earlierReference: domain.NewSecret("synthetic-earlier-material"),
		"old-model":      domain.NewSecret("synthetic-old-model"),
		"old-shared":     domain.NewSecret("synthetic-old-shared"),
	}}
	connection, err := db.Conn(ctx)
	if err != nil {
		test.Fatal(err)
	}
	if err := saveJournal(ctx, connection, credentialJournal{Entries: []credentialEntry{{Reference: earlierReference}}}); err != nil {
		connection.Close()
		test.Fatal(err)
	}
	snapshot, err := backup(ctx, connection, path, 2, "pre-restore")
	connection.Close()
	if err != nil {
		test.Fatal(err)
	}
	snapshotDB, err := sql.Open("sqlite", URI(snapshot, url.Values{"mode": {"rw"}}))
	if err != nil {
		test.Fatal(err)
	}
	exec(test, snapshotDB, "UPDATE ai_model_configs SET api_key='',api_key_ref=? WHERE id='model'", earlierReference)
	if err := snapshotDB.Close(); err != nil {
		test.Fatal(err)
	}
	if _, err := Apply(ctx, db, path, vault); err != nil {
		test.Fatal(err)
	}
	var journal credentialJournal
	if json.Unmarshal([]byte(scalar(test, db, "SELECT value FROM app_meta WHERE key=?", journalKey)), &journal) != nil || !journal.Published || len(journal.Entries) != 3 {
		test.Fatal("migration retry replaced the earlier snapshot-retained journal")
	}
	retained := false
	for _, entry := range journal.Entries {
		retained = retained || entry.Reference == earlierReference
	}
	if !retained || vault.values[earlierReference].Empty() {
		test.Fatal("migration retry lost the earlier staged credential or retirement intent")
	}
	writes := vault.writes
	removeCredentialTestBackups(test, path)
	if err := db.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := sql.Open("sqlite", URI(path, url.Values{"mode": {"rw"}}))
	if err != nil {
		test.Fatal(err)
	}
	reopened.SetMaxOpenConns(1)
	defer reopened.Close()
	if _, err := Apply(ctx, reopened, path, vault); err != nil {
		test.Fatal(err)
	}
	if !vault.values[earlierReference].Empty() || scalar(test, reopened, "SELECT COUNT(*) FROM app_meta WHERE key=?", journalKey) != "0" {
		test.Fatal("reopen could not reclaim the earlier credential after backup removal")
	}
	modelReference := scalar(test, reopened, "SELECT api_key_ref FROM ai_model_configs WHERE id='model'")
	tokenReference := scalar(test, reopened, "SELECT api_token_ref FROM app_settings WHERE id=1")
	if vault.writes != writes || vault.values[modelReference].Reveal() != "synthetic-key" || vault.values[tokenReference].Reveal() != "synthetic-token" || vault.values["old-shared"].Empty() {
		test.Fatal("journal recovery restaged credentials or removed live material")
	}
}
