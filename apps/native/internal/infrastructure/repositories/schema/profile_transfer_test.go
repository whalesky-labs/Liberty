package schema

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"liberty.local/liberty/internal/domain"
)

func TestTransferredEarlySchemaUsesExecutorWithoutDeletingSourceCredentialAccounts(t *testing.T) {
	db, path := database(t)
	v2(t, db)
	exec(t, db, "DELETE FROM ai_model_configs; UPDATE app_settings SET api_token='',api_token_ref=''")
	exec(t, db, `INSERT INTO ai_model_configs(id,name,base_url,api_key,api_key_ref,model,enabled,is_default,created_at,updated_at)
VALUES('synthetic-model','Synthetic model','https://example.invalid','synthetic-private-value','legacy-reference','synthetic',1,1,'now','now')`)
	// A copied, unpublished source journal is source ownership, never an
	// instruction to delete one of its credentials in the imported profile.
	exec(t, db, "INSERT INTO app_meta(key,value) VALUES(?,?)", journalKey, `{"published":false,"entries":[{"reference":"legacy-pending","previous":"legacy-reference"}]}`)
	vault := &memoryVault{values: map[string]domain.Secret{
		"legacy-reference": domain.NewSecret("synthetic-original-value"),
		"legacy-pending":   domain.NewSecret("synthetic-pending-value"),
	}}
	var intents []string
	live, err := PrepareTransferredProfile(t.Context(), path, vault, func(reference string) error {
		if !strings.HasPrefix(reference, "liberty:native-import:") {
			t.Fatal("source-style reference escaped ownership journal")
		}
		if _, exists := vault.values[reference]; exists {
			t.Fatal("intent must be durable before vault write")
		}
		intents = append(intents, reference)
		return nil
	})
	if err != nil || len(live) != 1 || len(intents) != 2 {
		t.Fatal("early-schema snapshot was not migrated and rebound", err, "live", len(live), "intents", len(intents), "writes", vault.writes, "version", scalar(t, db, "SELECT value FROM app_meta WHERE key='schema_version'"))
	}
	if vault.deletes != 0 || vault.values["legacy-reference"].Empty() || vault.values["legacy-pending"].Empty() {
		t.Fatal("source credentials were reclaimed during import")
	}
	if scalar(t, db, "SELECT api_key FROM ai_model_configs") != "" || scalar(t, db, "SELECT api_key_ref FROM ai_model_configs") != live[0] || vault.values[live[0]].Reveal() != "synthetic-private-value" {
		t.Fatal("migrated secret was not rebound")
	}
	if scalar(t, db, "SELECT value FROM app_meta WHERE key='schema_version'") != "13" {
		t.Fatal("normal versioned executor did not upgrade snapshot")
	}
	if err := VerifyTransferredProfile(context.Background(), path, vault); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || bytes.Contains(data, []byte("synthetic-private-value")) {
		t.Fatal("published snapshot retains a plaintext credential", err)
	}
}
