//go:build runtime_acceptance

package host

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/managed"
	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
	"liberty.local/liberty/internal/infrastructure/system"
)

func TestPinnedHostRuntimeAcceptance(t *testing.T) {
	cache := os.Getenv("LIBERTY_NATIVE_RUNTIME_ACCEPTANCE_DIR")
	if !filepath.IsAbs(cache) {
		t.Fatal("explicit isolated runtime acceptance directory required")
	}
	marker, err := os.ReadFile(filepath.Join(cache, ".native-runtime-acceptance"))
	if err != nil || string(marker) != "isolated-runtime-acceptance-v1\n" {
		t.Fatal("run native:runtime:acceptance first")
	}
	owner, err := profile.Acquire(cache)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	cache = owner.Directory()
	runtimeProfile := os.Getenv("LIBERTY_NATIVE_RUNTIME_ACCEPTANCE_PROFILE")
	if runtimeProfile == "" {
		runtimeProfile = "production"
	}
	installer, err := managed.NewInstallerWithProfile(cache, managed.CurrentPlatform(), runtimeProfile)
	if err != nil {
		t.Fatal(err)
	}
	if installer.Platform.UnsupportedReason != "" {
		t.Skip("managed local ASR is unsupported on this platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	// Both profiles require runtime generations to belong to the host's profile
	// root. A database in another temporary directory cannot borrow ready markers
	// from this cache. Transfer the one profile lock before opening the real host.
	databaseFile, err := os.CreateTemp(cache, "host-acceptance-*.sqlite3")
	if err != nil {
		t.Fatal("host database allocation failed")
	}
	database := databaseFile.Name()
	if databaseFile.Close() != nil || owner.Close() != nil {
		t.Fatal("host could not transfer profile ownership")
	}
	store, _, err := jobdb.OpenPrepared(ctx, database, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	artifacts := map[string]domain.RuntimeArtifact{}
	for _, component := range []string{"ffmpeg", "python", "model"} {
		var ready struct {
			Component      string `json:"component"`
			Generation     int64  `json:"generation"`
			Version        string `json:"artifactVersion"`
			Path           string `json:"resolvedPath"`
			Profile        string `json:"modelProfile"`
			RuntimeProfile string `json:"runtimeProfile"`
			InputDigest    string `json:"runtimeInputSha256"`
		}
		data, err := os.ReadFile(filepath.Join(installer.PlatformRoot(), "components", component, "generations", "1", "ready.json"))
		if err != nil || json.Unmarshal(data, &ready) != nil {
			t.Fatal("validated runtime marker unavailable", component)
		}
		expected := installer.Manifest.RuntimeVersion
		if component == "model" {
			expected = installer.Manifest.ModelSetVersion
		}
		if ready.Component != component || ready.Generation != 1 || ready.Version != expected || ready.Profile != installer.Manifest.ModelProfile || !filepath.IsAbs(ready.Path) {
			t.Fatal("validated runtime marker does not match current manifest", component)
		}
		if ready.RuntimeProfile != runtimeProfile || ready.InputDigest != installer.InputDigest || installer.InputDigest == "" {
			t.Fatal("runtime marker is not bound to the selected inputs", component)
		}
		resolved, err := filepath.EvalSymlinks(ready.Path)
		if err != nil {
			t.Fatal("validated runtime artifact unavailable", component)
		}
		relative, err := filepath.Rel(cache, resolved)
		if err != nil || relative == "." || !filepath.IsLocal(relative) {
			t.Fatal("runtime artifact is outside the isolated acceptance cache", component)
		}
		artifact := domain.RuntimeArtifact{GenerationID: strconv.FormatInt(ready.Generation, 10), Version: ready.Version, Path: ready.Path}
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
	modelPaths, err := installer.ResolveModels(ctx, artifacts["model"])
	if err != nil {
		t.Fatal("validated model paths unavailable", err)
	}
	input := filepath.Join(modelPaths.ASR, "example", "asr_example.wav")
	inputInfo, err := os.Lstat(input)
	if err != nil || !inputInfo.Mode().IsRegular() || inputInfo.Size() == 0 {
		t.Fatal("pinned model audio fixture unavailable")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	host, handover, err := OpenHandoverWithRuntimeProfile(ctx, database, false, runtimeProfile)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	if handover.Backup == "" {
		t.Fatal("prepared acceptance profile has no handover recovery point")
	}
	if err := host.Start(ctx); err != nil {
		t.Fatal(err)
	}
	metrics := &system.MetricsSampler{}
	baseline, err := metrics.Sample(ctx)
	if err != nil {
		t.Fatal("process-tree metrics unavailable", err)
	}
	peakMemory := baseline.MemoryBytes
	lastSample := time.Now()
	completed := map[string]domain.Result{}
	for _, speaker := range []bool{false, true} {
		selected, err := host.Selections.Authorize(input)
		if err != nil {
			t.Fatal("pinned model media selection failed", err)
		}
		id, err := host.Submission.Create(ctx, application.CreateMeeting{Selection: selected.Token, Title: "Pinned model audio acceptance", Language: "zh-CN", SummaryTemplate: "表格版会议纪要", SizeLabel: "pinned model fixture", Speaker: speaker})
		if err != nil {
			t.Fatal("host submission failed", err)
		}
		ticker := time.NewTicker(100 * time.Millisecond)
		for {
			if time.Since(lastSample) >= time.Second {
				usage, err := metrics.Sample(ctx)
				if err != nil {
					t.Fatal("runtime process metrics failed", err)
				}
				peakMemory = max(peakMemory, usage.MemoryBytes)
				lastSample = time.Now()
			}
			result, err := host.Meetings.Result(ctx, id)
			if err != nil {
				ticker.Stop()
				t.Fatal("host result read failed", err)
			}
			if result.Job.OverallStatus == "completed" {
				completed[id] = result
				break
			}
			if result.Job.OverallStatus == "failed" || result.Job.OverallStatus == "cancelled" {
				ticker.Stop()
				t.Fatal("host transcription did not complete", result.Job.OverallStatus)
			}
			select {
			case <-ctx.Done():
				ticker.Stop()
				t.Fatal("host transcription timed out")
			case <-ticker.C:
			}
		}
		ticker.Stop()
		result := completed[id]
		text := ""
		for _, segment := range result.Transcript {
			text += segment.Text
		}
		if strings.TrimSpace(text) == "" || result.Job.ASRStatus != "completed" || result.Job.RunnerProtocolVersion == nil || *result.Job.RunnerProtocolVersion != 2 {
			t.Fatal("host result lacks completed protocol-v2 model-fixture transcript")
		}
		expected := "disabled"
		if speaker {
			expected = "completed"
			if len(result.Speakers) == 0 {
				t.Fatal("host result lacks diarized segments")
			}
		}
		if result.Job.DiarizationStatus != expected {
			t.Fatal("host diarization state mismatch", result.Job.DiarizationStatus)
		}
		if len(result.SummaryRuns) != 0 || result.Job.SummaryStatus != "idle" {
			t.Fatal("basic transcription started AI without user action")
		}
		scopes := application.NewWindowScopes(host.Meetings)
		scopes.EnableEditing(host.SpeakerWriter)
		grant, err := scopes.OpenFromMain(ctx, application.Workbench, "local", id)
		if err != nil {
			t.Fatal("free result scope unavailable", err)
		}
		segment := result.PrimarySegments()[0]
		corrected := segment.Text + " [verified]"
		if err := grant.EditSegment(ctx, domain.SegmentEdit{Original: segment, Text: corrected, Speaker: segment.Speaker}); err != nil {
			t.Fatal("credential-free correction failed", err)
		}
		for _, kind := range []domain.TextExportKind{domain.ExportTranscript, domain.ExportBundle} {
			var exported []byte
			if err := grant.ExportText(ctx, kind, domain.TextExportLabels{TranscriptHeading: "Transcript"}, func(data []byte) error { exported = data; return nil }); err != nil || !strings.Contains(string(exported), corrected) {
				t.Fatal("credential-free export omitted saved correction", kind, err)
			}
		}
		grant.Close()
		completed[id], err = host.Meetings.Result(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("host transcription completed: speaker=%t transcript=%d speakers=%d", speaker, len(result.Transcript), len(result.Speakers))
	}
	host.Close()
	postStop, err := metrics.Sample(ctx)
	if err != nil {
		t.Fatal("post-shutdown process metrics unavailable", err)
	}
	t.Logf("Whole-process-tree memory: before %.1f MiB, peak %.1f MiB, after shutdown %.1f MiB; this test process excludes GUI and does not establish the 8 GiB target.", float64(baseline.MemoryBytes)/(1<<20), float64(peakMemory)/(1<<20), float64(postStop.MemoryBytes)/(1<<20))
	reopened, _, err := OpenHandoverWithRuntimeProfile(ctx, database, false, runtimeProfile)
	if err != nil {
		t.Fatal("completed profile could not reopen", err)
	}
	defer reopened.Close()
	jobs, err := reopened.Meetings.ListJobs(ctx)
	if err != nil || len(jobs) != len(completed) {
		t.Fatal("completed jobs did not persist", err)
	}
	for id, expected := range completed {
		result, err := reopened.Meetings.Result(ctx, id)
		if err != nil || result.Job.OverallStatus != "completed" || result.Job.DiarizationStatus != expected.Job.DiarizationStatus || !reflect.DeepEqual(result.Transcript, expected.Transcript) || !reflect.DeepEqual(result.Speakers, expected.Speakers) {
			t.Fatal("completed transcription changed after reopening", err)
		}
	}
	events, err := reopened.Pet.Events(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, event := range events {
		if event.EventType == "workflow" {
			counts[event.EventSource]++
		}
	}
	for _, event := range []string{"job_created", "transcription_started", "transcription_completed"} {
		if counts[event] != len(completed) {
			t.Fatal("host workflow rewards did not persist exactly once per job", event, counts[event])
		}
	}
	t.Log("Without AI configuration: real transcription, diarization, saved correction, TXT/Markdown exports and reopen passed; no AI summary was created.")
}
