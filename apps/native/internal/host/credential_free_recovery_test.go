package host

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/repositories/schema"
)

func hostRecoveryOpen(test *testing.T, database, mode string) (*Profile, schema.Result) {
	test.Helper()
	ctx := context.Background()
	var profileHost *Profile
	var result schema.Result
	var err error
	switch mode {
	case "create":
		profileHost, result, err = OpenPrepared(ctx, database, true)
	case "handover":
		hostRecoveryWithoutCredentials(test, database)
		profileHost, result, err = OpenHandover(ctx, database, false)
	case "open":
		hostRecoveryWithoutCredentials(test, database)
		profileHost, err = Open(ctx, database)
	default:
		test.Fatal("unknown credential-free host mode")
	}
	recoveryRequire(test, err)
	test.Cleanup(profileHost.Close)
	hostRecoveryWithoutCredentials(test, database)
	return profileHost, result
}

func hostRecoveryWithoutCredentials(test *testing.T, database string) {
	test.Helper()
	connection := recoverySQL(test, database, "ro")
	defer connection.Close()
	var references int
	recoveryRequire(test, connection.QueryRow(`SELECT
 (SELECT COUNT(*) FROM ai_model_configs WHERE TRIM(api_key)<>'' OR COALESCE(api_key_ref,'')<>'') +
 (SELECT COUNT(*) FROM app_settings WHERE TRIM(api_token)<>'' OR COALESCE(api_token_ref,'')<>'') +
 (SELECT COUNT(*) FROM credential_cleanup_intents) +
 (SELECT COUNT(*) FROM app_meta WHERE key='native_credential_migration_v3')`).Scan(&references))
	if references != 0 {
		test.Fatal("credential-free host fixture contains a secret, reference or cleanup journal")
	}
}

func hostRecoveryCompletedSummary(test *testing.T, profileHost *Profile, jobID, runID, templateID, now string) domain.SummaryRun {
	test.Helper()
	ctx := context.Background()
	meeting, err := profileHost.Meetings.Result(ctx, jobID)
	recoveryRequire(test, err)
	transcript, err := domain.SummaryJSON(meeting.PrimarySegments())
	recoveryRequire(test, err)
	hash := domain.SummaryHash(transcript)
	snapshot, err := json.Marshal(domain.SummaryExecutionSnapshot{SchemaVersion: 1, Job: domain.SnapshotSummaryJob(meeting), ModelBaseURL: "https://synthetic.invalid", ModelName: "synthetic-history", TemplateID: templateID, SystemPrompt: "Synthetic historical summary.", FallbackTitle: meeting.Job.Title})
	recoveryRequire(test, err)
	prompt := "Summarize the synthetic historical meeting."
	chunk := domain.SummaryChunk{Index: 0, SHA256: domain.SummaryHash([]byte(prompt)), UserPrompt: prompt}
	_, err = profileHost.store.CreateSummary(ctx, domain.SummarySeed{Execution: domain.SummaryExecution{Run: domain.SummaryRun{ID: runID, JobID: jobID, TemplateID: templateID, CreatedAt: now, UpdatedAt: now}, TranscriptRevision: "sha256:" + hash, TranscriptSHA256: hash, TranscriptJSON: string(transcript), SnapshotJSON: string(snapshot), ChunkCount: 1}, Chunks: []domain.SummaryChunk{chunk}})
	recoveryRequire(test, err)
	lease, err := profileHost.store.ClaimSummary(ctx, jobID, runID, now)
	recoveryRequire(test, err)
	if lease == nil {
		test.Fatal("historical summary did not obtain its publication lease")
	}
	parsed, err := domain.ParseSummaryStructured(recoverySummaryRaw)
	recoveryRequire(test, err)
	structured, err := domain.SummaryJSON(parsed)
	recoveryRequire(test, err)
	chunk.Raw, chunk.Structured = recoverySummaryRaw, string(structured)
	recoveryRequire(test, profileHost.store.SaveSummaryChunk(ctx, *lease, chunk, now))
	result, err := domain.ParseSummaryResponse(recoverySummaryRaw, meeting.Job.Title)
	recoveryRequire(test, err)
	minutes, err := domain.ReconstructMinutes(meeting, result, nil, templateID, &runID)
	recoveryRequire(test, err)
	recoveryRequire(test, profileHost.store.CompleteSummary(ctx, *lease, domain.SummaryCompletion{Raw: recoverySummaryRaw, Result: result, Minutes: minutes, Diagnostics: `{"schemaVersion":1}`, CompletedAt: now}))
	_, err = profileHost.Pet.Workflow(ctx, "ai_summary_completed", runID)
	recoveryRequire(test, err)
	return recoveryAwaitSummary(test, profileHost, jobID, runID)
}

func hostRecoveryWorkCycle(test *testing.T, fixture *recoveryFixture, profileHost *Profile) {
	test.Helper()
	fixture.workCycle(test, &Profile{Pet: profileHost.Pet, Work: application.NewWork(profileHost.store, func() time.Time { return fixture.now })})
}

func TestCredentialFreeHostRecoveryRestoresCompleteBusinessSnapshot(t *testing.T) {
	ctx := context.Background()
	fixture := &recoveryFixture{database: filepath.Join(t.TempDir(), "credential-free #资料.sqlite3"), now: time.Now().UTC().Add(-6 * time.Hour)}
	initial, prepared := hostRecoveryOpen(t, fixture.database, "create")
	if prepared.Version != 13 {
		t.Fatal("credential-free fixture was not prepared as v13")
	}
	recoveryRequire(t, initial.Start(ctx))
	template, err := initial.Resources.SaveTemplate(ctx, domain.SummaryTemplate{ID: "host-recovery-template", Name: "Before takeover", Prompt: "Summarize the historical meeting."})
	recoveryRequire(t, err)
	_, err = initial.Resources.Settings(ctx)
	recoveryRequire(t, err)
	_, err = initial.Resources.SaveMember(ctx, domain.MeetingMember{ID: "host-recovery-member", Name: "Before takeover", Department: "Synthetic", IsRecorder: true})
	recoveryRequire(t, err)
	_, err = initial.Pet.SaveName(ctx, "Before takeover")
	recoveryRequire(t, err)
	_, err = initial.Pet.ClaimCheckIn(ctx)
	recoveryRequire(t, err)
	fixture.job(t, initial, recoveryJobID, "Before takeover")
	originalSummary := hostRecoveryCompletedSummary(t, initial, recoveryJobID, "summary-run-1700000000000-801", template.ID, domain.PersistedUTC(fixture.now))
	hostRecoveryWorkCycle(t, fixture, initial)
	originalStore, err := initial.Pet.Store(ctx)
	recoveryRequire(t, err)
	hostRecoveryWithoutCredentials(t, fixture.database)
	initial.Close()
	before := recoverySnapshot(t, fixture.database)
	for _, table := range []string{"jobs", "job_runs", "job_events", "transcript_segments", "ai_summary_runs", "ai_summary_run_chunks", "meeting_members", "ai_summary_templates", "app_settings", "pet_profile", "pet_event_ledger", "pet_wallets", "pet_inventory", "pet_economy_ledger", "farm_harvest_ledger", "work_game_reward_ledger"} {
		if len(before[table]) == 0 {
			t.Fatal("credential-free business fixture did not populate", table)
		}
	}
	handover, takeover := hostRecoveryOpen(t, fixture.database, "handover")
	if takeover.Version != 13 || takeover.FromVersion != 13 || takeover.Backup == "" {
		t.Fatal("host did not create the first takeover recovery point", takeover)
	}
	recoveryEqualSnapshot(t, before, recoverySnapshot(t, takeover.Backup))
	if _, err := RestoreProfile(ctx, fixture.database, filepath.Base(takeover.Backup)); !errors.Is(err, profile.ErrInUse) {
		t.Fatal("public host restore bypassed active ownership", err)
	}
	recoveryRequire(t, handover.Start(ctx))
	_, err = handover.Resources.SaveMember(ctx, domain.MeetingMember{ID: "host-recovery-member", Name: "After takeover", Department: "Changed synthetic", IsRecorder: true})
	recoveryRequire(t, err)
	_, err = handover.Resources.SaveMember(ctx, domain.MeetingMember{ID: "host-added-member", Name: "Added after takeover"})
	recoveryRequire(t, err)
	changedTemplate := template
	changedTemplate.Name, changedTemplate.Prompt = "After takeover", "Changed historical prompt."
	_, err = handover.Resources.SaveTemplate(ctx, changedTemplate)
	recoveryRequire(t, err)
	_, err = handover.Resources.SaveTemplate(ctx, domain.SummaryTemplate{ID: "host-added-template", Name: "Added after takeover", Prompt: "Additional historical prompt."})
	recoveryRequire(t, err)
	settings, err := handover.Resources.Settings(ctx)
	recoveryRequire(t, err)
	settings.Locale = "en-US"
	_, outcome, err := handover.Resources.SaveSettings(ctx, settings, domain.CredentialUpdate{Action: domain.KeepCredential})
	recoveryRequire(t, err)
	if outcome.CleanupPending {
		t.Fatal("credential-free settings update left cleanup pending")
	}
	_, err = handover.Pet.SaveName(ctx, "After takeover")
	recoveryRequire(t, err)
	fixture.now = fixture.now.Add(time.Hour)
	hostRecoveryWorkCycle(t, fixture, handover)
	recoveryRequire(t, handover.store.Retry(ctx, recoveryJobID, "", ""))
	fixture.execute(t, handover, recoveryJobID, "Changed after takeover")
	hostRecoveryCompletedSummary(t, handover, recoveryJobID, "summary-run-1700000000000-802", template.ID, domain.PersistedUTC(fixture.now))
	fixture.job(t, handover, recoveryAddedJobID, "Added after takeover")
	hostRecoveryCompletedSummary(t, handover, recoveryAddedJobID, "summary-run-1700000000000-803", template.ID, domain.PersistedUTC(fixture.now))
	const interruptedDeletionID = "job-1700000000000-703"
	fixture.job(t, handover, interruptedDeletionID, "Synthetic interrupted deletion")
	operation, err := handover.store.PrepareDeletion(ctx, interruptedDeletionID)
	recoveryRequire(t, err)
	if operation.Phase != "prepared" || operation.PID != nil || operation.ProcessIdentity != nil {
		t.Fatal("synthetic deletion did not stop before its recoverable filesystem phase")
	}
	handover.Close()
	restarted, repeated := hostRecoveryOpen(t, fixture.database, "handover")
	if repeated.Backup != takeover.Backup {
		t.Fatal("public host restart replaced the first recovery point")
	}
	pending, err := restarted.store.PendingDeletions(ctx)
	recoveryRequire(t, err)
	if len(pending) != 1 || pending[0].ID != operation.ID {
		t.Fatal("interrupted deletion did not survive host shutdown")
	}
	recoveryRequire(t, restarted.Start(ctx))
	recoveryRequire(t, restarted.Start(ctx))
	pending, err = restarted.store.PendingDeletions(ctx)
	recoveryRequire(t, err)
	if len(pending) != 0 {
		t.Fatal("public host startup did not finish durable deletion recovery")
	}
	keeper := recoverySQL(t, fixture.database, "rw")
	defer keeper.Close()
	_, err = keeper.Exec("PRAGMA wal_autocheckpoint=0")
	recoveryRequire(t, err)
	var busy, frames, checkpointed int
	recoveryRequire(t, keeper.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &frames, &checkpointed))
	if busy != 0 {
		t.Fatal("credential-free WAL fixture could not checkpoint")
	}
	_, err = restarted.Resources.SaveMember(ctx, domain.MeetingMember{ID: "host-wal-only-member", Name: "Committed only in WAL"})
	recoveryRequire(t, err)
	restarted.Close()
	after := recoverySnapshot(t, fixture.database)
	if len(after["jobs"]) != 2 || len(after["farm_harvest_ledger"]) != 2 || len(after["work_game_reward_ledger"]) != 2 || reflect.DeepEqual(before, after) {
		t.Fatal("credential-free host did not retain post-takeover business changes")
	}
	mainOnly := filepath.Join(t.TempDir(), "credential-free-main-only.sqlite3")
	mainBytes, err := os.ReadFile(fixture.database)
	recoveryRequire(t, err)
	recoveryRequire(t, os.WriteFile(mainOnly, mainBytes, 0600))
	if len(after["meeting_members"]) != len(recoverySnapshot(t, mainOnly)["meeting_members"])+1 {
		t.Fatal("credential-free late write was not isolated to committed WAL")
	}
	corruptName := filepath.Base(fixture.database) + ".pre-native-v13-synthetic-corrupt.bak"
	recoveryRequire(t, os.WriteFile(filepath.Join(filepath.Dir(fixture.database), corruptName), []byte("synthetic corrupt snapshot"), 0600))
	failed, err := RestoreProfile(ctx, fixture.database, corruptName)
	if !errors.Is(err, schema.ErrRestore) || failed.SafetyBackup != "" {
		t.Fatal("public host reported success for an invalid recovery point", failed, err)
	}
	recoveryEqualSnapshot(t, after, recoverySnapshot(t, fixture.database))
	owner, err := profile.Acquire(filepath.Dir(fixture.database))
	recoveryRequire(t, err)
	recoveryRequire(t, owner.Close())
	hostRecoveryWithoutCredentials(t, fixture.database)
	hostRecoveryWithoutCredentials(t, takeover.Backup)
	restored, err := RestoreProfile(ctx, fixture.database, filepath.Base(takeover.Backup))
	recoveryRequire(t, err)
	if restored.RestoredVersion != 13 || restored.SafetyBackup == "" || restored.SafetyBackup == takeover.Backup {
		t.Fatal("public host restore did not retain its pre-restore safety backup", restored)
	}
	recoveryEqualSnapshot(t, before, recoverySnapshot(t, fixture.database))
	recoveryEqualSnapshot(t, after, recoverySnapshot(t, restored.SafetyBackup))
	recoveryRequire(t, keeper.Close())
	reopened, _ := hostRecoveryOpen(t, fixture.database, "open")
	recoveryRequire(t, reopened.Start(ctx))
	meeting, err := reopened.Meetings.Result(ctx, recoveryJobID)
	recoveryRequire(t, err)
	if len(meeting.Transcript) != 1 || meeting.Transcript[0].Text != "Before takeover" || len(meeting.SummaryRuns) != 1 || meeting.Job.ActiveSummaryRunID == nil || *meeting.Job.ActiveSummaryRunID != originalSummary.ID || meeting.SummaryRuns[0].MinutesPayload == nil {
		t.Fatal("public host reopen did not expose the restored transcript and selected minutes")
	}
	if _, err := reopened.Meetings.Result(ctx, recoveryAddedJobID); !errors.Is(err, application.ErrJobNotFound) {
		t.Fatal("public host reopen retained a post-takeover meeting", err)
	}
	members, err := reopened.Resources.Members(ctx)
	recoveryRequire(t, err)
	templates, err := reopened.Resources.Templates(ctx)
	recoveryRequire(t, err)
	matchedTemplate := false
	for _, candidate := range templates {
		if candidate.ID == template.ID {
			matchedTemplate = candidate == template
		}
		if candidate.ID == "host-added-template" {
			t.Fatal("public host reopen retained a post-takeover template")
		}
	}
	petStore, err := reopened.Pet.Store(ctx)
	recoveryRequire(t, err)
	pet, err := reopened.Pet.Profile(ctx)
	recoveryRequire(t, err)
	settings, err = reopened.Resources.Settings(ctx)
	recoveryRequire(t, err)
	farm, err := reopened.Work.Farm(ctx)
	recoveryRequire(t, err)
	mine, err := reopened.Work.Game(ctx, "mine")
	recoveryRequire(t, err)
	if len(members) != 1 || members[0].Name != "Before takeover" || !matchedTemplate || pet.Name != "Before takeover" || petStore.Wallet != originalStore.Wallet || settings.Locale != "zh-CN" || settings.TokenConfigured || len(farm.Harvests) != 1 || len(mine.Rewards) != 1 {
		t.Fatal("public host resource, pet or work APIs did not expose the original business snapshot")
	}
	hostRecoveryWithoutCredentials(t, fixture.database)
	t.Logf("public host prepare/handover/start/delete-recovery/restore/reopen verified with no credential references or vault substitution; exact %d-table rollback and WAL-inclusive safety backup", len(before))
}
