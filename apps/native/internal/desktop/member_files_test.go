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
	"testing"

	"github.com/egoist/mygo"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/memberxlsx"
)

type memberDialogFixture struct {
	paths      []string
	filename   string
	err        error
	returned   func()
	open, save int
}

func TestNativeMemberImportRejectsMalformedXMLAfterValidPersonnel(t *testing.T) {
	for _, suffix := range []string{"<worksheet/>", "<broken", "unexpected text"} {
		t.Run(suffix, func(t *testing.T) {
			values := []domain.MeetingMember{{Name: "合成人员", Department: "合成部门", SortOrder: 7}}
			data, err := memberxlsx.Encode(values)
			if err != nil {
				t.Fatal(err)
			}
			original, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			writer := zip.NewWriter(&output)
			for _, part := range original.File {
				reader, err := part.Open()
				if err != nil {
					t.Fatal(err)
				}
				content, readErr := io.ReadAll(reader)
				closeErr := reader.Close()
				if readErr != nil || closeErr != nil {
					t.Fatal("failed to read synthetic fixture part")
				}
				if part.Name == "xl/worksheets/sheet1.xml" {
					content = append(content, []byte(suffix)...)
				}
				destination, err := writer.Create(part.Name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := destination.Write(content); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			filename := memberFileDestination(t)
			if err := os.WriteFile(filename, output.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			service := &memberFileServiceFixture{}
			files := NativeMemberFiles{Service: service, dialogs: &memberDialogFixture{paths: []string{filename}}}
			result, selected, err := files.Import(context.Background())
			if !selected || err != memberxlsx.ErrWorkbook || result != (domain.MemberImportResult{}) || service.writes != 0 || service.reads != 0 {
				t.Fatal("valid early rows in an invalid document reached the resource service", selected, err)
			}
		})
	}
}

func (d *memberDialogFixture) Open(options mygo.OpenDialogOptions) ([]string, error) {
	d.open++
	if len(options.Filters) != 1 || !reflect.DeepEqual(options.Filters[0].Extensions, []string{"xlsx"}) {
		return nil, errors.New("wrong import filter")
	}
	if d.returned != nil {
		d.returned()
	}
	return d.paths, d.err
}

func (d *memberDialogFixture) Save(options mygo.SaveDialogOptions) (string, error) {
	d.save++
	if options.DefaultPath != "人员信息.xlsx" || len(options.Filters) != 1 || !reflect.DeepEqual(options.Filters[0].Extensions, []string{"xlsx"}) {
		return "", errors.New("wrong export options")
	}
	if d.returned != nil {
		d.returned()
	}
	return d.filename, d.err
}

type memberFileServiceFixture struct {
	values   []domain.MeetingMember
	imported []domain.MeetingMember
	result   domain.MemberImportResult
	err      error
	read     func()
	reads    int
	writes   int
}

func (s *memberFileServiceFixture) Members(context.Context) ([]domain.MeetingMember, error) {
	s.reads++
	if s.read != nil {
		s.read()
	}
	return s.values, s.err
}

func (s *memberFileServiceFixture) ImportMembers(_ context.Context, values []domain.MeetingMember) (domain.MemberImportResult, error) {
	s.writes++
	s.imported = values
	return s.result, s.err
}

func memberFileDestination(t *testing.T) string {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(directory, "members.xlsx")
}

func TestNativeMemberFilesCancelAndDialogErrorsNeverReachResources(t *testing.T) {
	for _, operation := range []string{"import", "export"} {
		for _, outcome := range []string{"cancel-before", "cancel-after", "dismiss", "dialog-error"} {
			t.Run(operation+"/"+outcome, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				filename := memberFileDestination(t)
				dialog := &memberDialogFixture{filename: filename, paths: []string{filename}}
				var want error
				switch outcome {
				case "cancel-before":
					cancel()
					want = context.Canceled
				case "cancel-after":
					dialog.returned = cancel
					want = context.Canceled
				case "dismiss":
					dialog.filename, dialog.paths = "", nil
				case "dialog-error":
					dialog.err = errors.New("synthetic native dialog detail")
					want = memberxlsx.ErrFile
				}
				service := &memberFileServiceFixture{}
				files := NativeMemberFiles{Service: service, dialogs: dialog}
				var selected bool
				var err error
				if operation == "import" {
					_, selected, err = files.Import(ctx)
				} else {
					selected, err = files.Export(ctx)
				}
				if selected || !errors.Is(err, want) || service.reads != 0 || service.writes != 0 {
					t.Fatal("cancelled or failed dialog reached resources or exposed its native error", selected, err)
				}
				wantDialogs := 1
				if outcome == "cancel-before" {
					wantDialogs = 0
				}
				if dialog.open+dialog.save != wantDialogs {
					t.Fatal("unexpected dialog count")
				}
				if _, err := os.Stat(filename); !os.IsNotExist(err) {
					t.Fatal("cancelled or failed dialog created an artifact")
				}
			})
		}
	}
}

func TestNativeMemberImportReadsSelectedWorkbookAndRejectsInvalidSelections(t *testing.T) {
	filename := memberFileDestination(t)
	values := []domain.MeetingMember{{Name: "合成人员", Department: "合成部门", SortOrder: 9223372036854775807, IsRecorder: true}}
	if err := memberxlsx.SaveFile(context.Background(), filename, values); err != nil {
		t.Fatal(err)
	}
	service := &memberFileServiceFixture{result: domain.MemberImportResult{Created: 1}}
	dialog := &memberDialogFixture{paths: []string{filename}}
	files := NativeMemberFiles{Service: service, dialogs: dialog}
	result, selected, err := files.Import(context.Background())
	if err != nil || !selected || result != service.result || !reflect.DeepEqual(service.imported, values) || service.writes != 1 {
		t.Fatal("selected workbook did not reach the resource transaction intact", err)
	}
	dialog.paths = []string{filename, filename}
	if _, selected, err := files.Import(context.Background()); selected || err != memberxlsx.ErrFile || service.writes != 1 {
		t.Fatal("multi-selection bypassed the single-file boundary")
	}
	dialog.paths = []string{filename}
	if err := os.WriteFile(filename, []byte("invalid workbook"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, selected, err := files.Import(context.Background()); !selected || err != memberxlsx.ErrWorkbook || service.writes != 1 {
		t.Fatal("invalid workbook reached the import transaction")
	}
}

func TestNativeMemberExportReadsBeforePublishingAndHonorsCancellation(t *testing.T) {
	for _, outcome := range []string{"success", "read-error", "cancel-during-read", "invalid-workbook"} {
		t.Run(outcome, func(t *testing.T) {
			filename := memberFileDestination(t)
			if err := os.WriteFile(filename, []byte("preserved artifact"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			service := &memberFileServiceFixture{values: []domain.MeetingMember{{Name: "合成人员", Department: "A&B", SortOrder: -7, IsRecorder: true}}}
			var want error
			switch outcome {
			case "read-error":
				service.err = domain.ErrResourceInvalid
				want = service.err
			case "cancel-during-read":
				service.read = cancel
				want = context.Canceled
			case "invalid-workbook":
				service.values[0].Name = "\x00"
				want = memberxlsx.ErrWorkbook
			}
			files := NativeMemberFiles{Service: service, dialogs: &memberDialogFixture{filename: filename}}
			selected, err := files.Export(ctx)
			if !errors.Is(err, want) || selected != (outcome != "cancel-during-read") || service.reads != 1 {
				t.Fatal("export did not preserve cancellation or resource failure", selected, err)
			}
			if outcome == "success" {
				got, err := memberxlsx.ReadFile(filename)
				if err != nil || !reflect.DeepEqual(got, service.values) {
					t.Fatal("export did not publish the resource snapshot", err)
				}
			} else {
				got, err := os.ReadFile(filename)
				if err != nil || string(got) != "preserved artifact" {
					t.Fatal("failed export destroyed the previous artifact")
				}
			}
			entries, err := os.ReadDir(filepath.Dir(filename))
			if err != nil || len(entries) != 1 {
				t.Fatal("export left temporary files")
			}
		})
	}
}
