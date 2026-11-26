package memberxlsx

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"liberty.local/liberty/internal/domain"
)

func members() []domain.MeetingMember {
	return []domain.MeetingMember{{Name: "合成人员", Department: "A&B <研发>", SortOrder: -7, IsRecorder: true}, {Name: "=SUM(1,2)", SortOrder: 9223372036854775807}}
}

func escapedWorkbookMembers() []domain.MeetingMember {
	values := []domain.MeetingMember{
		{Name: "_x0041_", Department: "literal _x000D_ / _x005F_"},
		{Name: "_x005F_x0041_", Department: "before\rafter / before\nafter / before\tafter"},
		{Name: "转义 📷", Department: "_xD83D__xDCF7_ / _x004a_"},
		{Name: "overlap _x0041_x0042_", Department: "prefix _x005F_x000D_ tail"},
		{Name: "near _x123_ _xGGGG_ _X0041_", Department: "A&B <研发>"},
	}
	for index := range values {
		values[index].SortOrder = int64(index)
		values[index].IsRecorder = index == 0
	}
	return values
}

func TestMemberWorkbookPreservesLiteralSpreadsheetEscapes(t *testing.T) {
	values := escapedWorkbookMembers()
	data, err := Encode(values)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(data)
	if err != nil || !reflect.DeepEqual(got, values) {
		t.Fatal("literal spreadsheet escapes changed", err)
	}
}
func TestMemberWorkbookPreservesUnicodeFlagsAndInt64(t *testing.T) {
	data, err := Encode(members())
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(data)
	if err != nil || !reflect.DeepEqual(got, members()) {
		t.Fatal("roundtrip differs", err)
	}
	parts, err := archive(data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(parts["xl/worksheets/sheet1.xml"]), "<f>") {
		t.Fatal("formula text executed")
	}
}
func modified(t *testing.T, edit func(map[string][]byte)) []byte {
	t.Helper()
	data, err := Encode(members())
	if err != nil {
		t.Fatal(err)
	}
	parts, err := archive(data)
	if err != nil {
		t.Fatal(err)
	}
	edit(parts)
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for name, value := range parts {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(value); err != nil {
			t.Fatal(err)
		}
	}
	if z.Close() != nil {
		t.Fatal("zip")
	}
	return out.Bytes()
}
func TestMemberWorkbookRejectsInvalidRowsAndExternalSheets(t *testing.T) {
	for _, item := range []struct{ name, from, to string }{
		{"decimal sort", ">-7<", ">1.2<"}, {"missing header", ">姓名<", ">Unknown<"}, {"invalid recorder", ">否<", ">maybe<"},
	} {
		t.Run(item.name, func(t *testing.T) {
			data := modified(t, func(parts map[string][]byte) {
				parts["xl/worksheets/sheet1.xml"] = []byte(strings.ReplaceAll(string(parts["xl/worksheets/sheet1.xml"]), item.from, item.to))
			})
			if _, err := Decode(data); err == nil {
				t.Fatal("invalid sheet accepted")
			}
		})
	}
	data := modified(t, func(parts map[string][]byte) {
		parts["xl/_rels/workbook.xml.rels"] = []byte(strings.ReplaceAll(string(parts["xl/_rels/workbook.xml.rels"]), `Target="worksheets/sheet1.xml"`, `TargetMode="External" Target="https://example.test/sheet.xml"`))
	})
	if _, err := Decode(data); err == nil {
		t.Fatal("external sheet accepted")
	}
	data = modified(t, func(parts map[string][]byte) { parts["../escape"] = []byte("no") })
	if _, err := Decode(data); err == nil {
		t.Fatal("traversal accepted")
	}
}
func TestMemberWorkbookAcceptsHeaderAliasesAndBooleans(t *testing.T) {
	data := modified(t, func(parts map[string][]byte) {
		text := string(parts["xl/worksheets/sheet1.xml"])
		for from, to := range map[string]string{"姓名": "Name", "部门": "Department", "排序": "Sort Order", "是否设置会议记录人": "Recorder", ">是<": ">YES<", ">否<": ">false<"} {
			text = strings.ReplaceAll(text, from, to)
		}
		parts["xl/worksheets/sheet1.xml"] = []byte(text)
	})
	got, err := Decode(data)
	if err != nil || !reflect.DeepEqual(got, members()) {
		t.Fatal("aliases differ", err)
	}
}
func TestAtomicMemberExportDoesNotReplaceSymlink(t *testing.T) {
	directory := memberExportDirectory(t)
	filename := filepath.Join(directory, "members.xlsx")
	if err := SaveFile(context.Background(), filename, members()); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(filename)
	if err != nil || !reflect.DeepEqual(got, members()) {
		t.Fatal("saved workbook differs")
	}
	target := filepath.Join(directory, "kept.xlsx")
	if os.WriteFile(target, []byte("kept"), 0600) != nil {
		t.Fatal("write")
	}
	link := filepath.Join(directory, "link.xlsx")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlink unavailable")
	}
	if err := SaveFile(context.Background(), link, members()); err == nil {
		t.Fatal("symlink overwritten")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "kept" {
		t.Fatal("target changed")
	}
	if err := SaveFile(context.Background(), filename, []domain.MeetingMember{{Name: string([]byte{0})}}); err == nil {
		t.Fatal("invalid XML published")
	}
	got, err = ReadFile(filename)
	if err != nil || !reflect.DeepEqual(got, members()) {
		t.Fatal("failed export destroyed previous workbook")
	}
}
