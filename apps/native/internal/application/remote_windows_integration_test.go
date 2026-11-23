package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/remote"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
)

type remoteWindowVault struct {
	mu     sync.Mutex
	values map[string]domain.Secret
}

func (vault *remoteWindowVault) Get(_ context.Context, reference string) (domain.Secret, error) {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	return vault.values[reference], nil
}
func (vault *remoteWindowVault) Set(_ context.Context, reference string, value domain.Secret) error {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	vault.values[reference] = value
	return nil
}
func (vault *remoteWindowVault) Delete(_ context.Context, reference string) error {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	delete(vault.values, reference)
	return nil
}

type remoteWindowLocal struct{}

func (remoteWindowLocal) ListJobs(context.Context) ([]domain.JobSummary, error) { return nil, nil }
func (remoteWindowLocal) ReadResult(_ context.Context, id string) (domain.Result, error) {
	return domain.Result{Job: domain.JobSummary{ID: id, Source: "local", Title: "Synthetic local"}}, nil
}
func (remoteWindowLocal) Retry(context.Context, string) error  { return application.ErrWindowScope }
func (remoteWindowLocal) Delete(context.Context, string) error { return application.ErrWindowScope }
func (remoteWindowLocal) RenameSpeaker(context.Context, string, string, string) error {
	return application.ErrWindowScope
}

func remoteWindowJob(title string) map[string]any {
	return map[string]any{
		"id": "same-id", "title": title, "createdAt": "2026-11-20", "overallStatus": "completed",
		"durationMinutes": 1, "lang": "zh", "enableSpeaker": true, "summaryTemplate": "default",
		"uploadStatus": "completed", "asrStatus": "completed", "summaryStatus": "idle",
		"sourceFiles": []any{}, "transcriptSegments": []any{}, "summaryRuns": []any{}, "summary": map[string]any{},
		"diarizationStatus": "completed",
		"speakerSegments":   []any{map[string]any{"id": "s1", "startMs": 0, "endMs": 1000, "text": "Synthetic transcript", "speaker": "Alice"}},
	}
}

func remoteWindowCapabilities() domain.RemoteCapabilities {
	return domain.RemoteCapabilities{Protocol: "liberty-meeting", ProtocolVersion: 1, JobSchemaVersion: 1, ServiceVersion: "synthetic", Operations: []string{"jobs.result.read", "transcript.speakers.rename"}}
}

func remoteWindowMeetings(t *testing.T, address string) (*application.Resources, *application.ModeMeetings) {
	t.Helper()
	ctx := context.Background()
	vault := &remoteWindowVault{values: map[string]domain.Secret{}}
	store, _, err := jobdb.OpenPrepared(ctx, filepath.Join(t.TempDir(), "remote-window.sqlite3"), vault, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	resources := application.NewResources(store, vault)
	t.Cleanup(resources.Close)
	settings, err := resources.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.BackendURL, settings.ProcessingMode = address, "remote"
	if _, _, err = resources.SaveSettings(ctx, settings, domain.CredentialUpdate{Action: domain.SetCredential, Value: domain.NewSecret("synthetic-window-token")}); err != nil {
		t.Fatal(err)
	}
	service := application.NewRemoteMeetings(resources, remote.Client{})
	t.Cleanup(service.Close)
	local := remoteWindowLocal{}
	return resources, application.NewModeMeetings(resources, local, service, local, local, local)
}

func TestRemoteWindowRejectsBackendRetarget(t *testing.T) {
	var serverBWrites atomic.Int32
	newServer := func(title string, writes *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Header.Get("Authorization") != "Bearer synthetic-window-token" {
				t.Error("synthetic authorization absent")
			}
			writer.Header().Set("Content-Type", "application/json")
			if request.URL.Path == "/api/capabilities" {
				_ = json.NewEncoder(writer).Encode(remoteWindowCapabilities())
				return
			}
			if request.Method == http.MethodPost && writes != nil {
				writes.Add(1)
			}
			_ = json.NewEncoder(writer).Encode(remoteWindowJob(title))
		}))
	}
	serverA, serverB := newServer("Synthetic A", nil), newServer("Synthetic B", &serverBWrites)
	defer serverA.Close()
	defer serverB.Close()
	resources, mode := remoteWindowMeetings(t, serverA.URL)
	scopes := application.NewWindowScopes(mode)
	scopes.EnableEditing(mode)
	grant, err := scopes.OpenFromMain(context.Background(), application.Workbench, "remote", "same-id")
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Close()
	before, err := grant.Result(context.Background(), "same-id")
	if err != nil || before.Job.Title != "Synthetic A" {
		t.Fatal("initial binding failed", err)
	}
	settings, err := resources.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	settings.BackendURL = serverB.URL
	if _, _, err = resources.SaveSettings(context.Background(), settings, domain.CredentialUpdate{Action: domain.KeepCredential}); err != nil {
		t.Fatal(err)
	}
	err = grant.RenameSpeaker(context.Background(), "Alice", "Bob")
	if !errors.Is(err, application.ErrWindowScope) || serverBWrites.Load() != 0 {
		t.Fatalf("old A grant accepted rename on B: error=%v B_POSTs=%d", err, serverBWrites.Load())
	}
}

func TestRemoteWindowSlowReadDoesNotBlockIndependentChildClose(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var blocking atomic.Bool
	var startOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/api/capabilities" {
			_ = json.NewEncoder(writer).Encode(remoteWindowCapabilities())
			return
		}
		if blocking.Load() && request.Method == http.MethodGet && request.URL.Path == "/api/jobs/same-id/result" {
			startOnce.Do(func() { close(started) })
			select {
			case <-release:
			case <-request.Context().Done():
				return
			}
		}
		_ = json.NewEncoder(writer).Encode(remoteWindowJob("Synthetic remote"))
	}))
	defer server.Close()
	defer unblock()
	_, mode := remoteWindowMeetings(t, server.URL)
	scopes := application.NewWindowScopes(mode)
	scopes.EnableEditing(mode)
	local, err := scopes.OpenFromMain(context.Background(), application.Workbench, "local", "job-1700000000000-1")
	if err != nil {
		t.Fatal(err)
	}
	child, err := local.OpenChild(application.MeetingNotes)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := scopes.OpenFromMain(context.Background(), application.Workbench, "remote", "same-id")
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Close()
	if _, err = child.Result(context.Background(), "job-1700000000000-1"); err != nil {
		t.Fatal("independent local child invalid", err)
	}
	blocking.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	renamed := make(chan error, 1)
	go func() { renamed <- grant.RenameSpeaker(ctx, "Alice", "Bob") }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("remote read never started")
	}
	closed := make(chan struct{})
	go func() { child.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(150 * time.Millisecond):
		t.Error("independent local child Close blocked behind remote fresh read before rename POST")
	}
	unblock()
	select {
	case err = <-renamed:
		if err != nil {
			t.Error(err)
		}
	case <-ctx.Done():
		t.Error("rename did not finish after release")
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Error("child Close did not finish after release")
	}
}

func TestRemoteWindowPersistedTokenRotationRevokesGrant(t *testing.T) {
	for _, token := range []string{"synthetic-rotated-token", "synthetic-window-token"} {
		t.Run(token, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				writer.Header().Set("Content-Type", "application/json")
				if request.URL.Path == "/api/capabilities" {
					_ = json.NewEncoder(writer).Encode(remoteWindowCapabilities())
					return
				}
				_ = json.NewEncoder(writer).Encode(remoteWindowJob("Synthetic remote"))
			}))
			defer server.Close()
			resources, mode := remoteWindowMeetings(t, server.URL)
			scopes := application.NewWindowScopes(mode)
			scopes.EnableEditing(mode)
			grant, err := scopes.OpenFromMain(context.Background(), application.Workbench, "remote", "same-id")
			if err != nil {
				t.Fatal(err)
			}
			defer grant.Close()
			before := requests.Load()
			settings, err := resources.Settings(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := resources.SaveSettings(context.Background(), settings, domain.CredentialUpdate{Action: domain.SetCredential, Value: domain.NewSecret(token)}); err != nil {
				t.Fatal(err)
			}
			if err := grant.RenameSpeaker(context.Background(), "Alice", "Bob"); !errors.Is(err, application.ErrWindowScope) || requests.Load() != before {
				t.Fatal("rotated credential retained stale window authority", err)
			}
			fresh, err := scopes.OpenFromMain(context.Background(), application.Workbench, "remote", "same-id")
			if err != nil {
				t.Fatal("explicitly reopened grant did not bind the rotated credential", err)
			}
			fresh.Close()
		})
	}
}
