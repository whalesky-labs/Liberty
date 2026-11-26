//go:build compatibility

package execution

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
	"liberty.local/liberty/internal/infrastructure/repositories/legacydb"
	"liberty.local/liberty/internal/infrastructure/runner"
)

func TestMain(m *testing.M) {
	mode := os.Getenv("LIBERTY_EXECUTION_FIXTURE_CHILD")
	if mode != "" {
		if models := os.Getenv("LIBERTY_EXECUTION_EXPECTED_MODELS"); models != "" {
			for key, child := range map[string]string{"FUNASR_MODEL": "asr", "FUNASR_VAD_MODEL": "vad", "FUNASR_PUNC_MODEL": "punc", "FUNASR_SPK_MODEL": "speaker"} {
				if os.Getenv(key) != filepath.Join(models, child) {
					os.Exit(10)
				}
			}
			if os.Getenv("FUNASR_PROFILE") != "paraformer" {
				os.Exit(10)
			}
		}
		directory := ""
		for i, arg := range os.Args {
			if arg == "--job-dir" && i+1 < len(os.Args) {
				directory = os.Args[i+1]
			}
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(directory, "synthetic-permit")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				os.Exit(8)
			}
			time.Sleep(time.Millisecond)
		}
		if mode == "failed" {
			os.Exit(3)
		}
		if mode == "cancel" {
			time.Sleep(30 * time.Second)
			os.Exit(0)
		}
		fmt.Fprintln(os.Stdout, `{"protocolVersion":2,"type":"diagnostic","level":"info","code":"synthetic_ready","message":"fixture ready"}`)
		data := []byte(`{"protocolVersion":2,"asrBackend":"funasr","diarizationRequested":false,"diarizationStatus":"disabled","warnings":[],"durationMinutes":1,"transcriptSegments":[{"id":"s1","startMs":0,"endMs":1000,"text":"Synthetic end to end"}],"speakerSegments":[]}`)
		if os.WriteFile(filepath.Join(directory, "result.json"), data, 0600) != nil {
			os.Exit(9)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type fixtureRuntime struct{ value Runtime }

func (r fixtureRuntime) Resolve(context.Context) (Runtime, error) { return r.value, nil }

type fixtureStore struct {
	*jobdb.Store
	directory string
	cancel    context.CancelFunc
}

func (s *fixtureStore) Attach(ctx context.Context, l domain.RunLease, pid uint32, identity string, now int64) error {
	if err := s.Store.Attach(ctx, l, pid, identity, now); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.directory, "jobs", l.JobID, l.OutputDir, "synthetic-permit"), nil, 0600); err != nil {
		return err
	}
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}
func TestWriteEngineRunsRealChildAndPersistsOutcome(t *testing.T) {
	source := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if source == "" {
		t.Fatal("run pnpm native:compat")
	}
	for _, mode := range []string{"completed", "failed", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("LIBERTY_EXECUTION_FIXTURE_CHILD", mode)
			directory := t.TempDir()
			modelsRoot := filepath.Join(directory, "selected-models")
			paths := runner.ModelPaths{Profile: "paraformer", ASR: filepath.Join(modelsRoot, "asr"), VAD: filepath.Join(modelsRoot, "vad"), Punctuation: filepath.Join(modelsRoot, "punc"), Speaker: filepath.Join(modelsRoot, "speaker")}
			for _, path := range []string{paths.ASR, paths.VAD, paths.Punctuation, paths.Speaker} {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("LIBERTY_EXECUTION_EXPECTED_MODELS", modelsRoot)
			for _, key := range []string{"FUNASR_PROFILE", "FUNASR_MODEL", "FUNASR_VAD_MODEL", "FUNASR_PUNC_MODEL", "FUNASR_SPK_MODEL"} {
				t.Setenv(key, "unselected-model-alias")
			}
			data, err := os.ReadFile(filepath.Join(source, "liberty.sqlite3"))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "liberty.sqlite3")
			if os.WriteFile(path, data, 0600) != nil {
				t.Fatal("database fixture")
			}
			store, _, err := jobdb.OpenPrepared(context.Background(), path, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			files, err := runner.OpenFiles(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer files.Close()
			input := filepath.Join(directory, "synthetic.wav")
			script := filepath.Join(directory, "synthetic-runner")
			if os.WriteFile(input, nil, 0600) != nil || os.WriteFile(script, nil, 0600) != nil {
				t.Fatal("process fixture")
			}
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			job := domain.JobInput{Job: domain.JobSummary{ID: "job-1700000000000-99", Title: "Synthetic execution", CreatedAt: "2026-11-17T00:00:00.000Z", Lang: "zh", SourceFiles: []domain.SourceFile{{ID: "source-99", Name: "synthetic.wav", Kind: "audio", SizeLabel: "1 KB"}}}, SourcePath: input}
			if err := store.Create(context.Background(), job); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			wrapper := &fixtureStore{Store: store, directory: directory}
			if mode == "cancel" {
				wrapper.cancel = cancel
			}
			adapter := &Runner{Files: files, Runtime: fixtureRuntime{Runtime{Python: binary, Script: script, Models: modelsRoot, ModelPaths: paths, Backend: "funasr", Threads: 1, BatchSeconds: 300}}}
			err = application.NewExecutor(wrapper, adapter, time.Now).Execute(ctx, job.Job.ID)
			if mode == "completed" && err != nil {
				t.Fatal(err)
			}
			if mode == "failed" && !errors.Is(err, runner.ErrProcess) {
				t.Fatalf("failure: %v", err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: %v", err)
			}
			reader, err := legacydb.Open(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			result, err := reader.ReadResult(context.Background(), job.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			expected := mode
			if mode == "cancel" {
				expected = "queued"
			}
			if result.Job.OverallStatus != expected {
				t.Fatalf("persisted %s expected %s", result.Job.OverallStatus, expected)
			}
			if mode == "completed" && (len(result.Transcript) != 1 || result.Transcript[0].Text != "Synthetic end to end" || result.ProcessLog == nil || !strings.Contains(*result.ProcessLog, "synthetic_ready")) {
				t.Fatal("result/log not persisted")
			}
			if mode != "completed" && len(result.Transcript) != 0 {
				t.Fatal("failed/cancelled process published result")
			}
		})
	}
}

type noWork struct{}

func (noWork) Execute(context.Context, string) error { return errors.New("unexpected_scheduling") }

type interruptedPurge struct {
	*runner.Files
	fail bool
}

func (f *interruptedPurge) Purge(name string) error {
	if f.fail {
		return errors.New("synthetic_purge_interruption")
	}
	return f.Files.Purge(name)
}
func TestWriteDeletionResumesActualFilesystemAndDatabase(t *testing.T) {
	source := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if source == "" {
		t.Fatal("run pnpm native:compat")
	}
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join(source, "liberty.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "liberty.sqlite3")
	if os.WriteFile(path, data, 0600) != nil {
		t.Fatal("fixture database")
	}
	ctx := context.Background()
	store, _, err := jobdb.OpenPrepared(ctx, path, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	files, err := runner.OpenFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	id := "job-1700000000000-2" // existing synthetic failed job
	if err := files.CreateJob(id); err != nil {
		t.Fatal(err)
	}
	if os.WriteFile(filepath.Join(dir, "jobs", id, "synthetic-result"), []byte("fixture"), 0600) != nil {
		t.Fatal("artifact fixture")
	}
	scheduler := application.NewScheduler(noWork{}, 1)
	defer scheduler.Shutdown(ctx)
	broken := &interruptedPurge{Files: files, fail: true}
	deleter := application.NewDeleter(store, broken, Processes{}, scheduler)
	if err := deleter.Delete(ctx, id); err == nil {
		t.Fatal("purge interruption not reported")
	}
	pending, err := store.PendingDeletions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	trash := ""
	for _, op := range pending {
		if op.JobID == id {
			if op.Phase != "database_deleted" {
				t.Fatal("delete transaction not committed")
			}
			trash = op.TrashName
		}
	}
	if trash == "" {
		t.Fatal("compensation journal missing")
	}
	artifact := filepath.Join(dir, "jobs", ".trash", trash, "synthetic-result")
	if _, err := os.Stat(artifact); err != nil {
		t.Fatal("interrupted trash removed prematurely")
	}
	store.Close()
	resumed, err := jobdb.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	broken.fail = false
	// The source fixture also has a separate prepared deletion; Recover handles
	// both without consulting any real profile, process or media file.
	if err := application.NewDeleter(resumed, broken, Processes{}, scheduler).Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(artifact); !os.IsNotExist(err) {
		t.Fatal("trash remains after recovery")
	}
	if pending, err := resumed.PendingDeletions(ctx); err != nil || len(pending) != 0 {
		t.Fatal("completed journal not cleared")
	}
	reader, err := legacydb.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.ReadResult(ctx, id); !errors.Is(err, application.ErrJobNotFound) {
		t.Fatal("deleted job reappeared")
	}
}
