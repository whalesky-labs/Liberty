package application

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

type permissionPorts struct {
	reads, capabilities, writes int
	source, job, from, to       string
	onRead                      func()
	result                      *domain.Result
}

func (ports *permissionPorts) Result(ctx context.Context, id string) (domain.Result, error) {
	return ports.ResultForSource(ctx, "local", id)
}

func (ports *permissionPorts) ResultForSource(_ context.Context, source, id string) (domain.Result, error) {
	ports.reads++
	ports.source, ports.job = source, id
	if ports.onRead != nil {
		ports.onRead()
	}
	if ports.result != nil {
		return *ports.result, nil
	}
	speaker := "original"
	run := "run-1"
	return domain.Result{Job: domain.JobSummary{ID: id, Source: source, DiarizationStatus: "completed", ActiveSummaryRunID: &run}, Speakers: []domain.Segment{{Speaker: &speaker, Text: "Synthetic transcript"}}, SummaryRuns: []domain.SummaryRun{{ID: run, JobID: id, Status: "completed", Result: &domain.SummaryResult{Overview: "Synthetic overview"}}}}, nil
}

func (ports *permissionPorts) RenameSpeaker(ctx context.Context, id, from, to string) error {
	return ports.RenameSpeakerForSource(ctx, "local", id, from, to)
}

func (ports *permissionPorts) RenameSpeakerForSource(_ context.Context, source, id, from, to string) error {
	ports.writes++
	ports.source, ports.job, ports.from, ports.to = source, id, from, to
	return nil
}

func (ports *permissionPorts) RemoteCapabilities(context.Context) (domain.RemoteCapabilities, error) {
	ports.capabilities++
	return domain.RemoteCapabilities{ServiceVersion: "synthetic"}, nil
}

func permissionGrant(t *testing.T, role WindowRole, source string) (*WindowScopes, *JobWindow, *permissionPorts) {
	t.Helper()
	ports := &permissionPorts{}
	scopes := NewWindowScopes(ports)
	scopes.EnableEditing(ports)
	grant, err := scopes.OpenFromMain(context.Background(), role, source, scopedID)
	if err != nil {
		t.Fatal(err)
	}
	ports.reads = 0
	return scopes, grant, ports
}

func TestWindowCanceledReadsDoNotDelegate(t *testing.T) {
	for _, operation := range []string{"open", "result", "capabilities"} {
		t.Run(operation, func(t *testing.T) {
			scopes, grant, ports := permissionGrant(t, Workbench, "remote")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var err error
			switch operation {
			case "open":
				_, err = scopes.OpenFromMain(ctx, Workbench, "remote", scopedID)
			case "result":
				_, err = grant.Result(ctx, scopedID)
			case "capabilities":
				_, err = grant.RemoteCapabilities(ctx)
			}
			if !errors.Is(err, context.Canceled) || ports.reads != 0 || ports.capabilities != 0 {
				t.Fatalf("canceled observation delegated: err=%v reads=%d capabilities=%d", err, ports.reads, ports.capabilities)
			}
			if _, err := grant.Result(context.Background(), scopedID); err != nil {
				t.Fatal("canceled issuance replaced the current grant", err)
			}
		})
	}
}

func TestWindowRenameCanceledDuringReadDoesNotDelegateMutation(t *testing.T) {
	_, grant, ports := permissionGrant(t, Workbench, "local")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ports.onRead = cancel
	err := grant.RenameSpeaker(ctx, "original", "replacement")
	if !errors.Is(err, context.Canceled) || ports.writes != 0 {
		t.Fatalf("canceled pre-mutation read reached writer: err=%v writes=%d", err, ports.writes)
	}
}

type permissionMinutesRepository struct {
	members, backfills int
}

func (repository *permissionMinutesRepository) Members(context.Context) ([]domain.MeetingMember, error) {
	repository.members++
	return nil, nil
}

func (repository *permissionMinutesRepository) BackfillMinutes(context.Context, string, string, domain.MinutesPayload) (bool, error) {
	repository.backfills++
	return true, nil
}

type permissionEffects struct {
	reads, writes, capabilities, publications, rewards, members, backfills, options int
	summaries                                                                       []string
}

func TestWindowPermissionDelegationMatrix(t *testing.T) {
	for _, window := range []struct {
		role   WindowRole
		source string
	}{{Workbench, "local"}, {Workbench, "remote"}, {AISummary, "local"}, {MeetingNotes, "local"}} {
		for _, lifetime := range []string{"active", "replaced", "closed", "forged", "nil"} {
			for _, operation := range []string{"result", "other-job", "rename", "text", "word", "capabilities", "ai-child", "notes-child", "summary-state", "generate", "select", "delete"} {
				t.Run(string(window.role)+"/"+window.source+"/"+lifetime+"/"+operation, func(t *testing.T) {
					ctx := context.Background()
					scopes, grant, ports := permissionGrant(t, window.role, window.source)
					summaries, resources := enablePermissionSummary(t, scopes)
					minutes := &permissionMinutesRepository{}
					scopes.EnableWordExport(NewWordExport(ports, minutes))
					effects := permissionEffects{}
					scopes.EnableExportRewards(workflowFixture(func(_ context.Context, event, job string) (domain.PetProfile, error) {
						if event != "export_completed" || job != scopedID || effects.publications != 1 {
							t.Error("reward preceded publication or used the wrong job")
						}
						effects.rewards++
						return domain.PetProfile{}, nil
					}))
					switch lifetime {
					case "replaced":
						if _, err := scopes.OpenFromMain(ctx, window.role, window.source, scopedID); err != nil {
							t.Fatal(err)
						}
						grant.Close()
					case "closed":
						grant.Close()
					case "forged":
						grant = &JobWindow{owner: scopes, role: window.role, source: window.source, id: scopedID}
					case "nil":
						grant = nil
					}
					ports.reads = 0
					allowed := lifetime == "active"
					want := permissionEffects{}
					var err error
					switch operation {
					case "result", "other-job":
						id := scopedID
						if operation == "other-job" {
							id += "1"
							allowed = false
						}
						var result domain.Result
						result, err = grant.Result(ctx, id)
						if allowed {
							want.reads = 1
							if result.Job.ID != scopedID || result.Job.Source != window.source {
								t.Error("read returned another job or source")
							}
						}
					case "rename":
						allowed = allowed && window.role == Workbench
						err = grant.RenameSpeaker(ctx, "original", "replacement")
						if allowed {
							want.reads, want.writes = 1, 1
							if ports.from != "original" || ports.to != "replacement" {
								t.Error("rename arguments were not delegated")
							}
						}
					case "text":
						allowed = allowed && window.role == Workbench
						err = grant.ExportText(ctx, domain.ExportTranscript, domain.TextExportLabels{}, func(content []byte) error {
							if !strings.Contains(string(content), "Synthetic transcript") {
								t.Error("export lost the bound transcript")
							}
							effects.publications++
							return nil
						})
						if allowed {
							want.reads, want.publications, want.rewards = 1, 1, 1
						}
					case "word":
						allowed = allowed && window.role == Workbench && window.source == "local"
						err = grant.ExportWord(ctx, "run-1", func(document domain.MinutesDocument) error {
							if len(document.SpeechBlocks) == 0 {
								t.Error("Word export lost the summary")
							}
							effects.publications++
							return nil
						})
						if allowed {
							want.reads, want.publications, want.rewards, want.members, want.backfills = 1, 1, 1, 1, 1
						}
					case "capabilities":
						allowed = allowed && window.role == Workbench && window.source == "remote"
						_, err = grant.RemoteCapabilities(ctx)
						if allowed {
							want.capabilities = 1
						}
					case "ai-child", "notes-child":
						allowed = allowed && window.role == Workbench && window.source == "local"
						role := AISummary
						if operation == "notes-child" {
							role = MeetingNotes
						}
						var child *JobWindow
						child, err = grant.OpenChild(role)
						if allowed && err == nil {
							_, err = child.Result(ctx, scopedID)
							want.reads = 1
							if child.role != role || child.source != "local" || child.id != scopedID {
								t.Error("issued child has the wrong scope")
							}
						}
					case "summary-state", "generate", "select", "delete":
						allowed = allowed && window.role == AISummary && window.source == "local"
						switch operation {
						case "summary-state":
							_, err = grant.SummaryState(ctx)
							if allowed {
								want.reads, want.options, want.summaries = 1, 1, []string{"list"}
							}
						case "generate":
							err = grant.GenerateSummary(ctx, SummaryForm{RunID: "run-1"})
							if allowed {
								want.summaries = []string{"resume"}
							}
						case "select":
							err = grant.SelectSummary(ctx, "run-1")
							if allowed {
								want.summaries = []string{"select"}
							}
						case "delete":
							err = grant.DeleteSummary(ctx, "run-1")
							if allowed {
								want.summaries = []string{"delete"}
							}
						}
						if allowed && (summaries.job != scopedID || (operation != "summary-state" && summaries.run != "run-1")) {
							t.Error("summary operation used another job or run")
						}
					}
					if (allowed && err != nil) || (!allowed && !errors.Is(err, ErrWindowScope)) {
						t.Fatalf("allowed=%v err=%v", allowed, err)
					}
					effects.reads, effects.writes, effects.capabilities = ports.reads, ports.writes, ports.capabilities
					effects.members, effects.backfills, effects.options, effects.summaries = minutes.members, minutes.backfills, resources.reads, summaries.calls
					if !reflect.DeepEqual(effects, want) {
						t.Fatalf("unexpected delegation: got %+v want %+v", effects, want)
					}
					if want.reads > 0 && (ports.job != scopedID || ports.source != window.source) {
						t.Error("delegation crossed the bound job or source")
					}
				})
			}
		}
	}
}

func TestWindowRejectedIssuancePreservesCurrentGrant(t *testing.T) {
	for _, invalid := range []struct {
		role   WindowRole
		source string
		id     string
	}{{AISummary, "remote", scopedID}, {MeetingNotes, "remote", scopedID}, {Workbench, "other", scopedID}, {WindowRole("model-editor"), "local", scopedID}, {Workbench, "local", "../job"}, {Workbench, "remote", strings.Repeat("x", 257)}} {
		t.Run(string(invalid.role)+"/"+invalid.source+"/"+invalid.id, func(t *testing.T) {
			scopes, grant, ports := permissionGrant(t, Workbench, "local")
			if _, err := scopes.OpenFromMain(context.Background(), invalid.role, invalid.source, invalid.id); !errors.Is(err, ErrWindowScope) || ports.reads != 0 {
				t.Fatal("invalid issuance reached the result reader", err, ports.reads)
			}
			if _, err := grant.Result(context.Background(), scopedID); err != nil {
				t.Fatal("invalid issuance revoked the existing grant", err)
			}
		})
	}
}

func TestWindowMismatchedSnapshotsCannotMutateOrExport(t *testing.T) {
	for _, mismatch := range []string{"job", "source", "diarization"} {
		for _, operation := range []string{"result", "rename", "text", "summary"} {
			if mismatch == "diarization" && operation != "rename" {
				continue
			}
			t.Run(mismatch+"/"+operation, func(t *testing.T) {
				role := Workbench
				if operation == "summary" {
					role = AISummary
				}
				scopes, grant, ports := permissionGrant(t, role, "local")
				repository, resources := enablePermissionSummary(t, scopes)
				result, _ := ports.Result(context.Background(), scopedID)
				switch mismatch {
				case "job":
					result.Job.ID += "1"
				case "source":
					result.Job.Source = "remote"
				case "diarization":
					result.Job.DiarizationStatus = "unavailable"
				}
				ports.result = &result
				publications := 0
				var err error
				switch operation {
				case "result":
					_, err = grant.Result(context.Background(), scopedID)
				case "rename":
					err = grant.RenameSpeaker(context.Background(), "original", "replacement")
				case "text":
					err = grant.ExportText(context.Background(), domain.ExportTranscript, domain.TextExportLabels{}, func([]byte) error { publications++; return nil })
				case "summary":
					_, err = grant.SummaryState(context.Background())
				}
				if !errors.Is(err, ErrWindowScope) || ports.writes != 0 || publications != 0 || resources.reads != 0 || len(repository.calls) != 0 {
					t.Fatal("mismatched result crossed a permission boundary", err)
				}
			})
		}
	}
}

type permissionSpeakerWriter func(context.Context, string, string, string) error

func (write permissionSpeakerWriter) RenameSpeaker(ctx context.Context, job, from, to string) error {
	return write(ctx, job, from, to)
}

func waitPermissionSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("permission operation did not finish")
	}
}

func TestWindowAcceptedRenameFinishesBeforeRevocation(t *testing.T) {
	scopes, grant, ports := permissionGrant(t, Workbench, "local")
	entered, release, closing, closed := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseWriter := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseWriter)
	scopes.EnableEditing(permissionSpeakerWriter(func(_ context.Context, job, from, to string) error {
		if job != scopedID || from != "original" || to != "replacement" {
			t.Error("accepted mutation changed scope")
		}
		close(entered)
		<-release
		return nil
	}))
	completed := make(chan error, 1)
	go func() { completed <- grant.RenameSpeaker(context.Background(), "original", "replacement") }()
	waitPermissionSignal(t, entered)
	go func() { close(closing); grant.Close(); close(closed) }()
	waitPermissionSignal(t, closing)
	select {
	case <-closed:
		t.Error("revocation overtook an accepted mutation")
	default:
	}
	releaseWriter()
	select {
	case err := <-completed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("accepted rename did not finish")
	}
	waitPermissionSignal(t, closed)
	reads := ports.reads
	if _, err := grant.Result(context.Background(), scopedID); !errors.Is(err, ErrWindowScope) || ports.reads != reads {
		t.Fatal("closed grant performed another read", err)
	}
}

func TestWindowAcceptedPublicationsAreNotUndoneByCallerCancellation(t *testing.T) {
	for _, operation := range []string{"rename", "text"} {
		t.Run(operation, func(t *testing.T) {
			scopes, grant, _ := permissionGrant(t, Workbench, "local")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			publications := 0
			publish := func() error { publications++; cancel(); return nil }
			var err error
			if operation == "rename" {
				scopes.EnableEditing(permissionSpeakerWriter(func(context.Context, string, string, string) error { return publish() }))
				err = grant.RenameSpeaker(ctx, "original", "replacement")
			} else {
				err = grant.ExportText(ctx, domain.ExportTranscript, domain.TextExportLabels{}, func([]byte) error { return publish() })
			}
			if err != nil || publications != 1 {
				t.Fatal("successful publication was reported as an unaccepted operation", err, publications)
			}
		})
	}
}
