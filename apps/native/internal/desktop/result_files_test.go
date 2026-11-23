package desktop

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/fileout"
)

type resultFileDialogFixture struct {
	filename string
	err      error
	returned func()
	options  []mygo.SaveDialogOptions
}

func (dialog *resultFileDialogFixture) Save(options mygo.SaveDialogOptions) (string, error) {
	dialog.options = append(dialog.options, options)
	if dialog.returned != nil {
		dialog.returned()
	}
	return dialog.filename, dialog.err
}

type resultFileServiceFixture struct {
	result       domain.Result
	reads        int
	backfills    int
	rewards      int
	err          error
	read         func()
	rewardedFile string
}

func (service *resultFileServiceFixture) Result(context.Context, string) (domain.Result, error) {
	service.reads++
	if service.read != nil {
		service.read()
	}
	return service.result, service.err
}

func (*resultFileServiceFixture) Members(context.Context) ([]domain.MeetingMember, error) {
	return nil, nil
}

func (service *resultFileServiceFixture) BackfillMinutes(_ context.Context, _ string, _ string, payload domain.MinutesPayload) (bool, error) {
	service.backfills++
	service.result.SummaryRuns[0].MinutesPayload = &payload
	return true, nil
}

func (service *resultFileServiceFixture) Workflow(_ context.Context, event, key string) (domain.PetProfile, error) {
	if event != "export_completed" || key != service.result.Job.ID {
		return domain.PetProfile{}, domain.ErrPet
	}
	if info, err := os.Stat(service.rewardedFile); err != nil || !info.Mode().IsRegular() {
		return domain.PetProfile{}, domain.ErrPet
	}
	service.rewards++
	return domain.PetProfile{}, nil
}

func newResultFileGrant(t *testing.T) (*application.WindowScopes, *application.JobWindow, *resultFileServiceFixture) {
	t.Helper()
	runID := "synthetic-summary"
	speaker := "合成讲话人"
	service := &resultFileServiceFixture{result: domain.Result{
		Job:         domain.JobSummary{ID: "job-1700000000000-601", Source: "local", Title: "合成会议", DiarizationStatus: "completed", ActiveSummaryRunID: &runID},
		Speakers:    []domain.Segment{{ID: "synthetic-speaker", Speaker: &speaker, EndMS: 1000, Text: "Synthetic transcript"}},
		Summary:     domain.Summary{Overview: "Synthetic overview", Topics: []string{"Synthetic topic"}},
		SummaryRuns: []domain.SummaryRun{{ID: runID, Status: "completed", Result: &domain.SummaryResult{Overview: "Synthetic overview", Topics: []string{"Synthetic topic"}}}},
	}}
	scopes := application.NewWindowScopes(service)
	scopes.EnableWordExport(application.NewWordExport(service, service))
	scopes.EnableExportRewards(service)
	grant, err := scopes.OpenFromMain(context.Background(), application.Workbench, "local", service.result.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	service.reads = 0
	t.Cleanup(grant.Close)
	return scopes, grant, service
}

func resultFileDestination(t *testing.T, kind domain.TextExportKind) string {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	extension, err := kind.Extension()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(directory, "synthetic."+extension)
}

func assertResultFilePreserved(t *testing.T, filename string) {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil || string(data) != "Synthetic existing artifact" {
		t.Fatal("failed or revoked export changed the existing artifact", err)
	}
	entries, err := os.ReadDir(filepath.Dir(filename))
	if err != nil || len(entries) != 1 {
		t.Fatal("export left a temporary artifact", err)
	}
}

func TestNativeResultFilesRecheckCancellationAndGrantAfterSaveDialog(t *testing.T) {
	for _, kind := range []domain.TextExportKind{domain.ExportTranscript, domain.ExportNotes, domain.ExportBundle, domain.ExportWord} {
		for _, outcome := range []string{"cancel-before", "cancel-after", "dismiss", "dialog-error", "close", "replace"} {
			t.Run(string(kind)+"/"+outcome, func(t *testing.T) {
				scopes, grant, service := newResultFileGrant(t)
				filename := resultFileDestination(t, kind)
				if err := os.WriteFile(filename, []byte("Synthetic existing artifact"), 0600); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				dialog := &resultFileDialogFixture{filename: filename}
				var want error
				selected := false
				switch outcome {
				case "cancel-before":
					cancel()
					want = context.Canceled
				case "cancel-after":
					dialog.returned = cancel
					want = context.Canceled
				case "dismiss":
					dialog.filename = ""
				case "dialog-error":
					dialog.err = errors.New("synthetic native dialog private detail")
					want = fileout.ErrOutput
				case "close":
					dialog.returned = grant.Close
					want, selected = application.ErrWindowScope, true
				case "replace":
					dialog.returned = func() {
						replacement, err := scopes.OpenFromMain(context.Background(), application.Workbench, "local", service.result.Job.ID)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(replacement.Close)
						service.reads = 0
					}
					want, selected = application.ErrWindowScope, true
				}
				files := NativeResultFiles{Grant: grant, dialogs: dialog}
				gotSelected, err := files.Export(ctx, kind, "Synthetic meeting", "en-US")
				if !errors.Is(err, want) || gotSelected != selected || service.reads != 0 || service.backfills != 0 || service.rewards != 0 {
					t.Fatal("export escaped cancellation, dialog failure or revoked authorization", gotSelected, err, service.reads, service.backfills, service.rewards)
				}
				wantDialogs := 1
				if outcome == "cancel-before" {
					wantDialogs = 0
				}
				if len(dialog.options) != wantDialogs {
					t.Fatal("unexpected native dialog count")
				}
				assertResultFilePreserved(t, filename)
			})
		}
	}
}

func TestNativeResultFilesPublishFreshBoundContentAndRewardOnlyAfterSuccess(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, kind := range []domain.TextExportKind{domain.ExportTranscript, domain.ExportNotes, domain.ExportBundle, domain.ExportWord} {
			t.Run(locale+"/"+string(kind), func(t *testing.T) {
				_, grant, service := newResultFileGrant(t)
				filename := resultFileDestination(t, kind)
				service.rewardedFile = filename
				dialog := &resultFileDialogFixture{filename: filename, returned: func() {
					service.result.Speakers[0].Text = "Fresh bound transcript"
					service.result.Summary.Overview = "Fresh bound overview"
					service.result.SummaryRuns[0].Result.Overview = "Fresh bound overview"
				}}
				files := NativeResultFiles{Grant: grant, dialogs: dialog}
				selected, err := files.Export(context.Background(), kind, "  Synthetic / meeting:*?  ", locale)
				if err != nil || !selected || service.rewards != 1 || service.reads == 0 {
					t.Fatal("selected export did not publish before rewarding", selected, err, service.rewards)
				}
				extension, err := kind.Extension()
				if err != nil {
					t.Fatal(err)
				}
				suffix := string(kind)
				if kind == domain.ExportWord {
					suffix = "meeting-minutes"
				}
				options := dialog.options[0]
				if options.DefaultPath != "Synthetic _ meeting___-"+suffix+"."+extension || len(options.Filters) != 1 || !reflect.DeepEqual(options.Filters[0].Extensions, []string{extension}) {
					t.Fatal("save dialog did not constrain the artifact type", options.DefaultPath)
				}
				data, err := os.ReadFile(filename)
				if err != nil {
					t.Fatal(err)
				}
				if kind == domain.ExportWord {
					archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
					if err != nil {
						t.Fatal("Word export is not an OOXML archive", err)
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
						if readErr != nil || closeErr != nil || !bytes.Contains(content, []byte("Fresh bound overview")) || !bytes.Contains(content, []byte(service.result.Job.Title)) {
							t.Fatal("Word export lost the fresh authorized minutes")
						}
						found = true
					}
					if !found || service.backfills != 1 {
						t.Fatal("Word export did not reconstruct and preserve its minutes source")
					}
				} else {
					want, err := domain.RenderTextExport(service.result, kind, exportLabels(locale))
					if err != nil || string(data) != want || strings.Contains(string(data), "Synthetic overview") || service.backfills != 0 {
						t.Fatal("text export did not publish the fresh bound snapshot and language", err)
					}
				}
			})
		}
	}
}

func TestNativeResultFilesPreserveExistingArtifactOnReadOrWriteFailure(t *testing.T) {
	for _, kind := range []domain.TextExportKind{domain.ExportTranscript, domain.ExportWord} {
		for _, outcome := range []string{"read-failure", "cancel-during-read", "linked-output"} {
			t.Run(string(kind)+"/"+outcome, func(t *testing.T) {
				_, grant, service := newResultFileGrant(t)
				filename := resultFileDestination(t, kind)
				if err := os.WriteFile(filename, []byte("Synthetic existing artifact"), 0600); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				dialog := &resultFileDialogFixture{filename: filename}
				var want error
				switch outcome {
				case "read-failure":
					want = application.ErrJobNotFound
					service.err = want
				case "cancel-during-read":
					want = context.Canceled
					service.read = cancel
				case "linked-output":
					linkedDirectory, err := filepath.EvalSymlinks(t.TempDir())
					if err != nil {
						t.Fatal(err)
					}
					dialog.filename = filepath.Join(linkedDirectory, "linked-artifact")
					if err := os.Symlink(filename, dialog.filename); err != nil {
						t.Skip("symlink unavailable")
					}
					want = fileout.ErrOutput
				}
				service.rewardedFile = filename
				files := NativeResultFiles{Grant: grant, dialogs: dialog}
				selected, err := files.Export(ctx, kind, "Synthetic meeting", "zh-CN")
				if !selected || !errors.Is(err, want) || service.rewards != 0 {
					t.Fatal("failed export reported success or awarded a reward", selected, err, service.rewards)
				}
				assertResultFilePreserved(t, filename)
			})
		}
	}
}
