//go:build runtime_acceptance

package application_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/execution"
	"liberty.local/liberty/internal/infrastructure/managed"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
	"liberty.local/liberty/internal/infrastructure/runner"
	"liberty.local/liberty/internal/infrastructure/system"
)

type realLiveASR struct {
	command runner.LiveCommand
	record  func(domain.LiveRecognitionEvent)
}

func (realLiveASR) Prepare(context.Context, func(string)) error { return nil }
func (realLiveASR) Ready(context.Context) bool                  { return true }
func (a realLiveASR) Run(ctx context.Context, frames <-chan domain.LiveAudio, emit func(domain.LiveRecognitionEvent) error) error {
	return a.command.Run(ctx, frames, func(event domain.LiveRecognitionEvent) error {
		if a.record != nil {
			a.record(event)
		}
		return emit(event)
	})
}

type pacedFixtureCapture struct {
	ffmpeg, input, directory string
	loop                     bool
}

func (p pacedFixtureCapture) Devices(context.Context) ([]domain.AudioDevice, error) {
	return []domain.AudioDevice{{ID: "public-fixture", Name: "Public test audio"}}, nil
}
func (p pacedFixtureCapture) Capture(ctx context.Context, _ string, emit func([]byte) error) error {
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-re"}
	if p.loop {
		args = append(args, "-stream_loop", "-1")
	}
	args = append(args, "-i", p.input, "-af", "apad=pad_dur=1", "-ac", "1", "-ar", "16000", "-f", "s16le", "pipe:1")
	err := runner.CapturePCM(ctx, p.ffmpeg, args, p.directory, emit)
	if err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

type fixedLiveRuntime struct{ value execution.Runtime }

func (r fixedLiveRuntime) Resolve(context.Context) (execution.Runtime, error) { return r.value, nil }

func TestRealLiveASROfflineTranslationPersistenceAndSubtitleExport(t *testing.T) {
	// Optional paced soak shares the production service, persistence and local
	// workers. It is opt-in and does not run as part of ordinary unit checks.
	var soak time.Duration
	if value := os.Getenv("LIBERTY_NATIVE_LIVE_SOAK_SECONDS"); value != "" {
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds < 60 || seconds > 7200 {
			t.Fatal("soak duration must be 60..7200 seconds")
		}
		soak = time.Duration(seconds) * time.Second
	}
	cache := os.Getenv("LIBERTY_NATIVE_UPGRADE_ROOT")
	seed := os.Getenv("LIBERTY_NATIVE_UPGRADE_MODEL_SEED")
	if !filepath.IsAbs(cache) || !filepath.IsAbs(seed) {
		t.Fatal("explicit isolated runtime acceptance inputs required")
	}
	marker, err := os.ReadFile(filepath.Join(cache, ".native-funasr-upgrade-acceptance"))
	if err != nil || string(marker) != "isolated-funasr-upgrade-v1\n" {
		t.Fatal("acceptance marker required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), soak+3*time.Minute)
	defer cancel()
	m, err := managed.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	runtimeRoot := filepath.Join(cache, "runtime", "profiles", "funasr-upgrade", managed.CurrentPlatform())
	paths := map[string]string{}
	for _, component := range []string{"python", "ffmpeg", "model"} {
		var ready struct {
			Path    string `json:"resolvedPath"`
			Profile string `json:"runtimeProfile"`
			Version string `json:"artifactVersion"`
		}
		data, err := os.ReadFile(filepath.Join(runtimeRoot, "components", component, "generations", "1", "ready.json"))
		if err != nil || json.Unmarshal(data, &ready) != nil || ready.Profile != "funasr-upgrade" || !filepath.IsAbs(ready.Path) {
			t.Fatal("verified runtime marker unavailable", component)
		}
		paths[component] = ready.Path
	}
	publicAudio := filepath.Join(seed, "example", "asr_example.wav")
	data, err := os.ReadFile(publicAudio)
	if err != nil {
		t.Fatal("fixture missing")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != "a1bd32dc78493c123f9625a66deee562aed2895f53fbc39f2cca3be7e6f4f20f" {
		t.Fatal("fixture hash mismatch")
	}
	directory := t.TempDir()
	autoFixture := os.Getenv("LIBERTY_NATIVE_AUTO_LANGUAGE_FIXTURE")
	if autoFixture != "" {
		if !filepath.IsAbs(autoFixture) || soak != 0 {
			t.Fatal("bilingual acceptance requires a separate absolute public fixture")
		}
		english, err := os.ReadFile(autoFixture)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(english)
		if hex.EncodeToString(sum[:]) != "fee8e0460ca707f108782be0d93c555bf34fb6b1cb297e5fceed70192cc65f9b" {
			t.Fatal("English public fixture identity mismatch")
		}
		combined := filepath.Join(directory, "public-bilingual.wav")
		filter := "[0:a]aresample=16000,apad=pad_dur=1[a];[1:a]aresample=16000,apad=pad_dur=1[b];[a][b]concat=n=2:v=0:a=1[out]"
		if err := exec.CommandContext(ctx, paths["ffmpeg"], "-hide_banner", "-nostdin", "-loglevel", "error", "-i", publicAudio, "-i", autoFixture, "-filter_complex", filter, "-map", "[out]", "-ac", "1", "-ar", "16000", combined).Run(); err != nil {
			t.Fatal("public bilingual fixture assembly", err)
		}
		publicAudio = combined
	}
	if soak > 0 {
		padded := filepath.Join(directory, "public-padded.wav")
		if err := exec.CommandContext(ctx, paths["ffmpeg"], "-hide_banner", "-nostdin", "-loglevel", "error", "-i", publicAudio, "-af", "apad=pad_dur=1", "-ac", "1", "-ar", "16000", padded).Run(); err != nil {
			t.Fatal("public fixture padding failed")
		}
		publicAudio = padded
	}
	resources, err := managed.MaterializeResources(directory)
	if err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(directory, "live.sqlite3")
	store, _, err := jobdb.OpenPrepared(ctx, database, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	installer, err := managed.NewInstaller(cache, managed.CurrentPlatform())
	if err != nil {
		t.Fatal(err)
	}
	if err := installer.PrepareTranslation(ctx, nil); err != nil {
		t.Fatal("local translation installation", err)
	}
	translator := &execution.LocalTranslator{Runtime: fixedLiveRuntime{execution.Runtime{Python: paths["python"]}}, Installer: installer, Directory: directory}
	model := filepath.Join(cache, "runtime", "live-models", m.LiveASR.Version)
	vad := filepath.Join(paths["model"], "modelscope", "models", "iic", "speech_fsmn_vad_zh-cn-16k-common-pytorch")
	var recognitionReady time.Time
	var finalDelays []float64
	asr := realLiveASR{command: runner.LiveCommand{Python: paths["python"], Script: filepath.Join(resources, "live_runner.py"), Model: model, VAD: vad, Directory: directory, Threads: 2}, record: func(event domain.LiveRecognitionEvent) {
		if event.Type == "ready" {
			recognitionReady = time.Now()
		}
		if event.Type == "final" && !recognitionReady.IsZero() {
			finalDelays = append(finalDelays, max(0, time.Since(recognitionReady).Seconds()-float64(event.EndMS)/1000))
		}
	}}
	service := application.NewLiveService(store, pacedFixtureCapture{ffmpeg: paths["ffmpeg"], input: publicAudio, directory: directory, loop: soak > 0}, asr, translator, &liveBudgetFixture{})
	defer service.Close()
	sampler := &system.MetricsSampler{}
	baseline, err := sampler.Sample(ctx)
	if err != nil {
		t.Fatal("process metrics unavailable", err)
	}
	request := liveRequest()
	request.DeviceID = "public-fixture"
	request.AutoDetect = autoFixture != ""
	run, err := service.Start(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	partial := false
	completed := false
	var listeningAt time.Time
	var samples []domain.ProcessMetrics
	var sampleLog *os.File
	if soak > 0 {
		sampleLog, err = os.OpenFile(filepath.Join(cache, "live-soak-samples.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			t.Fatal("sample log unavailable", err)
		}
		defer sampleLog.Close()
		defer func() {
			if !t.Failed() {
				return
			}
			report := map[string]any{"failed": true, "elapsedSeconds": time.Since(listeningAt).Seconds(), "session": service.Snapshot().Session, "samples": samples}
			data, e := json.Marshal(report)
			if e == nil {
				_ = os.WriteFile(filepath.Join(cache, "live-soak-failure.json"), data, 0600)
			}
		}()
	}
	recordSample := func(m domain.ProcessMetrics) {
		samples = append(samples, m)
		if sampleLog != nil {
			if err := json.NewEncoder(sampleLog).Encode(m); err != nil {
				t.Fatal("sample log write failed", err)
			}
		}
	}
	nextSample := time.Now().Add(time.Second)
	for !completed {
		select {
		case <-ctx.Done():
			t.Fatal("live chain timed out")
		case <-time.After(50 * time.Millisecond):
			if soak > 0 && !time.Now().Before(nextSample) {
				m, err := sampler.Sample(ctx)
				if err != nil {
					t.Fatal("warmup metrics failed", err)
				}
				recordSample(m)
				nextSample = time.Now().Add(time.Second)
			}
			v := service.Snapshot()
			if v.Session.State == "listening" && listeningAt.IsZero() {
				listeningAt = time.Now()
			}
			partial = partial || v.Partial != nil
			if v.Session.State == "failed" {
				t.Fatal("recognition failed", v.Session.ErrorCode)
			}
			completed = len(v.Segments) > 0 && v.Segments[0].TranslationState == "completed"
			if autoFixture != "" {
				completed = false
				for _, row := range v.Segments {
					if strings.Contains(row.Text, "think") && row.TranslationState == "completed" {
						completed = true
					}
				}
			}
		}
	}
	if !partial {
		t.Fatal("no incremental result observed")
	}
	if soak > 0 {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for time.Since(listeningAt) < soak {
			select {
			case <-ctx.Done():
				t.Fatal("soak timed out")
			case <-ticker.C:
				v := service.Snapshot()
				if v.Session.State != "listening" {
					t.Fatal("soak stopped unexpectedly", v.Session.State, v.Session.ErrorCode)
				}
				if len(v.Segments) > domain.LiveVisibleSegments || v.TranslationPending > 10 {
					t.Fatal("unbounded visible captions or translation backlog")
				}
				m, err := sampler.Sample(ctx)
				if err != nil {
					t.Fatal("metrics failed", err)
				}
				recordSample(m)
				if len(samples)%300 == 0 {
					t.Logf("soak %.0f/%.0f seconds; captions=%d, pending=%d, physical/working memory=%.1f MiB", time.Since(listeningAt).Seconds(), soak.Seconds(), len(v.Segments), v.TranslationPending, float64(m.MemoryBytes)/(1<<20))
				}
			}
		}
	}
	if err := service.Control(ctx, run.ID, "stop"); err != nil {
		t.Fatal(err)
	}
	for domain.LiveActive(service.Snapshot().Session.State) {
		select {
		case <-ctx.Done():
			t.Fatal("stop did not finish")
		case <-time.After(50 * time.Millisecond):
		}
	}
	service.Close()
	postStop, err := sampler.Sample(ctx)
	if err != nil {
		t.Fatal("stop metrics failed", err)
	}
	rows, err := store.LiveSegments(ctx, run.ID, 0, 10)
	if err != nil || len(rows) == 0 || (soak == 0 && autoFixture == "" && len(rows) != 1) || !strings.Contains(rows[0].Text, "语音识别") || !strings.Contains(strings.ToLower(rows[0].Translation), "recognition") {
		t.Fatal("ASR/offline translation result was not persisted", err)
	}
	if autoFixture != "" {
		englishTranslated := false
		for _, row := range rows {
			if strings.Contains(row.Text, "think") {
				source, _ := domain.LiveTranslationDirection(row.Translation)
				englishTranslated = row.TranslationState == "completed" && source == "zh"
			}
		}
		if !englishTranslated {
			t.Fatal("automatic English-to-Chinese translation missing")
		}
	}
	// Use a separate service after closing capture to exercise export policy.
	exporter := application.NewLiveService(store, nil, nil, nil, nil)
	var subtitle string
	if err := exporter.Export(ctx, run.ID, "vtt", func(data []byte) error { subtitle = string(data); return nil }); err != nil || !strings.HasPrefix(subtitle, "WEBVTT\n") || !strings.Contains(subtitle, rows[0].Text) || !strings.Contains(subtitle, rows[0].Translation) {
		t.Fatal("bilingual export failed", err)
	}
	exporter.Close()
	store.Close()
	store, _, err = jobdb.OpenPrepared(ctx, database, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	saved, err := store.LiveSession(ctx, run.ID)
	if err != nil || saved.State != "completed" || (soak == 0 && autoFixture == "" && saved.SegmentCount != 1) || saved.AutoDetect != request.AutoDetect || (soak > 0 && saved.SegmentCount < int64(soak.Seconds()/12)-2) {
		t.Fatal("session did not survive reopen", err)
	}
	if saved.ModelID != translator.ModelID() {
		t.Fatal("persisted model identity differs from the pinned local translator")
	}
	if soak > 0 {
		var memory, cpu, translationMS []float64
		for _, m := range samples {
			memory = append(memory, float64(m.MemoryBytes)/(1<<20))
			if m.CPUPercent != nil {
				cpu = append(cpu, *m.CPUPercent)
			}
		}
		var after int64
		for {
			page, err := store.LiveSegments(ctx, run.ID, after, 100)
			if err != nil {
				t.Fatal(err)
			}
			if len(page) == 0 {
				break
			}
			for _, row := range page {
				if row.TranslationState != "completed" || row.Translation == "" {
					t.Fatal("unfinished persisted translation")
				}
				translationMS = append(translationMS, float64(row.TranslationMS))
				after = row.ID
			}
		}
		percentile := func(values []float64, p float64) float64 {
			if len(values) == 0 {
				return 0
			}
			copy := append([]float64(nil), values...)
			slices.Sort(copy)
			return copy[int(float64(len(copy)-1)*p)]
		}
		report := map[string]any{"durationSeconds": soak.Seconds(), "segments": saved.SegmentCount, "memoryKind": postStop.MemoryKind, "baselineMiB": float64(baseline.MemoryBytes) / (1 << 20), "afterStopMiB": float64(postStop.MemoryBytes) / (1 << 20), "memoryP50MiB": percentile(memory, .5), "memoryP95MiB": percentile(memory, .95), "memoryMaxMiB": percentile(memory, 1), "cpuP50Percent": percentile(cpu, .5), "cpuP95Percent": percentile(cpu, .95), "cpuMaxPercent": percentile(cpu, 1), "finalDelayP95Seconds": percentile(finalDelays, .95), "translationP95MS": percentile(translationMS, .95), "samples": samples}
		data, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cache, "live-soak-result.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		delete(report, "samples")
		summary, _ := json.Marshal(report)
		t.Log("live_soak_metrics", string(summary))
		if len(translationMS) != int(saved.SegmentCount) || postStop.MemoryBytes > baseline.MemoryBytes+128<<20 {
			t.Fatal("incomplete persistence or worker memory not released")
		}
	}
	t.Log("Paced public audio -> real ASR partial/final -> real local translation -> SQLite -> bilingual VTT -> reopen passed without credentials or network translation. Corpus quality is a separate acceptance gate.")
}
