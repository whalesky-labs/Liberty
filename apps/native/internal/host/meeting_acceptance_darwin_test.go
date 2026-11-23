//go:build meeting_acceptance

package host

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/credentials"
	"liberty.local/liberty/internal/infrastructure/managed"
	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
)

func TestMeetingWindowAcceptance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()
	cache := os.Getenv("LIBERTY_NATIVE_RUNTIME_ACCEPTANCE_DIR")
	marker, err := os.ReadFile(filepath.Join(cache, ".native-runtime-acceptance"))
	if !filepath.IsAbs(cache) || err != nil || string(marker) != "isolated-runtime-acceptance-v1\n" {
		t.Fatal("run native:runtime:acceptance first")
	}
	lock, err := profile.Acquire(cache)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	cache = lock.Directory()
	marker, err = os.ReadFile(filepath.Join(cache, ".native-runtime-acceptance"))
	if err != nil || string(marker) != "isolated-runtime-acceptance-v1\n" {
		t.Fatal("isolated runtime acceptance ownership changed")
	}
	installer, err := managed.NewInstaller(cache, managed.CurrentPlatform())
	if err != nil {
		t.Fatal(err)
	}
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Ready runtime generations belong to this profile root. The GUI must use
	// the same root, but may never reuse or replace an existing default database.
	database := filepath.Join(cache, "liberty.sqlite3")
	before, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal("inspect isolated acceptance profile", err)
	}
	existing := make(map[string]bool, len(before))
	for _, entry := range before {
		name := entry.Name()
		existing[name] = true
		if name == "liberty.sqlite3" || name == "liberty.sqlite3-wal" || name == "liberty.sqlite3-shm" || name == "liberty.sqlite3-journal" || strings.HasPrefix(name, "liberty.sqlite3.restore-credentials-") {
			t.Fatal("meeting acceptance requires an unused default database")
		}
	}
	created, err := os.OpenFile(database, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal("allocate isolated meeting database", err)
	}
	createdInfo, err := created.Stat()
	if err != nil {
		created.Close()
		t.Fatal("identify isolated meeting database", err)
	}
	t.Cleanup(func() {
		// All connection/GUI defers have completed before Cleanup runs. A new
		// owner or replaced database prevents cleanup rather than risking data.
		owner, err := profile.Acquire(cache)
		if err != nil {
			t.Error("meeting database cleanup could not acquire ownership", err)
			return
		}
		defer owner.Close()
		current, err := os.Lstat(database)
		if err != nil || !current.Mode().IsRegular() || !os.SameFile(createdInfo, current) {
			t.Error("meeting database cleanup refused changed database identity")
			return
		}
		entries, err := os.ReadDir(cache)
		if err != nil {
			t.Error("inspect meeting database cleanup", err)
			return
		}
		for _, entry := range entries {
			name := entry.Name()
			if existing[name] || name == "liberty.sqlite3" {
				continue
			}
			sidecar := name == "liberty.sqlite3-wal" || name == "liberty.sqlite3-shm" || name == "liberty.sqlite3-journal"
			backup := strings.HasPrefix(name, "liberty.sqlite3.pre-") && strings.HasSuffix(name, ".bak")
			if !sidecar && !backup {
				continue
			}
			file := filepath.Join(cache, name)
			info, err := os.Lstat(file)
			if err != nil || !info.Mode().IsRegular() {
				t.Error("meeting database cleanup refused non-regular attachment")
				return
			}
			if err := os.Remove(file); err != nil {
				t.Error("remove meeting database attachment", err)
				return
			}
		}
		if err := os.Remove(database); err != nil {
			t.Error("remove isolated meeting database", err)
		}
	})
	if err := created.Close(); err != nil {
		t.Fatal("close isolated meeting database allocation", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal("transfer meeting profile ownership", err)
	}
	store, _, err := jobdb.OpenPrepared(ctx, database, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	artifacts := map[string]domain.RuntimeArtifact{}
	for _, component := range []string{"ffmpeg", "python", "model"} {
		var artifact domain.RuntimeArtifact
		if component == "model" {
			artifact, err = installer.ValidateModels(ctx, 1, artifacts["python"].Path, nil)
		} else {
			artifact, err = installer.InstallTool(ctx, component, 1, "official", nil)
		}
		if err != nil {
			t.Fatalf("validate %s: %v", component, err)
		}
		artifacts[component] = artifact
		state, err := store.BeginRuntime(ctx, managed.CurrentPlatform(), component, "managed", "validating", "validating", false)
		if err != nil {
			t.Fatal(err)
		}
		state.Artifact, state.Availability = &artifact, "ready"
		state.Operation.Kind, state.Operation.Phase = "idle", "ready"
		if err := store.UpdateRuntime(ctx, managed.CurrentPlatform(), state); err != nil {
			t.Fatal(err)
		}
	}
	voice := filepath.Join(directory, "speech.aiff")
	media := filepath.Join(directory, "acceptance.wav")
	if err := exec.CommandContext(ctx, "/usr/bin/say", "-v", "Tingting", "-o", voice, "这是一个会议转写测试。今天我们讨论项目计划，明天检查会议纪要。谢谢大家。").Run(); err != nil {
		t.Fatal("synthesize recording", err)
	}
	if err := exec.CommandContext(ctx, artifacts["ffmpeg"].Path, "-nostdin", "-i", voice, "-ar", "16000", "-ac", "1", media).Run(); err != nil {
		t.Fatal("convert recording", err)
	}
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	secret := domain.NewSecret(hex.EncodeToString(nonce[:]))
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/chat/completions" || request.Header.Get("Authorization") != "Bearer "+secret.Reveal() {
			writer.WriteHeader(http.StatusForbidden)
			return
		}
		var payload struct {
			Model    string
			Messages []struct{ Role, Content string }
		}
		if json.NewDecoder(io.LimitReader(request.Body, 2<<20)).Decode(&payload) != nil || payload.Model != "fixture" || len(payload.Messages) < 2 || !strings.Contains(payload.Messages[len(payload.Messages)-1].Content, "会议") {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"title\":\"Acceptance meeting\",\"overview\":\"Synthetic project plan verified\",\"topics\":[\"Project plan\"],\"decisions\":[],\"actionItems\":[],\"risks\":[],\"followUps\":[],\"speakerReports\":[],\"globalSummary\":[]}"}}]}`)
	}))
	defer server.Close()
	vault := credentials.NewSystem()
	resources := application.NewResources(store, vault)
	model, _, err := resources.SaveModel(ctx, domain.AIModel{Name: "Local acceptance", BaseURL: server.URL + "/v1", Model: "fixture", Enabled: true, IsDefault: true}, domain.CredentialUpdate{Action: domain.SetCredential, Value: secret})
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Model(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := vault.Delete(context.Background(), record.Reference); err != nil {
			t.Error("remove temporary credential", err)
		}
	}()
	resources.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	export := filepath.Join(directory, "acceptance.md")
	t.Logf("Synthetic input: %s\nExport the full result to %s and close the preview.", media, export)
	binary := os.Getenv("LIBERTY_NATIVE_GUI_BINARY")
	if !filepath.IsAbs(binary) {
		t.Fatal("explicit preview binary required")
	}
	if err := exec.CommandContext(ctx, binary, "--profile-directory", filepath.Dir(database), "--writable").Run(); err != nil {
		t.Fatal("preview exited", err)
	}
	reopened, err := Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	jobs, err := reopened.Meetings.ListJobs(ctx)
	if err != nil || len(jobs) != 1 || jobs[0].OverallStatus != "completed" {
		t.Fatal("expected one completed job", err)
	}
	result, err := reopened.Meetings.Result(ctx, jobs[0].ID)
	if err != nil || len(result.SummaryRuns) != 1 || result.SummaryRuns[0].Status != "completed" || result.SummaryRuns[0].MinutesPayload == nil || requests.Load() != 1 {
		t.Fatal("summary publication mismatch", err)
	}
	data, err := os.ReadFile(export)
	if err != nil || !strings.Contains(string(data), "会议") || !strings.Contains(string(data), "测试") || !strings.Contains(string(data), "Synthetic project plan verified") {
		t.Fatal("export did not contain recognized speech and returned summary")
	}
	events, err := reopened.Pet.Events(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, entry := range events {
		if entry.EventType == "workflow" {
			counts[entry.EventSource]++
		}
	}
	for _, event := range []string{"job_created", "transcription_started", "transcription_completed", "ai_summary_completed", "export_completed"} {
		if counts[event] != 1 {
			t.Fatal("unexpected reward count", event, counts[event])
		}
	}
}
