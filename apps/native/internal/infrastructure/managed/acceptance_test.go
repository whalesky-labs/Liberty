//go:build runtime_acceptance

package managed

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/runner"
)

// This opt-in check downloads the original pinned upstream assets and executes
// the real validators. The explicit isolated directory is kept for safe retries;
// it is never a discovery mechanism for an existing user profile.
func TestPinnedRuntimeAcceptance(t *testing.T) {
	directory := os.Getenv("LIBERTY_NATIVE_RUNTIME_ACCEPTANCE_DIR")
	if !filepath.IsAbs(directory) {
		t.Fatal("explicit acceptance directory required")
	}
	marker, err := os.ReadFile(filepath.Join(directory, ".native-runtime-acceptance"))
	if err != nil || string(marker) != "isolated-runtime-acceptance-v1\n" {
		t.Fatal("isolated acceptance marker required")
	}
	lock, err := profile.Acquire(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	runtimeProfile := os.Getenv("LIBERTY_NATIVE_RUNTIME_ACCEPTANCE_PROFILE")
	if runtimeProfile == "" {
		runtimeProfile = "production"
	}
	i, err := NewInstallerWithProfile(directory, CurrentPlatform(), runtimeProfile)
	if err != nil {
		t.Fatal(err)
	}
	if i.Platform.UnsupportedReason != "" {
		t.Skip("managed local ASR is unsupported on this platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()
	for _, component := range []string{"ffmpeg", "python"} {
		t.Log("installing pinned " + component)
		if _, err := i.InstallTool(ctx, component, 1, "official", func(text string) error { return i.AppendLog(component, text) }); err != nil {
			t.Fatalf("%s: %v", component, err)
		}
		t.Log(component + " validated")
	}
	t.Log("acquiring pinned model set")
	if !i.ModelsAcquired(1) {
		if err := i.AcquireModels(ctx, 1, "official", func(text string) error { return i.AppendLog("model", text) }); err != nil {
			t.Fatal(err)
		}
	}
	pythonRoot, err := i.openGeneration("python", 1)
	if err != nil {
		t.Fatal(err)
	}
	python := findCandidate(pythonRoot, pythonRoot.Name(), i.Platform.PythonCandidates)
	pythonRoot.Close()
	if python == "" {
		t.Fatal("validated python unavailable")
	}
	t.Log("validating model set offline")
	models, err := i.ValidateModels(ctx, 1, python, func(text string) error { return i.AppendLog("model", text) })
	if err != nil {
		t.Fatal(err)
	}
	modelPaths, err := i.ResolveModels(ctx, models)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(modelPaths.ASR, "example", "asr_example.wav")
	inputInfo, err := os.Lstat(input)
	if err != nil || !inputInfo.Mode().IsRegular() || inputInfo.Size() == 0 {
		t.Fatal("pinned model audio fixture unavailable")
	}
	t.Log("pinned runtime and offline models validated")
	log := func(text string) error { return i.AppendLog("python", text) }
	attemptRoot, err := os.MkdirTemp(directory, "runner-acceptance-")
	if err != nil {
		t.Fatal("runner acceptance directory unavailable")
	}
	defer os.RemoveAll(attemptRoot)
	for _, speaker := range []bool{false, true} {
		attempt, err := os.MkdirTemp(attemptRoot, "attempt-")
		if err != nil {
			t.Fatal("attempt directory unavailable")
		}
		result, err := runner.Execute(ctx, runner.CommandSpec{Python: python, Script: filepath.Join(i.Resources, "runner.py"), Input: input, Directory: attempt, FFmpeg: "", Models: models.Path, ModelPaths: modelPaths, Language: "zh-CN", Speaker: speaker, Backend: i.Platform.Backend, Device: "cpu", Threads: 2, BatchSeconds: 300}, runner.Hooks{Diagnostic: log, Event: func(event runner.Event) error { return log(event.Code + ": " + event.Message) }})
		if err != nil {
			t.Fatal(err)
		}
		text := ""
		for _, segment := range result.Transcript {
			text += segment.Text
		}
		if strings.TrimSpace(text) == "" {
			t.Fatal("pinned model audio produced an empty transcript")
		}
		if !speaker && result.DiarizationStatus != "disabled" {
			t.Fatal("disabled diarization changed")
		}
		t.Logf("pinned model audio validated: segments=%d diarization=%s", len(result.Transcript), result.DiarizationStatus)
	}
}
