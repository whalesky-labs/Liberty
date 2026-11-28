package host

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
	"liberty.local/liberty/internal/infrastructure/repositories/schema"
)

const recoveryJobID = "job-1700000000000-701"
const recoveryAddedJobID = "job-1700000000000-702"
const recoverySummaryRaw = `{"choices":[{"finish_reason":"stop","message":{"content":"{\"title\":\"Synthetic recovery\",\"overview\":\"Synthetic recovery overview\",\"topics\":[],\"decisions\":[],\"actionItems\":[],\"risks\":[],\"followUps\":[],\"speakerReports\":[],\"globalSummary\":[]}"}}]}`

type recoveryVault struct {
	mu              sync.Mutex
	values          map[string]domain.Secret
	readErrors      map[string]error
	writes, deletes int
	reads           int
	onRead          func() error
}

func (vault *recoveryVault) Get(_ context.Context, reference string) (domain.Secret, error) {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	vault.reads++
	if vault.onRead != nil {
		if err := vault.onRead(); err != nil {
			return domain.Secret{}, err
		}
	}
	if err := vault.readErrors[reference]; err != nil {
		return domain.Secret{}, err
	}
	return vault.values[reference], nil
}

func (vault *recoveryVault) Set(_ context.Context, reference string, secret domain.Secret) error {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	vault.writes++
	vault.values[reference] = secret
	return nil
}

func (vault *recoveryVault) Delete(_ context.Context, reference string) error {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	vault.deletes++
	delete(vault.values, reference)
	return nil
}

type recoverySummaryProvider func(context.Context, domain.SummaryRequest) (string, error)

func (provider recoverySummaryProvider) Complete(ctx context.Context, request domain.SummaryRequest) (string, error) {
	return provider(ctx, request)
}

type recoveryRunner struct{ text string }

func (runner recoveryRunner) Run(_ context.Context, _ domain.JobInput, _ domain.RunLease, callbacks application.ExecutionCallbacks) (domain.RunnerResult, error) {
	if err := callbacks.Heartbeat(); err != nil {
		return domain.RunnerResult{}, err
	}
	return domain.RunnerResult{ProtocolVersion: 2, ASRBackend: "funasr", DiarizationStatus: "disabled", Warnings: []domain.RunnerWarning{}, Transcript: []domain.Segment{{ID: "synthetic-segment", EndMS: 1000, Text: runner.text}}, Speakers: []domain.Segment{}}, nil
}

type recoveryFixture struct {
	database string
	vault    *recoveryVault
	now      time.Time
	model    domain.AIModel
	template domain.SummaryTemplate
	summary  domain.SummaryRun
}

func (fixture *recoveryFixture) open(test *testing.T, mode string) (*Profile, schema.Result) {
	test.Helper()
	ctx := context.Background()
	var store *jobdb.Store
	var result schema.Result
	var err error
	switch mode {
	case "create":
		store, result, err = jobdb.OpenPrepared(ctx, fixture.database, fixture.vault, true)
	case "handover":
		store, result, err = jobdb.OpenHandover(ctx, fixture.database, fixture.vault, false)
	case "open":
		store, err = jobdb.Open(ctx, fixture.database)
	default:
		test.Fatal("unknown synthetic profile mode")
	}
	recoveryRequire(test, err)
	profileHost, err := assemble(ctx, fixture.database, store)
	recoveryRequire(test, err)
	test.Cleanup(profileHost.Close)
	profileHost.Summary.Close()
	profileHost.Resources.Close()
	profileHost.Resources = application.NewResources(store, fixture.vault)
	profileHost.Summary = application.NewSummaryService(profileHost.Meetings, store, profileHost.Resources, recoverySummaryProvider(func(context.Context, domain.SummaryRequest) (string, error) {
		return recoverySummaryRaw, nil
	}), profileHost.Pet)
	profileHost.Work = application.NewWork(store, func() time.Time { return fixture.now })
	return profileHost, result
}

func newRecoveryFixture(test *testing.T) (*recoveryFixture, *Profile) {
	test.Helper()
	ctx := context.Background()
	fixture := &recoveryFixture{database: filepath.Join(test.TempDir(), "synthetic #资料.sqlite3"), vault: &recoveryVault{values: map[string]domain.Secret{}}, now: time.Date(2026, 11, 19, 3, 0, 0, 0, time.UTC)}
	profileHost, prepared := fixture.open(test, "create")
	if prepared.Version != 13 {
		test.Fatal("synthetic profile is not v13", prepared.Version)
	}
	recoveryRequire(test, profileHost.Start(ctx))
	var err error
	fixture.template, err = profileHost.Resources.SaveTemplate(ctx, domain.SummaryTemplate{ID: "synthetic-recovery-template", Name: "Before takeover", Prompt: "Summarize the synthetic meeting."})
	recoveryRequire(test, err)
	_, err = profileHost.Resources.SaveMember(ctx, domain.MeetingMember{ID: "synthetic-recovery-member", Name: "Before takeover", Department: "Synthetic", IsRecorder: true})
	recoveryRequire(test, err)
	var outcome application.MutationOutcome
	fixture.model, outcome, err = profileHost.Resources.SaveModel(ctx, domain.AIModel{ID: "synthetic-recovery-model", Name: "Before takeover", BaseURL: "https://synthetic.invalid", Model: "synthetic", Enabled: true, IsDefault: true}, domain.CredentialUpdate{Action: domain.SetCredential, Value: domain.NewSecret("synthetic-recovery-model-secret")})
	recoveryRequire(test, err)
	if outcome.CleanupPending {
		test.Fatal("synthetic model cleanup pending")
	}
	settings, err := profileHost.Resources.Settings(ctx)
	recoveryRequire(test, err)
	settings.BackendURL = "https://synthetic.invalid"
	_, outcome, err = profileHost.Resources.SaveSettings(ctx, settings, domain.CredentialUpdate{Action: domain.SetCredential, Value: domain.NewSecret("synthetic-recovery-token-secret")})
	recoveryRequire(test, err)
	if outcome.CleanupPending {
		test.Fatal("synthetic token cleanup pending")
	}
	modelRecord, err := profileHost.store.Model(ctx, fixture.model.ID)
	recoveryRequire(test, err)
	settingsRecord, err := profileHost.store.Settings(ctx)
	recoveryRequire(test, err)
	if modelRecord.Reference == "" || settingsRecord.Reference == "" || modelRecord.LegacyPresent || settingsRecord.LegacyPresent {
		test.Fatal("synthetic credentials were not stored as references only")
	}
	_, err = profileHost.Pet.SaveName(ctx, "Before takeover")
	recoveryRequire(test, err)
	_, err = profileHost.Pet.ClaimCheckIn(ctx)
	recoveryRequire(test, err)
	fixture.job(test, profileHost, recoveryJobID, "Before takeover")
	fixture.summary = fixture.summarize(test, profileHost, recoveryJobID)
	fixture.workCycle(test, profileHost)
	return fixture, profileHost
}

func (fixture *recoveryFixture) job(test *testing.T, profileHost *Profile, id, text string) {
	test.Helper()
	ctx := context.Background()
	media := filepath.Join(filepath.Dir(fixture.database), "synthetic.wav")
	recoveryRequire(test, os.WriteFile(media, []byte("synthetic fixture"), 0600))
	recoveryRequire(test, profileHost.store.Create(ctx, domain.JobInput{Job: domain.JobSummary{ID: id, Title: text, CreatedAt: domain.PersistedUTC(fixture.now), Lang: "zh-CN", SourceFiles: []domain.SourceFile{{ID: id + "-file", Name: "synthetic.wav", Kind: "audio"}}}, SourcePath: media}))
	_, err := profileHost.Pet.Workflow(ctx, "job_created", id)
	recoveryRequire(test, err)
	fixture.execute(test, profileHost, id, text)
}

func (fixture *recoveryFixture) execute(test *testing.T, profileHost *Profile, id, text string) {
	test.Helper()
	executor := application.NewExecutor(profileHost.store, recoveryRunner{text: text}, func() time.Time { return fixture.now }, profileHost.Pet)
	recoveryRequire(test, executor.Execute(context.Background(), id))
}

func (fixture *recoveryFixture) summarize(test *testing.T, profileHost *Profile, jobID string) domain.SummaryRun {
	test.Helper()
	run, err := profileHost.Summary.Start(context.Background(), jobID, application.SummaryForm{ModelID: fixture.model.ID, TemplateID: fixture.template.ID})
	recoveryRequire(test, err)
	return recoveryAwaitSummary(test, profileHost, jobID, run.ID)
}

func recoveryAwaitSummary(test *testing.T, profileHost *Profile, jobID, runID string) domain.SummaryRun {
	test.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		run, err := profileHost.store.SummaryRun(context.Background(), jobID, runID)
		recoveryRequire(test, err)
		if run.Status == "failed" {
			test.Fatal("synthetic summary failed")
		}
		events, err := profileHost.Pet.Events(context.Background(), 100)
		recoveryRequire(test, err)
		rewards := 0
		for _, event := range events {
			var metadata struct{ Source string }
			if event.EventSource == "ai_summary_completed" && event.Metadata != nil && json.Unmarshal([]byte(*event.Metadata), &metadata) == nil && metadata.Source == runID {
				rewards++
			}
		}
		if run.Status == "completed" && rewards == 1 {
			if run.Result == nil || run.MinutesPayload == nil {
				test.Fatal("summary completed without result/minutes")
			}
			return run
		}
		time.Sleep(time.Millisecond)
	}
	test.Fatal("summary publication/reward did not complete exactly once")
	return domain.SummaryRun{}
}

func (fixture *recoveryFixture) workCycle(test *testing.T, profileHost *Profile) {
	test.Helper()
	ctx := context.Background()
	_, err := profileHost.Pet.Purchase(ctx, "wheat-seed", 1)
	recoveryRequire(test, err)
	_, err = profileHost.Work.Plant(ctx, "farm-plot-1", "wheat")
	recoveryRequire(test, err)
	fixture.now = fixture.now.Add(150 * time.Second)
	_, err = profileHost.Work.Water(ctx, "farm-plot-1")
	recoveryRequire(test, err)
	fixture.now = fixture.now.Add(150 * time.Second)
	harvest, err := profileHost.Work.Harvest(ctx, "farm-plot-1")
	recoveryRequire(test, err)
	_, err = profileHost.Work.Start(ctx, "mine", "mine-slot-1", "shallow-vein")
	recoveryRequire(test, err)
	fixture.now = fixture.now.Add(300 * time.Second)
	_, err = profileHost.Work.Care(ctx, "mine", "mine-slot-1")
	recoveryRequire(test, err)
	fixture.now = fixture.now.Add(300 * time.Second)
	claim, err := profileHost.Work.Claim(ctx, "mine", "mine-slot-1")
	recoveryRequire(test, err)
	if harvest.Harvest.ID == "" || claim.Reward.ID == "" || harvest.Harvest.LPReward <= 0 || claim.Reward.LPReward <= 0 {
		test.Fatal("synthetic work cycle did not publish its rewards")
	}
}

func recoveryRequire(test *testing.T, err error) {
	test.Helper()
	if err != nil {
		test.Fatal(err)
	}
}

func recoverySQL(test *testing.T, database, mode string) *sql.DB {
	test.Helper()
	connection, err := sql.Open("sqlite", schema.URI(database, url.Values{"mode": {mode}}))
	recoveryRequire(test, err)
	connection.SetMaxOpenConns(1)
	test.Cleanup(func() { _ = connection.Close() })
	return connection
}

func recoverySnapshot(test *testing.T, database string) map[string][]string {
	test.Helper()
	connection := recoverySQL(test, database, "ro")
	defer connection.Close()
	transaction, err := connection.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	recoveryRequire(test, err)
	defer transaction.Rollback()
	var integrity string
	recoveryRequire(test, transaction.QueryRow("PRAGMA integrity_check").Scan(&integrity))
	if integrity != "ok" {
		test.Fatal("synthetic recovery integrity check failed")
	}
	violations, err := transaction.Query("PRAGMA foreign_key_check")
	recoveryRequire(test, err)
	if violations.Next() {
		violations.Close()
		test.Fatal("synthetic recovery contains a broken relationship")
	}
	recoveryRequire(test, violations.Err())
	recoveryRequire(test, violations.Close())
	tables, err := transaction.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	recoveryRequire(test, err)
	var names []string
	for tables.Next() {
		var name string
		recoveryRequire(test, tables.Scan(&name))
		names = append(names, name)
	}
	recoveryRequire(test, tables.Err())
	recoveryRequire(test, tables.Close())
	snapshot := map[string][]string{}
	for _, name := range names {
		rows, err := transaction.Query(`SELECT * FROM "` + strings.ReplaceAll(name, `"`, `""`) + `"`)
		recoveryRequire(test, err)
		columns, err := rows.Columns()
		recoveryRequire(test, err)
		snapshot[name] = []string{}
		for rows.Next() {
			values := make([]any, len(columns))
			targets := make([]any, len(columns))
			for index := range values {
				targets[index] = &values[index]
			}
			recoveryRequire(test, rows.Scan(targets...))
			encoded, err := json.Marshal(values)
			recoveryRequire(test, err)
			snapshot[name] = append(snapshot[name], string(encoded))
		}
		recoveryRequire(test, rows.Err())
		recoveryRequire(test, rows.Close())
		sort.Strings(snapshot[name])
	}
	recoveryRequire(test, transaction.Commit())
	return snapshot
}

func recoveryEqualSnapshot(test *testing.T, expected, actual map[string][]string) {
	test.Helper()
	if len(expected) != len(actual) {
		test.Fatalf("recovery table count: got %d, want %d", len(actual), len(expected))
	}
	for table, rows := range expected {
		if !reflect.DeepEqual(rows, actual[table]) {
			test.Errorf("recovery changed %s: got %d rows, want the original %d rows", table, len(actual[table]), len(rows))
		}
	}
	if test.Failed() {
		test.FailNow()
	}
}

func recoveryExpectedRestoreIntent(test *testing.T, expected, actual map[string][]string, reference string) map[string][]string {
	test.Helper()
	if len(expected["credential_cleanup_intents"]) != 0 || len(actual["credential_cleanup_intents"]) != 1 {
		test.Fatal("restore must preserve the business snapshot and add exactly one operational retirement intent")
	}
	var row []any
	recoveryRequire(test, json.Unmarshal([]byte(actual["credential_cleanup_intents"][0]), &row))
	digest := sha256.Sum256([]byte(reference))
	if len(row) != 8 || row[0] != "restore-"+hex.EncodeToString(digest[:]) || row[1] != "" || row[2] != reference || row[3] != "retire" || row[4] != float64(0) || row[5] != nil || row[6] != row[7] {
		test.Fatal("restored operational intent has unexpected identity, owner, reference, operation, failure metadata or timestamps")
	}
	timestamp, ok := row[6].(string)
	if !ok {
		test.Fatal("restored intent timestamp is not text")
	}
	_, err := time.Parse(time.RFC3339Nano, timestamp)
	recoveryRequire(test, err)
	want := make(map[string][]string, len(expected))
	for table, rows := range expected {
		want[table] = rows
	}
	want["credential_cleanup_intents"] = actual["credential_cleanup_intents"]
	return want
}

func (fixture *recoveryFixture) restore(test *testing.T, backup string) (schema.RestoreResult, error) {
	test.Helper()
	owner, err := profile.Acquire(filepath.Dir(fixture.database))
	recoveryRequire(test, err)
	defer owner.Close()
	checks := 0
	fixture.vault.mu.Lock()
	writes, deletes := fixture.vault.writes, fixture.vault.deletes
	fixture.vault.onRead = func() error {
		other, err := profile.Acquire(filepath.Dir(fixture.database))
		if other != nil {
			other.Close()
		}
		if !errors.Is(err, profile.ErrInUse) {
			return errors.New("restore read credentials before obtaining profile ownership")
		}
		checks++
		return nil
	}
	fixture.vault.mu.Unlock()
	result, restoreErr := schema.RestoreLocked(context.Background(), fixture.database, filepath.Base(backup), fixture.vault)
	fixture.vault.mu.Lock()
	fixture.vault.onRead = nil
	changed := writes != fixture.vault.writes || deletes != fixture.vault.deletes
	fixture.vault.mu.Unlock()
	if checks == 0 || (restoreErr == nil && checks != 2) || changed {
		test.Fatal("restore bypassed synthetic credential preflight or mutated the vault")
	}
	return result, restoreErr
}

func TestHandoverRecoveryRestoresBusinessSnapshotAndRetainsWALChanges(t *testing.T) {
	ctx := context.Background()
	fixture, initial := newRecoveryFixture(t)
	originalTemplate := fixture.template
	originalStore, err := initial.Pet.Store(ctx)
	recoveryRequire(t, err)
	initial.Close()
	before := recoverySnapshot(t, fixture.database)
	for _, table := range []string{"jobs", "job_runs", "job_events", "transcript_segments", "ai_summary_runs", "meeting_members", "ai_summary_templates", "ai_model_configs", "app_settings", "pet_profile", "pet_event_ledger", "pet_wallets", "pet_inventory", "pet_economy_ledger", "farm_harvest_ledger", "work_game_reward_ledger"} {
		if len(before[table]) == 0 {
			t.Fatal("synthetic recovery fixture did not populate", table)
		}
	}
	handover, result := fixture.open(t, "handover")
	if result.FromVersion != 13 || result.Version != 13 || result.Backup == "" {
		t.Fatal("first takeover did not record a v13 recovery point", result)
	}
	recoveryEqualSnapshot(t, before, recoverySnapshot(t, result.Backup))
	originalBackup, err := os.ReadFile(result.Backup)
	recoveryRequire(t, err)
	recoveryRequire(t, handover.Start(ctx))
	_, err = handover.Resources.SaveMember(ctx, domain.MeetingMember{ID: "synthetic-recovery-member", Name: "After takeover", Department: "Synthetic changed", IsRecorder: true})
	recoveryRequire(t, err)
	_, err = handover.Resources.SaveMember(ctx, domain.MeetingMember{ID: "synthetic-added-member", Name: "Added after takeover"})
	recoveryRequire(t, err)
	fixture.template.Name, fixture.template.Prompt = "After takeover", "Summarize the changed synthetic meeting."
	fixture.template, err = handover.Resources.SaveTemplate(ctx, fixture.template)
	recoveryRequire(t, err)
	_, err = handover.Resources.SaveTemplate(ctx, domain.SummaryTemplate{ID: "synthetic-added-template", Name: "Added after takeover", Prompt: "Synthetic additional template."})
	recoveryRequire(t, err)
	fixture.model.Name = "After takeover"
	_, outcome, err := handover.Resources.SaveModel(ctx, fixture.model, domain.CredentialUpdate{Action: domain.KeepCredential})
	recoveryRequire(t, err)
	if outcome.CleanupPending {
		t.Fatal("synthetic metadata update left cleanup pending")
	}
	settings, err := handover.Resources.Settings(ctx)
	recoveryRequire(t, err)
	settings.Locale = "en-US"
	_, _, err = handover.Resources.SaveSettings(ctx, settings, domain.CredentialUpdate{Action: domain.KeepCredential})
	recoveryRequire(t, err)
	_, err = handover.Pet.SaveName(ctx, "After takeover")
	recoveryRequire(t, err)
	fixture.now = fixture.now.Add(time.Hour)
	fixture.workCycle(t, handover)
	recoveryRequire(t, handover.store.Retry(ctx, recoveryJobID, "", ""))
	fixture.execute(t, handover, recoveryJobID, "Changed after takeover")
	fixture.summarize(t, handover, recoveryJobID)
	fixture.job(t, handover, recoveryAddedJobID, "Added after takeover")
	handover.Summary.Close()
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseProvider := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseProvider)
	handover.Summary = application.NewSummaryService(handover.Meetings, handover.store, handover.Resources, recoverySummaryProvider(func(ctx context.Context, request domain.SummaryRequest) (string, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return "", ctx.Err()
	}), handover.Pet)
	interrupted, err := handover.Summary.Start(ctx, recoveryAddedJobID, application.SummaryForm{ModelID: fixture.model.ID, TemplateID: fixture.template.ID})
	recoveryRequire(t, err)
	recoverySignal(t, entered)
	closed := make(chan struct{})
	go func() { handover.Close(); close(closed) }()
	recoverySignal(t, canceled)
	owner, err := profile.Acquire(filepath.Dir(fixture.database))
	if owner != nil {
		owner.Close()
	}
	if !errors.Is(err, profile.ErrInUse) {
		t.Fatal("shutdown released ownership before summary worker drained", err)
	}
	if _, err := RestoreProfile(ctx, fixture.database, filepath.Base(result.Backup)); !errors.Is(err, profile.ErrInUse) {
		t.Fatal("restore bypassed the draining host", err)
	}
	releaseProvider()
	recoverySignal(t, closed)
	restarted, repeated := fixture.open(t, "handover")
	if repeated.Backup != result.Backup {
		t.Fatal("restart replaced the first takeover recovery point")
	}
	pending, err := restarted.store.SummaryRun(ctx, recoveryAddedJobID, interrupted.ID)
	recoveryRequire(t, err)
	if pending.Status != "running" || pending.Result != nil {
		t.Fatal("interrupted summary was falsely reported completed")
	}
	var recoveredCalls atomic.Int32
	restarted.Summary.Close()
	restarted.Summary = application.NewSummaryService(restarted.Meetings, restarted.store, restarted.Resources, recoverySummaryProvider(func(context.Context, domain.SummaryRequest) (string, error) {
		recoveredCalls.Add(1)
		return recoverySummaryRaw, nil
	}), restarted.Pet)
	recoveryRequire(t, restarted.Start(ctx))
	recoveryAwaitSummary(t, restarted, recoveryAddedJobID, interrupted.ID)
	recoveryRequire(t, restarted.Start(ctx))
	if recoveredCalls.Load() != 1 {
		t.Fatal("startup failed to resume exactly one interrupted summary")
	}
	keeper := recoverySQL(t, fixture.database, "rw")
	defer keeper.Close()
	_, err = keeper.Exec("PRAGMA wal_autocheckpoint=0")
	recoveryRequire(t, err)
	var busy, frames, checkpointed int
	recoveryRequire(t, keeper.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &frames, &checkpointed))
	if busy != 0 {
		t.Fatal("synthetic WAL fixture could not checkpoint")
	}
	_, err = restarted.Resources.SaveMember(ctx, domain.MeetingMember{ID: "synthetic-wal-only-member", Name: "Committed only in WAL"})
	recoveryRequire(t, err)
	restarted.Close()
	after := recoverySnapshot(t, fixture.database)
	if len(after["jobs"]) != 2 || len(after["farm_harvest_ledger"]) != 2 || len(after["work_game_reward_ledger"]) != 2 || reflect.DeepEqual(before, after) {
		t.Fatal("post-takeover multi-domain writes were not committed")
	}
	wal, err := os.Stat(fixture.database + "-wal")
	recoveryRequire(t, err)
	if wal.Size() <= 32 {
		t.Fatal("synthetic WAL fixture has no committed frames")
	}
	mainOnly := filepath.Join(t.TempDir(), "main-only.sqlite3")
	mainBytes, err := os.ReadFile(fixture.database)
	recoveryRequire(t, err)
	recoveryRequire(t, os.WriteFile(mainOnly, mainBytes, 0600))
	mainSnapshot := recoverySnapshot(t, mainOnly)
	if len(after["meeting_members"]) != len(mainSnapshot["meeting_members"])+1 {
		t.Fatal("late synthetic member was not isolated to committed WAL frames")
	}
	restored, err := fixture.restore(t, result.Backup)
	recoveryRequire(t, err)
	if restored.RestoredVersion != 13 || restored.SafetyBackup == "" || restored.SafetyBackup == result.Backup {
		t.Fatal("restore did not retain a separate pre-restore safety backup", restored)
	}
	recoveryEqualSnapshot(t, before, recoverySnapshot(t, fixture.database))
	recoveryEqualSnapshot(t, after, recoverySnapshot(t, restored.SafetyBackup))
	unchangedBackup, err := os.ReadFile(result.Backup)
	recoveryRequire(t, err)
	if sha256.Sum256(originalBackup) != sha256.Sum256(unchangedBackup) {
		t.Fatal("restore changed the original takeover recovery point")
	}
	recoveryRequire(t, keeper.Close())
	reopened, _ := fixture.open(t, "open")
	recoveryRequire(t, reopened.Start(ctx))
	meeting, err := reopened.Meetings.Result(ctx, recoveryJobID)
	recoveryRequire(t, err)
	if len(meeting.Transcript) != 1 || meeting.Transcript[0].Text != "Before takeover" || len(meeting.SummaryRuns) != 1 || meeting.Job.ActiveSummaryRunID == nil || *meeting.Job.ActiveSummaryRunID != fixture.summary.ID || meeting.SummaryRuns[0].MinutesPayload == nil {
		t.Fatal("reopened meeting did not expose the original transcript and selected minutes")
	}
	if _, err := reopened.Meetings.Result(ctx, recoveryAddedJobID); !errors.Is(err, application.ErrJobNotFound) {
		t.Fatal("restored profile retained a post-takeover meeting", err)
	}
	members, err := reopened.Resources.Members(ctx)
	recoveryRequire(t, err)
	pet, err := reopened.Pet.Profile(ctx)
	recoveryRequire(t, err)
	models, err := reopened.Resources.Models(ctx)
	recoveryRequire(t, err)
	templates, err := reopened.Resources.Templates(ctx)
	recoveryRequire(t, err)
	matchedTemplate := false
	for _, template := range templates {
		if template.ID == originalTemplate.ID {
			matchedTemplate = template == originalTemplate
		}
		if template.ID == "synthetic-added-template" {
			t.Fatal("restored profile retained a post-takeover template")
		}
	}
	petStore, err := reopened.Pet.Store(ctx)
	recoveryRequire(t, err)
	settings, err = reopened.Resources.Settings(ctx)
	recoveryRequire(t, err)
	farm, err := reopened.Work.Farm(ctx)
	recoveryRequire(t, err)
	mine, err := reopened.Work.Game(ctx, "mine")
	recoveryRequire(t, err)
	if len(members) != 1 || members[0].Name != "Before takeover" || pet.Name != "Before takeover" || len(models) != 1 || models[0].Name != "Before takeover" || !models[0].CredentialPresent || !settings.TokenConfigured || settings.Locale != "zh-CN" || len(farm.Harvests) != 1 || len(mine.Rewards) != 1 {
		t.Fatal("reopened resources, credential references, pet or work ledgers did not match the recovery point")
	}
	if !matchedTemplate || petStore.Wallet != originalStore.Wallet {
		t.Fatal("reopened template or wallet did not match the recovery point")
	}
	t.Logf("v13 takeover, interrupted-summary recovery, shutdown ownership, WAL-only write and exact %d-table rollback/safety-backup comparison verified", len(before))
}

func recoverySignal(test *testing.T, signal <-chan struct{}) {
	test.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		test.Fatal("synthetic recovery stage did not complete")
	}
}

type recoveryCredentialRotation struct {
	previous, current             string
	previousSecret, currentSecret domain.Secret
	intentID                      string
	writes, deletes               int
}

func recoveryCredentialReference(test *testing.T, profileHost *Profile, credential string) string {
	test.Helper()
	if credential == "model" {
		record, err := profileHost.store.Model(context.Background(), "synthetic-recovery-model")
		recoveryRequire(test, err)
		return record.Reference
	}
	if credential != "token" {
		test.Fatal("unknown synthetic credential")
	}
	record, err := profileHost.store.Settings(context.Background())
	recoveryRequire(test, err)
	return record.Reference
}

func recoveryRotateCredential(test *testing.T, fixture *recoveryFixture, profileHost *Profile, credential string) recoveryCredentialRotation {
	test.Helper()
	ctx := context.Background()
	rotation := recoveryCredentialRotation{previous: recoveryCredentialReference(test, profileHost, credential), currentSecret: domain.NewSecret("synthetic-rotated-" + credential + "-secret")}
	var err error
	rotation.previousSecret, err = fixture.vault.Get(ctx, rotation.previous)
	recoveryRequire(test, err)
	fixture.vault.mu.Lock()
	deletes := fixture.vault.deletes
	fixture.vault.mu.Unlock()
	update := domain.CredentialUpdate{Action: domain.SetCredential, Value: rotation.currentSecret}
	var outcome application.MutationOutcome
	if credential == "model" {
		_, outcome, err = profileHost.Resources.SaveModel(ctx, fixture.model, update)
	} else {
		settings, readErr := profileHost.Resources.Settings(ctx)
		recoveryRequire(test, readErr)
		_, outcome, err = profileHost.Resources.SaveSettings(ctx, settings, update)
	}
	recoveryRequire(test, err)
	if outcome.CleanupPending {
		test.Fatal("normal recovery-point retention was reported as failed credential cleanup")
	}
	rotation.current = recoveryCredentialReference(test, profileHost, credential)
	if rotation.previous == "" || rotation.previousSecret.Empty() || rotation.current == "" || rotation.current == rotation.previous {
		test.Fatal("credential rotation did not publish a new synthetic reference")
	}
	pending, err := profileHost.store.PendingCredentials(ctx)
	recoveryRequire(test, err)
	if len(pending) != 1 || pending[0].Reference != rotation.previous || pending[0].Operation != "retire" {
		test.Fatal("rotation discarded the retirement intent while the recovery point still needs its credential")
	}
	rotation.intentID = pending[0].ID
	fixture.vault.mu.Lock()
	rotation.writes, rotation.deletes = fixture.vault.writes, fixture.vault.deletes
	fixture.vault.mu.Unlock()
	if rotation.deletes != deletes {
		test.Fatal("rotation deleted a recovery-point credential")
	}
	recoveryRetainedCredential(test, fixture, profileHost, rotation)
	return rotation
}

func recoveryRetainedCredential(test *testing.T, fixture *recoveryFixture, profileHost *Profile, rotation recoveryCredentialRotation) {
	test.Helper()
	previous, err := fixture.vault.Get(context.Background(), rotation.previous)
	recoveryRequire(test, err)
	current, err := fixture.vault.Get(context.Background(), rotation.current)
	recoveryRequire(test, err)
	if previous != rotation.previousSecret || current != rotation.currentSecret {
		test.Fatal("recovery-point or live credential was lost or overwritten")
	}
	pending, err := profileHost.store.PendingCredentials(context.Background())
	recoveryRequire(test, err)
	if len(pending) != 1 || pending[0].ID != rotation.intentID || pending[0].Reference != rotation.previous || pending[0].Operation != "retire" {
		test.Fatal("deferred recovery-point retirement intent was not retained")
	}
	connection := recoverySQL(test, fixture.database, "ro")
	defer connection.Close()
	var attempt int64
	var lastError sql.NullString
	recoveryRequire(test, connection.QueryRowContext(context.Background(), "SELECT attempt,last_error FROM credential_cleanup_intents WHERE id=?", rotation.intentID).Scan(&attempt, &lastError))
	if attempt != 0 || lastError.Valid {
		test.Fatal("normal recovery-point retention changed credential cleanup failure metadata")
	}
	fixture.vault.mu.Lock()
	changed := fixture.vault.writes != rotation.writes || fixture.vault.deletes != rotation.deletes
	fixture.vault.mu.Unlock()
	if changed {
		test.Fatal("deferred credential recovery mutated the vault")
	}
}

func recoveryDeferredCleanup(test *testing.T, profileHost *Profile) {
	test.Helper()
	err := profileHost.Resources.RecoverCredentials(context.Background())
	if err != nil {
		test.Fatal("valid recovery-point retention failed", err)
	}
}

func TestHandoverRecoveryPreservesRotatedSnapshotCredentialsAndRestoresBusinessData(t *testing.T) {
	for _, credential := range []string{"model", "token"} {
		t.Run(credential, func(test *testing.T) {
			ctx := context.Background()
			fixture, initial := newRecoveryFixture(test)
			initial.Close()
			original := recoverySnapshot(test, fixture.database)
			handover, result := fixture.open(test, "handover")
			recoveryRequire(test, handover.Start(ctx))
			rotation := recoveryRotateCredential(test, fixture, handover, credential)
			for range 2 {
				recoveryDeferredCleanup(test, handover)
				recoveryRetainedCredential(test, fixture, handover, rotation)
			}
			fixture.job(test, handover, recoveryAddedJobID, "Retain in pre-restore safety backup")
			handover.Close()
			after := recoverySnapshot(test, fixture.database)
			restored, err := fixture.restore(test, result.Backup)
			recoveryRequire(test, err)
			if restored.RestoredVersion != 13 || restored.SafetyBackup == "" {
				test.Fatal("normal credential rotation made its recovery point unusable", restored)
			}
			current := recoverySnapshot(test, fixture.database)
			recoveryEqualSnapshot(test, recoveryExpectedRestoreIntent(test, original, current, rotation.current), current)
			recoveryEqualSnapshot(test, after, recoverySnapshot(test, restored.SafetyBackup))
			reopened, _ := fixture.open(test, "open")
			recoveryRequire(test, reopened.Start(ctx))
			if recoveryCredentialReference(test, reopened, credential) != rotation.previous {
				test.Fatal("host reopen did not select the restored credential reference")
			}
			secret, err := fixture.vault.Get(ctx, rotation.previous)
			recoveryRequire(test, err)
			if secret != rotation.previousSecret {
				test.Fatal("restored credential does not resolve to the original secret")
			}
			models, err := reopened.Resources.Models(ctx)
			recoveryRequire(test, err)
			settings, err := reopened.Resources.Settings(ctx)
			recoveryRequire(test, err)
			meeting, err := reopened.Meetings.Result(ctx, recoveryJobID)
			recoveryRequire(test, err)
			if len(models) != 1 || !models[0].CredentialPresent || !settings.TokenConfigured || len(meeting.SummaryRuns) != 1 || meeting.SummaryRuns[0].ID != fixture.summary.ID || meeting.SummaryRuns[0].MinutesPayload == nil {
				test.Fatal("host reopen lost restored credentials or completed minutes")
			}
			if _, err := reopened.Meetings.Result(ctx, recoveryAddedJobID); !errors.Is(err, application.ErrJobNotFound) {
				test.Fatal("restored profile retained the post-rotation meeting", err)
			}
			test.Logf("%s rotation retains the old credential and retirement intent; restore/reopen and exact %d-table rollback/safety backup verified", credential, len(original))
		})
	}
}

func TestHandoverRecoveryRetiresPostSnapshotCredentialsAfterRestoreAndLastSafetyRemoval(t *testing.T) {
	for _, credential := range []string{"model", "token"} {
		for _, removal := range []string{"before-reopen", "after-reopen"} {
			t.Run(credential+"/"+removal, func(test *testing.T) {
				ctx := context.Background()
				fixture, initial := newRecoveryFixture(test)
				initial.Close()
				original := recoverySnapshot(test, fixture.database)
				handover, takeover := fixture.open(test, "handover")
				recoveryRequire(test, handover.Start(ctx))
				rotation := recoveryRotateCredential(test, fixture, handover, credential)
				handover.Close()
				after := recoverySnapshot(test, fixture.database)
				restored, err := fixture.restore(test, takeover.Backup)
				recoveryRequire(test, err)
				current := recoverySnapshot(test, fixture.database)
				recoveryEqualSnapshot(test, recoveryExpectedRestoreIntent(test, original, current, rotation.current), current)
				recoveryEqualSnapshot(test, after, recoverySnapshot(test, restored.SafetyBackup))
				var reopened *Profile
				if removal == "after-reopen" {
					reopened, _ = fixture.open(test, "open")
					recoveryRequire(test, reopened.Start(ctx))
					pending, err := reopened.store.PendingCredentials(ctx)
					recoveryRequire(test, err)
					if len(pending) != 1 || pending[0].Reference != rotation.current || pending[0].Operation != "retire" {
						test.Fatal("reopen discarded the post-snapshot retirement intent")
					}
					additional := fixture.database + ".pre-restore-v13-synthetic-second-safety.bak"
					data, err := os.ReadFile(restored.SafetyBackup)
					recoveryRequire(test, err)
					recoveryRequire(test, os.WriteFile(additional, data, 0600))
					recoveryRequire(test, os.Remove(restored.SafetyBackup))
					recoveryDeferredCleanup(test, reopened)
					secret, err := fixture.vault.Get(ctx, rotation.current)
					recoveryRequire(test, err)
					if secret != rotation.currentSecret {
						test.Fatal("cleanup deleted a credential still needed by the second safety snapshot")
					}
					recoveryRequire(test, os.Remove(additional))
				} else {
					recoveryRequire(test, os.Remove(restored.SafetyBackup))
					reopened, _ = fixture.open(test, "open")
					recoveryRequire(test, reopened.Start(ctx))
				}
				beforeCleanup := recoverySnapshot(test, fixture.database)
				for range 2 {
					recoveryRequire(test, reopened.Resources.RecoverCredentials(ctx))
				}
				previous, err := fixture.vault.Get(ctx, rotation.previous)
				recoveryRequire(test, err)
				retired, err := fixture.vault.Get(ctx, rotation.current)
				recoveryRequire(test, err)
				pending, err := reopened.store.PendingCredentials(ctx)
				recoveryRequire(test, err)
				fixture.vault.mu.Lock()
				changed := fixture.vault.writes != rotation.writes || fixture.vault.deletes != rotation.deletes+1
				fixture.vault.mu.Unlock()
				if previous != rotation.previousSecret || !retired.Empty() || len(pending) != 0 || changed || recoveryCredentialReference(test, reopened, credential) != rotation.previous {
					test.Fatal("restore cleanup did not retire exactly the post-snapshot credential once")
				}
				reopened.Close()
				beforeCleanup["credential_cleanup_intents"] = []string{}
				recoveryEqualSnapshot(test, beforeCleanup, recoverySnapshot(test, fixture.database))
				recoveryEqualSnapshot(test, original, recoverySnapshot(test, takeover.Backup))
				test.Logf("%s: restore/reopen and safety removal %s preserve all %d tables; only the post-snapshot credential is reclaimed exactly once", credential, removal, len(original))
			})
		}
	}
}

func TestHandoverRecoveryRejectsUnavailableSnapshotCredentialsWithoutChangingBusinessData(t *testing.T) {
	for _, credential := range []string{"model", "token"} {
		for _, failure := range []string{"lost", "revoked"} {
			t.Run(credential+"/"+failure, func(test *testing.T) {
				ctx := context.Background()
				fixture, initial := newRecoveryFixture(test)
				initial.Close()
				handover, result := fixture.open(test, "handover")
				recoveryRequire(test, handover.Start(ctx))
				reference := recoveryCredentialReference(test, handover, credential)
				fixture.job(test, handover, recoveryAddedJobID, "Retain after failed restore")
				handover.Close()
				before := recoverySnapshot(test, fixture.database)
				fixture.vault.mu.Lock()
				if failure == "lost" {
					delete(fixture.vault.values, reference)
				} else {
					fixture.vault.readErrors = map[string]error{reference: domain.ErrCredentialStore}
				}
				fixture.vault.mu.Unlock()
				restored, err := fixture.restore(test, result.Backup)
				if !errors.Is(err, schema.ErrRestore) || restored.SafetyBackup != "" {
					test.Fatal("unavailable snapshot credential was falsely accepted as a successful restore", restored, err)
				}
				recoveryEqualSnapshot(test, before, recoverySnapshot(test, fixture.database))
				reopened, _ := fixture.open(test, "open")
				recoveryRequire(test, reopened.Start(ctx))
				meeting, err := reopened.Meetings.Result(ctx, recoveryAddedJobID)
				recoveryRequire(test, err)
				if len(meeting.Transcript) != 1 || meeting.Transcript[0].Text != "Retain after failed restore" {
					test.Fatal("failed restore lost the new meeting")
				}
				test.Logf("externally %s %s credential rejects restore before safety-backup creation and preserves all %d current tables", failure, credential, len(before))
			})
		}
	}
}

func TestHandoverRecoveryReclaimsRetiredCredentialsOnlyAfterLastSnapshotRemoval(t *testing.T) {
	for _, credential := range []string{"model", "token"} {
		t.Run(credential, func(test *testing.T) {
			ctx := context.Background()
			fixture, initial := newRecoveryFixture(test)
			initial.Close()
			handover, result := fixture.open(test, "handover")
			recoveryRequire(test, handover.Start(ctx))
			additionalBackup := fixture.database + ".pre-restore-v13-synthetic-retention.bak"
			backupBytes, err := os.ReadFile(result.Backup)
			recoveryRequire(test, err)
			recoveryRequire(test, os.WriteFile(additionalBackup, backupBytes, 0600))
			recoveryEqualSnapshot(test, recoverySnapshot(test, result.Backup), recoverySnapshot(test, additionalBackup))
			rotation := recoveryRotateCredential(test, fixture, handover, credential)
			recoveryRequire(test, os.Remove(result.Backup))
			recoveryRetainedCredential(test, fixture, handover, rotation)
			recoveryDeferredCleanup(test, handover)
			recoveryRetainedCredential(test, fixture, handover, rotation)
			recoveryRequire(test, os.Remove(additionalBackup))
			recoveryRetainedCredential(test, fixture, handover, rotation)
			beforeCleanup := recoverySnapshot(test, fixture.database)
			recoveryRequire(test, handover.Resources.RecoverCredentials(ctx))
			previous, err := fixture.vault.Get(ctx, rotation.previous)
			recoveryRequire(test, err)
			current, err := fixture.vault.Get(ctx, rotation.current)
			recoveryRequire(test, err)
			pending, err := handover.store.PendingCredentials(ctx)
			recoveryRequire(test, err)
			if !previous.Empty() || current != rotation.currentSecret || len(pending) != 0 {
				test.Fatal("last snapshot removal did not allow retirement of only the old credential")
			}
			recoveryRequire(test, handover.Resources.RecoverCredentials(ctx))
			fixture.vault.mu.Lock()
			changed := fixture.vault.writes != rotation.writes || fixture.vault.deletes != rotation.deletes+1
			fixture.vault.mu.Unlock()
			if changed {
				test.Fatal("credential reclamation was not a single idempotent deletion")
			}
			afterCleanup := recoverySnapshot(test, fixture.database)
			delete(beforeCleanup, "credential_cleanup_intents")
			delete(afterCleanup, "credential_cleanup_intents")
			recoveryEqualSnapshot(test, beforeCleanup, afterCleanup)
			handover.Close()
			blocked, _, err := jobdb.OpenHandover(ctx, fixture.database, fixture.vault, false)
			if blocked != nil {
				blocked.Close()
			}
			if !errors.Is(err, schema.ErrBackup) {
				test.Fatal("handover accepted a missing SHA-marked recovery point", err)
			}
			reopened, _ := fixture.open(test, "open")
			recoveryRequire(test, reopened.Start(ctx))
			models, err := reopened.Resources.Models(ctx)
			recoveryRequire(test, err)
			settings, err := reopened.Resources.Settings(ctx)
			recoveryRequire(test, err)
			if recoveryCredentialReference(test, reopened, credential) != rotation.current || len(models) != 1 || !models[0].CredentialPresent || !settings.TokenConfigured {
				test.Fatal("reclamation changed a live credential or the reopened host")
			}
			test.Logf("%s retirement survives either valid recovery point with CleanupPending=false and attempt=0/last_error=NULL; removing the last snapshot allows one cleanup; missing SHA-marked pre-native backup rejects OpenHandover while Open verifies the cleanup lifecycle", credential)
		})
	}
}

func TestHandoverRecoveryImportsOldSchemaJournalBeforeResources(t *testing.T) {
	ctx := context.Background()
	fixture, initial := newRecoveryFixture(t)
	want := []string{recoveryCredentialReference(t, initial, "model"), recoveryCredentialReference(t, initial, "token")}
	sort.Strings(want)
	initial.Close()
	oldSnapshot := fixture.database + ".pre-native-v0-synthetic-empty.bak"
	empty := recoverySQL(t, oldSnapshot, "rwc")
	_, err := empty.Exec("PRAGMA user_version=1")
	recoveryRequire(t, err)
	recoveryRequire(t, empty.Close())
	owner, err := profile.Acquire(filepath.Dir(fixture.database))
	recoveryRequire(t, err)
	restored, err := schema.RestoreLocked(ctx, fixture.database, filepath.Base(oldSnapshot), fixture.vault)
	recoveryRequire(t, owner.Close())
	recoveryRequire(t, err)
	if restored.RestoredVersion != 0 || restored.SafetyBackup == "" {
		t.Fatal("empty legacy snapshot was not restored with a separate safety backup")
	}
	journals, err := filepath.Glob(fixture.database + ".restore-credentials-*.json")
	recoveryRequire(t, err)
	if len(journals) != 1 {
		t.Fatal("legacy restore did not retain its independent retirement journal")
	}
	recoveryRequire(t, os.Remove(restored.SafetyBackup))
	store, err := jobdb.Open(ctx, fixture.database)
	if store != nil {
		store.Close()
	}
	if err == nil {
		t.Fatal("unprepared Open accepted a legacy database")
	}
	if _, err := os.Stat(journals[0]); err != nil {
		t.Fatal("rejected old-schema open discarded the retirement journal", err)
	}
	store, result, err := jobdb.OpenPrepared(ctx, fixture.database, fixture.vault, false)
	recoveryRequire(t, err)
	defer store.Close()
	if result.FromVersion != 0 || result.Version != 13 {
		t.Fatal("legacy preparation did not reach v13 before intent import")
	}
	pending, err := store.PendingCredentials(ctx)
	recoveryRequire(t, err)
	var got []string
	for _, intent := range pending {
		if intent.Operation != "retire" || intent.Owner != "" {
			t.Fatal("old-schema preparation imported unexpected operational intent metadata")
		}
		got = append(got, intent.Reference)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("old-schema upgrade lost a retired live reference")
	}
	if _, err := os.Lstat(journals[0]); !os.IsNotExist(err) {
		t.Fatal("durably imported legacy journal was not removed", err)
	}
	recoveryRequire(t, store.Close())
	reopened, _ := fixture.open(t, "open")
	recoveryRequire(t, reopened.Start(ctx))
	pending, err = reopened.store.PendingCredentials(ctx)
	recoveryRequire(t, err)
	if len(pending) != 0 {
		t.Fatal("resources were assembled without the imported retirement intents")
	}
	for _, reference := range want {
		secret, err := fixture.vault.Get(ctx, reference)
		recoveryRequire(t, err)
		if !secret.Empty() {
			t.Fatal("last safety removal before old-schema reopen orphaned a credential")
		}
	}
}

func TestHandoverRecoveryOpenRetriesJournalImportWithoutLosingProfileOwnership(t *testing.T) {
	for _, mode := range []string{"open", "prepared"} {
		t.Run(mode, func(test *testing.T) {
			ctx := context.Background()
			database := filepath.Join(test.TempDir(), "journal #资料.sqlite3")
			store, _, err := jobdb.OpenPrepared(ctx, database, nil, true)
			recoveryRequire(test, err)
			recoveryRequire(test, store.Close())
			journal := database + ".restore-credentials-" + strings.Repeat("a", 32) + ".json"
			data, err := json.Marshal(map[string]any{"version": 1, "database": filepath.Base(database), "references": []string{"synthetic-retirement-reference"}})
			recoveryRequire(test, err)
			recoveryRequire(test, os.WriteFile(journal, data, 0600))
			connection := recoverySQL(test, database, "rw")
			_, err = connection.Exec(`CREATE TRIGGER reject_restore_intent BEFORE INSERT ON credential_cleanup_intents BEGIN SELECT RAISE(ABORT,'synthetic startup import failure'); END;`)
			recoveryRequire(test, err)
			recoveryRequire(test, connection.Close())
			open := func() (*jobdb.Store, error) {
				if mode == "open" {
					return jobdb.Open(ctx, database)
				}
				value, _, err := jobdb.OpenPrepared(ctx, database, nil, false)
				return value, err
			}
			store, err = open()
			if store != nil {
				store.Close()
			}
			if !errors.Is(err, schema.ErrRestore) {
				test.Fatal("open ignored an incomplete retirement journal import", err)
			}
			owner, err := profile.Acquire(filepath.Dir(database))
			recoveryRequire(test, err)
			recoveryRequire(test, owner.Close())
			if actual, err := os.ReadFile(journal); err != nil || string(actual) != string(data) {
				test.Fatal("failed open discarded or altered the journal", err)
			}
			connection = recoverySQL(test, database, "rw")
			var count int
			recoveryRequire(test, connection.QueryRow("SELECT COUNT(*) FROM credential_cleanup_intents").Scan(&count))
			if count != 0 {
				test.Fatal("failed startup partially imported retirement intents")
			}
			_, err = connection.Exec("DROP TRIGGER reject_restore_intent")
			recoveryRequire(test, err)
			recoveryRequire(test, connection.Close())
			store, err = open()
			recoveryRequire(test, err)
			defer store.Close()
			pending, err := store.PendingCredentials(ctx)
			recoveryRequire(test, err)
			if len(pending) != 1 || pending[0].Reference != "synthetic-retirement-reference" || pending[0].Operation != "retire" {
				test.Fatal("startup retry did not import the original retirement reference")
			}
			if _, err := os.Lstat(journal); !os.IsNotExist(err) {
				test.Fatal("successful startup retry retained its imported side journal", err)
			}
		})
	}
}
