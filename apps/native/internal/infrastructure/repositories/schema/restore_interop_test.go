package schema

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRestoreCredentialJournalProtectsLiveReferencesAndRetainsIntentMetadata(test *testing.T) {
	database := filepath.Join(test.TempDir(), "liberty.sqlite3")
	connection, err := sql.Open("sqlite", URI(database, url.Values{"mode": {"rwc"}}))
	if err != nil {
		test.Fatal(err)
	}
	defer connection.Close()
	connection.SetMaxOpenConns(1)
	ctx := context.Background()
	if _, err := Apply(ctx, connection, database, nil); err != nil {
		test.Fatal(err)
	}
	exec(test, connection, `INSERT INTO ai_model_configs(id,name,base_url,api_key,api_key_ref,model,created_at,updated_at)
 VALUES('synthetic-live-model','Synthetic live model','https://example.invalid/v1','','synthetic-live-reference','synthetic','before','before')`)
	exec(test, connection, `INSERT INTO credential_cleanup_intents(id,model_id,credential_reference,operation,attempt,last_error,created_at,updated_at)
 VALUES('synthetic-existing-id','synthetic-existing-owner','synthetic-existing-reference','rollback',7,'synthetic-existing-error','before','after')`)
	if err := writeRestoreCredentialJournal(ctx, database, []string{"synthetic-existing-reference", "synthetic-live-reference", "synthetic-retired-reference"}); err != nil {
		test.Fatal(err)
	}
	if scalar(test, connection, "SELECT COUNT(*) FROM credential_cleanup_intents") != "1" || len(restoreCredentialRecords(test, database)) != 1 {
		test.Fatal("pending journal changed before recovery")
	}
	if err := connection.Close(); err != nil {
		test.Fatal(err)
	}
	connection, err = sql.Open("sqlite", URI(database, url.Values{"mode": {"rw"}}))
	if err != nil {
		test.Fatal(err)
	}
	defer connection.Close()
	connection.SetMaxOpenConns(1)
	if err := RecoverRestoreCredentials(ctx, connection, database); err != nil {
		test.Fatal(err)
	}
	if len(restoreCredentialRecords(test, database)) != 0 || scalar(test, connection, "SELECT COUNT(*) FROM credential_cleanup_intents") != "2" {
		test.Fatal("recovery did not consume the pending journal into exactly two intents")
	}
	if scalar(test, connection, "SELECT COUNT(*) FROM credential_cleanup_intents WHERE credential_reference='synthetic-live-reference'") != "0" || scalar(test, connection, "SELECT api_key_ref FROM ai_model_configs WHERE id='synthetic-live-model'") != "synthetic-live-reference" {
		test.Fatal("recovery retired or replaced the live credential reference")
	}
	if scalar(test, connection, `SELECT id||'|'||model_id||'|'||operation||'|'||attempt||'|'||last_error||'|'||created_at||'|'||updated_at
 FROM credential_cleanup_intents WHERE credential_reference='synthetic-existing-reference'`) != "synthetic-existing-id|synthetic-existing-owner|rollback|7|synthetic-existing-error|before|after" {
		test.Fatal("recovery replaced existing cleanup intent metadata")
	}
	if scalar(test, connection, "SELECT operation||'|'||attempt FROM credential_cleanup_intents WHERE credential_reference='synthetic-retired-reference'") != "retire|0" || scalar(test, connection, "PRAGMA integrity_check") != "ok" {
		test.Fatal("recovery did not durably retire the post-snapshot reference")
	}
	before := snapshot(test, connection)
	if err := RecoverRestoreCredentials(context.Background(), connection, database); err != nil {
		test.Fatal(err)
	}
	if !reflect.DeepEqual(before, snapshot(test, connection)) {
		test.Fatal("recovery replay changed the imported intents")
	}
}
