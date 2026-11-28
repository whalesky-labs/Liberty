package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
	"liberty.local/liberty/internal/infrastructure/repositories/legacydb"
)

type testVault struct{ values map[string]domain.Secret }

func (v *testVault) Get(_ context.Context, ref string) (domain.Secret, error) {
	return v.values[ref], nil
}
func (v *testVault) Set(_ context.Context, ref string, secret domain.Secret) error {
	v.values[ref] = secret
	return nil
}
func (v *testVault) Delete(_ context.Context, ref string) error { delete(v.values, ref); return nil }

type noLocalMutations struct{ t *testing.T }

func (n noLocalMutations) Retry(context.Context, string) error {
	n.t.Fatal("remote retry reached local")
	return nil
}
func (n noLocalMutations) Delete(context.Context, string) error {
	n.t.Fatal("remote delete reached local")
	return nil
}
func (n noLocalMutations) RenameSpeaker(context.Context, string, string, string) error {
	n.t.Fatal("remote rename reached local")
	return nil
}

func TestRemotePersistedSettingsToHTTPWithNoLocalFallback(t *testing.T) {
	ctx := context.Background()
	vault := &testVault{map[string]domain.Secret{}}
	path := filepath.Join(t.TempDir(), "liberty.sqlite3")
	store, _, err := jobdb.OpenPrepared(ctx, path, vault, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if version, err := store.SchemaVersion(ctx); err != nil || version != 13 {
		t.Fatal("diagnostic schema projection", version, err)
	}
	reader, err := legacydb.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	resources := application.NewResources(store, vault)
	defer resources.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-persisted-token" {
			t.Error("persisted credential missing")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/capabilities":
			_ = json.NewEncoder(w).Encode(syntheticCapabilities())
		case "/api/jobs":
			_ = json.NewEncoder(w).Encode([]any{syntheticJob("remote-id")})
		default:
			if r.Method == "DELETE" {
				w.WriteHeader(204)
			} else {
				_ = json.NewEncoder(w).Encode(syntheticJob("remote-id"))
			}
		}
	}))
	defer server.Close()
	settings, err := resources.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.BackendURL = server.URL
	settings.ProcessingMode = "remote"
	settings, _, err = resources.SaveSettings(ctx, settings, domain.CredentialUpdate{Action: domain.SetCredential, Value: domain.NewSecret("synthetic-persisted-token")})
	if err != nil || !settings.TokenConfigured {
		t.Fatal(err)
	}
	record, err := store.Settings(ctx)
	if err != nil || record.Reference == "" || record.LegacyPresent || len(vault.values) != 1 {
		t.Fatal("unsafe credential publication", err)
	}
	transport := application.NewRemoteMeetings(resources, Client{})
	localMutations := noLocalMutations{t}
	routed := application.NewModeMeetings(resources, reader, transport, localMutations, localMutations, localMutations)
	page, err := routed.Queue(ctx, domain.QueueQuery{Source: "local"})
	if err != nil || page.Total != 1 || page.Items[0].ID != "remote-id" {
		t.Fatal("remote list", page, err)
	}
	dashboard := application.NewModeDashboard(reader, routed)
	overview, err := dashboard.Dashboard(ctx, "all", time.Now())
	if err != nil || overview.Source != "remote" || overview.Metrics.TotalJobs != 1 || overview.Resources != (domain.DashboardResources{}) || overview.Companion != nil {
		t.Fatal("remote dashboard used local data", overview, err)
	}
	if _, err = routed.Result(ctx, "remote-id"); err != nil {
		t.Fatal(err)
	}
	if routed.Retry(ctx, "remote-id") != nil || routed.RenameSpeaker(ctx, "remote-id", "Alice", "Bob") != nil || routed.Delete(ctx, "remote-id") != nil {
		t.Fatal("remote mutations failed")
	}
	localJobs, err := reader.ListJobs(ctx)
	if err != nil || len(localJobs) != 0 {
		t.Fatal("remote requests changed local jobs")
	}
	settings.ProcessingMode = "local"
	settings, _, err = resources.SaveSettings(ctx, settings, domain.CredentialUpdate{Action: domain.KeepCredential})
	if err != nil {
		t.Fatal(err)
	}
	page, err = routed.Queue(ctx, domain.QueueQuery{Source: "remote"})
	if err != nil || page.Total != 0 {
		t.Fatal("local mode did not use local database", err)
	}
	overview, err = dashboard.Dashboard(ctx, "all", time.Now())
	if err != nil || overview.Source != "local" || overview.Metrics.TotalJobs != 0 || overview.Resources.Templates != 5 {
		t.Fatal("local dashboard did not restore local resources", overview, err)
	}
	bound, err := routed.ResultForSource(ctx, "remote", "remote-id")
	if err != nil || bound.Job.Source != "remote" {
		t.Fatal("mode switch redirected bound window", err)
	}
	settings.ProcessingMode = "remote"
	_, _, err = resources.SaveSettings(ctx, settings, domain.CredentialUpdate{Action: domain.ClearCredential})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = routed.Queue(ctx, domain.QueueQuery{Source: "local"}); err == nil {
		t.Fatal("missing remote token silently fell back")
	}
}
