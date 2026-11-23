package desktop

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/memberxlsx"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
)

// These tests use only a new temporary profile and its real repositories.
// Dialog selection is controlled; no system dialogs or user profiles are used.
type persistedMemberFiles struct {
	database string
	workbook string
	store    *jobdb.Store
	service  *application.Resources
}

func newPersistedMemberFiles(t *testing.T) *persistedMemberFiles {
	t.Helper()
	workbook := memberFileDestination(t)
	f := &persistedMemberFiles{database: filepath.Join(filepath.Dir(workbook), "liberty.sqlite3"), workbook: workbook}
	var err error
	f.store, _, err = jobdb.OpenPrepared(context.Background(), f.database, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	f.service = application.NewResources(f.store, nil)
	t.Cleanup(func() { f.service.Close(); _ = f.store.Close() })
	for _, member := range []domain.MeetingMember{
		{Name: "合成甲", Department: "原部门甲", SortOrder: -3, IsRecorder: true},
		{Name: "合成乙", Department: "原部门乙", SortOrder: 7},
		{Name: "未参与合并", Department: "原部门丙", SortOrder: 42},
	} {
		if _, err := f.service.SaveMember(context.Background(), member); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *persistedMemberFiles) snapshot(t *testing.T) []domain.MeetingMember {
	t.Helper()
	values, err := f.service.Members(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return values
}

func (f *persistedMemberFiles) reopen(t *testing.T, statements ...string) {
	t.Helper()
	f.service.Close()
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	if len(statements) != 0 {
		path := filepath.ToSlash(f.database)
		if path[0] != '/' {
			path = "/" + path
		}
		uri := url.URL{Scheme: "file", Path: path, RawQuery: "mode=rw"}
		db, err := sql.Open("sqlite", uri.String())
		if err != nil {
			t.Fatal(err)
		}
		for _, statement := range statements {
			if _, err := db.Exec(statement); err != nil {
				_ = db.Close()
				t.Fatal(err)
			}
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	f.store, err = jobdb.Open(context.Background(), f.database)
	if err != nil {
		t.Fatal(err)
	}
	f.service = application.NewResources(f.store, nil)
}

func (f *persistedMemberFiles) write(t *testing.T, values []domain.MeetingMember) {
	t.Helper()
	if err := memberxlsx.SaveFile(context.Background(), f.workbook, values); err != nil {
		t.Fatal(err)
	}
}

func (f *persistedMemberFiles) importFile(ctx context.Context, returned func()) (domain.MemberImportResult, bool, error) {
	adapter := NativeMemberFiles{Service: f.service, dialogs: &memberDialogFixture{paths: []string{f.workbook}, returned: returned}}
	return adapter.Import(ctx)
}

func TestMemberFileTransactionRollbackRetryAndReopen(t *testing.T) {
	for _, failure := range []struct{ name, statement string }{
		{"late-insert", `CREATE TRIGGER member_import_failure BEFORE INSERT ON meeting_members WHEN NEW.name='合成拒绝' BEGIN SELECT RAISE(ABORT,'synthetic member insert failure'); END`},
		{"recorder-transfer", `CREATE TRIGGER member_import_failure BEFORE UPDATE OF is_recorder ON meeting_members WHEN OLD.is_recorder=1 AND NEW.is_recorder=0 BEGIN SELECT RAISE(ABORT,'synthetic recorder transfer failure'); END`},
	} {
		t.Run(failure.name, func(t *testing.T) {
			f := newPersistedMemberFiles(t)
			before := f.snapshot(t)
			f.write(t, []domain.MeetingMember{
				{Name: " 合成乙 ", Department: " 更新部门 ", SortOrder: -9223372036854775808},
				{Name: "合成新记录人", Department: "A&B <合成>", SortOrder: 9223372036854775807, IsRecorder: true},
				{Name: "合成拒绝", Department: "末行", SortOrder: 10},
			})
			f.reopen(t, failure.statement)
			result, selected, err := f.importFile(context.Background(), nil)
			if !selected || err != jobdb.ErrDatabase || result != (domain.MemberImportResult{}) {
				t.Fatal("failed transaction reported successful import counts", result, selected, err)
			}
			for restart := range 3 {
				f.reopen(t)
				if !reflect.DeepEqual(f.snapshot(t), before) {
					t.Fatalf("failed import changed member rows or recorder state after restart %d", restart)
				}
			}
			f.reopen(t, "DROP TRIGGER member_import_failure")
			result, selected, err = f.importFile(context.Background(), nil)
			if err != nil || !selected || result != (domain.MemberImportResult{Created: 2, Updated: 1}) {
				t.Fatal("retry did not commit the complete workbook", result, err)
			}
			f.reopen(t)
			committed := f.snapshot(t)
			if len(committed) != 5 {
				t.Fatal("retry lost or duplicated members")
			}
			original := make(map[string]domain.MeetingMember)
			for _, member := range before {
				original[member.Name] = member
			}
			recorders := 0
			for _, member := range committed {
				if old, ok := original[member.Name]; ok && (member.ID != old.ID || member.CreatedAt != old.CreatedAt) {
					t.Fatal("import replaced an existing identity or creation timestamp")
				}
				if member.IsRecorder {
					recorders++
					if member.Name != "合成新记录人" {
						t.Fatal("recorder transfer retained a different recorder")
					}
				}
				switch member.Name {
				case "合成乙":
					if member.Department != "更新部门" || member.SortOrder != -9223372036854775808 {
						t.Fatal("merge lost trimming or minimum int64")
					}
				case "合成新记录人":
					if member.SortOrder != 9223372036854775807 || member.Department != "A&B <合成>" {
						t.Fatal("new record lost text or maximum int64")
					}
				case "未参与合并":
					if member != original[member.Name] {
						t.Fatal("import modified an unrelated member")
					}
				}
			}
			if recorders != 1 {
				t.Fatal("committed import violated the single-recorder rule")
			}
			result, selected, err = f.importFile(context.Background(), nil)
			if err != nil || !selected || result != (domain.MemberImportResult{Updated: 3}) {
				t.Fatal("repeated import duplicated rows", result, err)
			}
			f.reopen(t)
			replayed := f.snapshot(t)
			if len(replayed) != len(committed) {
				t.Fatal("repeated import changed member count")
			}
			for i := range replayed {
				replayed[i].UpdatedAt = committed[i].UpdatedAt
			}
			if !reflect.DeepEqual(replayed, committed) {
				t.Fatal("repeated import changed identities or data")
			}
			exported := filepath.Join(filepath.Dir(f.workbook), "reopened-export.xlsx")
			adapter := NativeMemberFiles{Service: f.service, dialogs: &memberDialogFixture{filename: exported}}
			if selected, err := adapter.Export(context.Background()); err != nil || !selected {
				t.Fatal("reopened persisted members could not be exported", err)
			}
			decoded, err := memberxlsx.ReadFile(exported)
			for i := range committed {
				committed[i].ID, committed[i].CreatedAt, committed[i].UpdatedAt = "", "", ""
			}
			if err != nil || !reflect.DeepEqual(decoded, committed) {
				t.Fatal("exported workbook does not match committed personnel", err)
			}
		})
	}
}

func corruptMemberWorkbookTail(t *testing.T, filename string) {
	t.Helper()
	data, err := os.ReadFile(filename)
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
			t.Fatal("cannot read synthetic workbook")
		}
		if part.Name == "xl/worksheets/sheet1.xml" {
			content = append(content, []byte("<worksheet/>")...)
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
	if err := os.WriteFile(filename, output.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestMemberFileRejectedOrCancelledImportPreservesDatabaseAfterReopen(t *testing.T) {
	for _, sample := range []string{"duplicate-trimmed-name", "multiple-recorders", "empty-name", "malformed-tail", "cancelled-selection", "closed-service"} {
		t.Run(sample, func(t *testing.T) {
			f := newPersistedMemberFiles(t)
			before := f.snapshot(t)
			values := []domain.MeetingMember{{Name: "合成乙", Department: "不得保存", SortOrder: 12}, {Name: "合成新成员", IsRecorder: true}}
			want := memberxlsx.ErrWorkbook
			switch sample {
			case "duplicate-trimmed-name":
				values[1].Name = " 合成乙 "
			case "multiple-recorders":
				values[0].IsRecorder = true
			case "empty-name":
				values[1].Name = "  "
			case "cancelled-selection":
				want = context.Canceled
			case "closed-service":
				want = application.ErrWindowScope
			}
			f.write(t, values)
			if sample == "malformed-tail" {
				corruptMemberWorkbookTail(t, f.workbook)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var returned func()
			if sample == "cancelled-selection" {
				returned = cancel
			} else if sample == "closed-service" {
				returned = f.service.Close
			}
			result, selected, err := f.importFile(ctx, returned)
			if !errors.Is(err, want) || result != (domain.MemberImportResult{}) || selected != (sample != "cancelled-selection") {
				t.Error("rejected import returned a successful result", result, selected, err)
			}
			for restart := range 2 {
				f.reopen(t)
				if !reflect.DeepEqual(f.snapshot(t), before) {
					t.Errorf("rejected import changed persistent members after reopen %d", restart)
				}
			}
		})
	}
}
