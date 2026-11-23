package jobdb

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

func TestTemplateCopyIsImmediatelyDurableAndFailedCopyRetainsScope(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "liberty.sqlite3")
	store, _, err := OpenPrepared(ctx, path, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := application.NewResources(store, nil)
	defer service.Close()
	values, err := service.Templates(ctx)
	if err != nil || len(values) != 5 {
		t.Fatal("built-in templates unavailable", err)
	}
	source := values[0]
	grant, draft, err := service.OpenEditor(ctx, application.Templates, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Close()
	if _, err := store.db.Exec(`CREATE TRIGGER reject_synthetic_copy BEFORE INSERT ON ai_summary_templates BEGIN SELECT RAISE(ABORT, 'synthetic copy failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := grant.Duplicate(ctx, draft, " - Copy"); err == nil {
		t.Fatal("failed insert reported successful duplication")
	}
	values, err = service.Templates(ctx)
	if err != nil || len(values) != 5 {
		t.Fatal("failed insert changed the template list", err)
	}
	if _, err := store.db.Exec(`DROP TRIGGER reject_synthetic_copy`); err != nil {
		t.Fatal(err)
	}
	copy, err := grant.Duplicate(ctx, draft, " - Copy")
	if err != nil || copy.ID() == "" || copy.ID() == source.ID || copy.Template.Builtin {
		t.Fatal("retry did not persist a separate template", err)
	}
	if _, _, err := grant.Save(ctx, draft, domain.CredentialUpdate{}); !errors.Is(err, application.ErrWindowScope) {
		t.Fatal("copy retained access to source", err)
	}
	grant.Close()
	service.Close()
	store.Close()
	reopened, _, err := OpenPrepared(ctx, path, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	values, err = reopened.Templates(ctx)
	if err != nil || len(values) != 6 {
		t.Fatal("copy required another save before becoming durable", err)
	}
	foundOriginal, foundCopy := false, false
	for _, value := range values {
		if value.ID == source.ID {
			foundOriginal = value == source
		}
		if value.ID == copy.ID() {
			foundCopy = value == copy.Template
		}
	}
	if !foundOriginal || !foundCopy {
		t.Fatal("reopen lost the copy or modified the built-in original")
	}
}
