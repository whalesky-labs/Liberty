//go:build compatibility

package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/managed"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
)

func TestMain(m *testing.M) {
	if os.Getenv("LIBERTY_HOST_FIXTURE_CHILD") == "asr" {
		if len(os.Args) == 6 && os.Args[1] == "-I" && filepath.Base(os.Args[2]) == "runtime_validate.py" &&
			os.Args[3] == "--python-version" && os.Args[4] == os.Getenv("LIBERTY_HOST_EXPECTED_PYTHON_VERSION") &&
			os.Args[5] == "--interpreter-only" {
			os.Exit(0)
		}
		var expected map[string]string
		if json.Unmarshal([]byte(os.Getenv("LIBERTY_HOST_EXPECTED_MODELS")), &expected) != nil || len(expected) != 5 {
			os.Exit(10)
		}
		for key, value := range expected {
			if os.Getenv(key) != value {
				os.Exit(10)
			}
		}
		dir := ""
		for j, arg := range os.Args {
			if arg == "--job-dir" && j+1 < len(os.Args) {
				dir = os.Args[j+1]
			}
		}
		if dir == "" {
			os.Exit(3)
		}
		fmt.Fprintln(os.Stdout, `{"protocolVersion":2,"type":"diagnostic","level":"info","code":"synthetic_start","message":"synthetic start"}`)
		data := []byte(`{"protocolVersion":2,"asrBackend":"funasr","diarizationRequested":false,"diarizationStatus":"disabled","warnings":[],"durationMinutes":1,"transcriptSegments":[{"id":"s1","startMs":0,"endMs":1000,"text":"Synthetic host result"}],"speakerSegments":[]}`)
		if os.WriteFile(filepath.Join(dir, "result.json"), data, 0600) != nil {
			os.Exit(4)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestWriteHostSubmissionSchedulerResultRetryDeleteAndOwnership(t *testing.T) {
	ctx := context.Background()
	source := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if source == "" {
		t.Fatal("run pnpm native:compat")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "liberty.sqlite3")
	data, err := os.ReadFile(filepath.Join(source, "liberty.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	if os.WriteFile(path, data, 0600) != nil {
		t.Fatal("database fixture")
	}
	store, err := jobdb.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := managed.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// This is a synthetic Runner/process integration test, not a runtime
	// installation. Use a named desktop target even on the Linux CI host and
	// bind its synthetic generation to the real managed input identity.
	const fixturePlatform = "windows-x64"
	installer, err := managed.NewInstaller(dir, fixturePlatform)
	if err != nil {
		t.Fatal(err)
	}
	models := filepath.Join(installer.PlatformRoot(), "components", "model", "generations", "1", "models")
	if os.MkdirAll(models, 0700) != nil {
		t.Fatal("model fixture")
	}
	resources, err := managed.MaterializeResources(dir)
	if err != nil {
		t.Fatal(err)
	}
	pinsData, err := os.ReadFile(filepath.Join(resources, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pins []managed.ModelPin
	if json.Unmarshal(pinsData, &pins) != nil || len(pins) != 4 {
		t.Fatal("pinned model fixture unavailable")
	}
	keys := map[string]string{"model": "FUNASR_MODEL", "vad": "FUNASR_VAD_MODEL", "punc": "FUNASR_PUNC_MODEL", "spk": "FUNASR_SPK_MODEL"}
	expected := map[string]string{"FUNASR_PROFILE": manifest.ModelProfile}
	for _, pin := range pins {
		key, ok := keys[pin.Role]
		if !ok {
			t.Fatal("unknown pinned model role")
		}
		model := filepath.Join(models, "modelscope", "models", filepath.FromSlash(pin.ID))
		if err := os.MkdirAll(model, 0700); err != nil {
			t.Fatal("pinned model directory unavailable")
		}
		expected[key] = model
	}
	environment, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIBERTY_HOST_EXPECTED_MODELS", string(environment))
	t.Setenv("LIBERTY_HOST_EXPECTED_PYTHON_VERSION", manifest.PythonVersion)
	for key := range expected {
		t.Setenv(key, "unselected-model-alias")
	}
	for _, component := range []string{"python", "ffmpeg", "model"} {
		s, err := store.BeginRuntime(ctx, fixturePlatform, component, "managed", "validating", "validating", false)
		if err != nil {
			t.Fatal(err)
		}
		generation := filepath.Join(installer.PlatformRoot(), "components", component, "generations", "1")
		if err := os.MkdirAll(generation, 0700); err != nil {
			t.Fatal(err)
		}
		p, version := filepath.Join(generation, "synthetic-"+component), manifest.RuntimeVersion
		if component == "model" {
			p, version = models, manifest.ModelSetVersion
		} else {
			bytes, err := os.ReadFile(binary)
			if err != nil || os.WriteFile(p, bytes, 0700) != nil {
				t.Fatal("synthetic runtime executable copy failed")
			}
		}
		marker, err := json.Marshal(map[string]any{
			"component": component, "generation": 1, "artifactVersion": version,
			"resolvedPath": p, "modelProfile": manifest.ModelProfile,
			"runtimeProfile": managed.ProductionRuntimeProfile, "runtimeInputSha256": installer.InputDigest,
		})
		if err != nil || os.WriteFile(filepath.Join(generation, "ready.json"), marker, 0600) != nil {
			t.Fatal("synthetic runtime ready binding failed")
		}
		s.Artifact = &domain.RuntimeArtifact{GenerationID: "1", Version: version, Path: p}
		s.Availability = "ready"
		s.Operation.Kind = "idle"
		s.Operation.Phase = "ready"
		if err := store.UpdateRuntime(ctx, fixturePlatform, s); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()
	t.Setenv("LIBERTY_HOST_FIXTURE_CHILD", "asr")
	openFixture := func() (*Profile, error) {
		store, err := jobdb.Open(ctx, path)
		if err != nil {
			return nil, err
		}
		return assembleWithRuntimePlatform(ctx, path, store, managed.ProductionRuntimeProfile, fixturePlatform)
	}
	p, err := openFixture()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(dir, "synthetic.wav")
	if os.WriteFile(media, []byte("synthetic-media"), 0600) != nil {
		t.Fatal("media fixture")
	}
	selected, err := p.Selections.Authorize(media)
	if err != nil {
		t.Fatal(err)
	}
	id, err := p.Submission.Create(ctx, application.CreateMeeting{Selection: selected.Token, Title: "Synthetic host", Language: "zh-CN", SizeLabel: "Local path"})
	if err != nil {
		t.Fatal(err)
	}
	await := func() {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			result, err := p.Meetings.Result(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if result.Job.OverallStatus == "completed" {
				if len(result.Transcript) != 1 || result.Transcript[0].Text != "Synthetic host result" {
					t.Fatal("persisted result missing")
				}
				return
			}
			if result.Job.OverallStatus == "failed" {
				t.Fatal("synthetic host runner failed")
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("native host did not finish job")
	}
	await()
	if err := p.Submission.Retry(ctx, id); err != nil {
		t.Fatal(err)
	}
	await()
	if err := p.Deleter.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	page, err := p.Meetings.Queue(ctx, domain.QueueQuery{Source: "local", Filter: "all", Page: 1, Search: "Synthetic host"})
	if err != nil || page.Total != 0 {
		t.Fatal("deleted job remained visible")
	}
	p.Close()
	reopened, err := openFixture()
	if err != nil {
		t.Fatal("shutdown retained profile lock")
	}
	defer reopened.Close()
	events, err := reopened.Pet.Events(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, entry := range events {
		if entry.EventType != "workflow" || entry.Metadata == nil {
			continue
		}
		var metadata struct {
			Source string `json:"source"`
		}
		if json.Unmarshal([]byte(*entry.Metadata), &metadata) == nil && metadata.Source == id {
			counts[entry.EventSource]++
		}
	}
	if !reflect.DeepEqual(counts, map[string]int{"job_created": 1, "transcription_started": 1, "transcription_completed": 1}) {
		t.Fatal("unexpected ledger after retry and reopen", counts)
	}
}
