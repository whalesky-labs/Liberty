package desktop

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
	"liberty.local/liberty/internal/infrastructure/repositories/legacydb"
)

type persistedResultFiles struct {
	database string
	store    *jobdb.Store
	reader   *legacydb.Reader
	grant    *application.JobWindow
}

var resultFileJobIDs = []string{"job-1700000000000-611", "job-1700000000000-612"}

func newPersistedResultFiles(t *testing.T) *persistedResultFiles {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture := &persistedResultFiles{database: filepath.Join(directory, "result-export.sqlite3")}
	ctx := context.Background()
	fixture.store, _, err = jobdb.OpenPrepared(ctx, fixture.database, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fixture.close)
	for _, jobID := range resultFileJobIDs {
		job := domain.JobSummary{ID: jobID, Title: "合成会议 " + jobID, CreatedAt: "2026-11-20T00:00:00Z", Lang: "zh", EnableSpeaker: true, SourceFiles: []domain.SourceFile{{ID: jobID + "-file", Name: "synthetic.wav", Kind: "audio"}}}
		if err := fixture.store.Create(ctx, domain.JobInput{Job: job, SourcePath: filepath.Join(directory, "synthetic.wav")}); err != nil {
			t.Fatal(err)
		}
		lease, err := fixture.store.Begin(ctx, jobID, 1)
		if err != nil {
			t.Fatal(err)
		}
		speaker := "原讲话人"
		result := domain.RunnerResult{ProtocolVersion: 2, ASRBackend: "funasr", DiarizationStatus: "completed", Warnings: []domain.RunnerWarning{}, Transcript: []domain.Segment{{ID: jobID + "-transcript", EndMS: 1000, Text: "Synthetic transcript " + jobID}}, Speakers: []domain.Segment{{ID: jobID + "-speaker", Speaker: &speaker, EndMS: 1000, Text: "Synthetic transcript " + jobID}}}
		if err := fixture.store.Complete(ctx, domain.Completion{Lease: lease, FinishedAtMS: 2, Result: result}); err != nil {
			t.Fatal(err)
		}
	}
	if err := fixture.store.EnsurePet(ctx, "2026-11-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	seed, err := sql.Open("sqlite", fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	summary := domain.SummaryResult{Title: "合成会议", Overview: "Synthetic persisted overview", Topics: []string{"Synthetic persisted topic"}, Decisions: []string{}, ActionItems: []domain.SummaryAction{}, Risks: []string{}, FollowUps: []string{}}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Exec(`INSERT INTO ai_summary_runs(id,job_id,status,result_json,created_at,updated_at) VALUES('synthetic-export-summary',?,'completed',?,'2026-11-20T00:00:00Z','2026-11-20T00:00:00Z')`, resultFileJobIDs[0], string(encoded)); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Exec(`UPDATE jobs SET active_summary_run_id='synthetic-export-summary',summary_status='completed' WHERE id=?`, resultFileJobIDs[0]); err != nil {
		t.Fatal(err)
	}
	fixture.attach(t)
	return fixture
}

func (fixture *persistedResultFiles) attach(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	var err error
	fixture.reader, err = legacydb.Open(ctx, fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	meetings := application.NewMeetings(fixture.reader)
	scopes := application.NewWindowScopes(meetings)
	scopes.EnableWordExport(application.NewWordExport(meetings, fixture.store))
	scopes.EnableExportRewards(application.NewPet(fixture.store))
	fixture.grant, err = scopes.OpenFromMain(ctx, application.Workbench, "local", resultFileJobIDs[0])
	if err != nil {
		t.Fatal(err)
	}
}

func (fixture *persistedResultFiles) close() {
	if fixture.grant != nil {
		fixture.grant.Close()
	}
	if fixture.reader != nil {
		_ = fixture.reader.Close()
	}
	if fixture.store != nil {
		_ = fixture.store.Close()
	}
}

func (fixture *persistedResultFiles) reopen(t *testing.T) {
	t.Helper()
	fixture.close()
	var err error
	fixture.store, err = jobdb.Open(context.Background(), fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	fixture.attach(t)
}

func (fixture *persistedResultFiles) assertExportRewards(t *testing.T, want int) {
	t.Helper()
	events, err := fixture.store.PetEvents(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.EventType == "workflow" && event.EventSource == "export_completed" {
			if event.Metadata == nil || !strings.Contains(*event.Metadata, resultFileJobIDs[0]) {
				t.Fatal("export reward lost its bound task identity")
			}
			count++
		}
	}
	if count != want {
		t.Fatal("export reward was missing, early or duplicated", count, want)
	}
}

func TestNativeResultFilesPersistBoundMinutesAndDeduplicateRewardsAcrossReopen(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			fixture := newPersistedResultFiles(t)
			ctx := context.Background()
			unrelated, err := fixture.reader.ReadResult(ctx, resultFileJobIDs[1])
			if err != nil {
				t.Fatal(err)
			}
			fixture.assertExportRewards(t, 0)
			dismissed := NativeResultFiles{Grant: fixture.grant, dialogs: &resultFileDialogFixture{}}
			if selected, err := dismissed.Export(ctx, domain.ExportWord, "Synthetic", locale); selected || err != nil {
				t.Fatal("dismissed export did not remain a no-op", selected, err)
			}
			fixture.reopen(t)
			fixture.assertExportRewards(t, 0)
			previousSpeaker := "原讲话人"
			for _, operation := range []struct {
				kind   domain.TextExportKind
				rename bool
			}{{domain.ExportTranscript, true}, {domain.ExportBundle, true}, {domain.ExportNotes, true}, {domain.ExportWord, true}, {domain.ExportWord, false}} {
				kind := operation.kind
				filename := resultFileDestination(t, kind)
				currentSpeaker := previousSpeaker
				if operation.rename {
					currentSpeaker += "甲"
				}
				dialog := &resultFileDialogFixture{filename: filename, returned: func() {
					if operation.rename {
						if err := fixture.store.RenameSpeaker(ctx, resultFileJobIDs[0], previousSpeaker, currentSpeaker); err != nil {
							t.Fatal(err)
						}
					}
				}}
				files := NativeResultFiles{Grant: fixture.grant, dialogs: dialog}
				if selected, err := files.Export(ctx, kind, "Synthetic persisted meeting", locale); err != nil || !selected {
					t.Fatal("real repository export failed", kind, selected, err)
				}
				data, err := os.ReadFile(filename)
				if err != nil {
					t.Fatal(err)
				}
				if kind == domain.ExportWord {
					archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for _, part := range archive.File {
						if part.Name != "word/document.xml" {
							continue
						}
						reader, err := part.Open()
						if err != nil {
							t.Fatal(err)
						}
						content, readErr := io.ReadAll(reader)
						closeErr := reader.Close()
						if readErr != nil || closeErr != nil || !bytes.Contains(content, []byte(currentSpeaker)) || !bytes.Contains(content, []byte("Synthetic persisted overview")) {
							t.Fatal("Word artifact did not reflect the persisted task and renamed speaker")
						}
						found = true
					}
					if !found {
						t.Fatal("Word artifact has no main document")
					}
				} else {
					content := string(data)
					if kind != domain.ExportNotes && (!strings.Contains(content, currentSpeaker+": Synthetic transcript "+resultFileJobIDs[0]) || strings.Contains(content, resultFileJobIDs[1])) {
						t.Fatal("text artifact used a stale speaker or another task")
					}
					if kind != domain.ExportTranscript && (!strings.Contains(content, "Synthetic persisted overview") || !strings.Contains(content, exportLabels(locale).SummaryHeading)) {
						t.Fatal("notes artifact lost the persisted summary or selected language")
					}
				}
				previousSpeaker = currentSpeaker
				fixture.reopen(t)
				fixture.assertExportRewards(t, 1)
				persistedUnrelated, err := fixture.reader.ReadResult(ctx, resultFileJobIDs[1])
				if err != nil || !reflect.DeepEqual(persistedUnrelated, unrelated) {
					t.Fatal("export changed an unrelated persisted task", err)
				}
				if kind == domain.ExportWord {
					run, err := fixture.store.SummaryRun(ctx, resultFileJobIDs[0], "synthetic-export-summary")
					if err != nil || run.MinutesPayload == nil || run.MinutesPayload.SourceSummaryRunID == nil || *run.MinutesPayload.SourceSummaryRunID != run.ID {
						t.Fatal("reconstructed minutes did not survive reopening with their original summary identity", err)
					}
				}
			}
		})
	}
}
