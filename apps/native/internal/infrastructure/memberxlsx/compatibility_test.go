//go:build compatibility

package memberxlsx

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRustEscapedWorkbookInteroperability(t *testing.T) {
	directory := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if directory == "" {
		t.Fatal("explicit Rust escaped workbook fixture required")
	}
	directory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	values := escapedWorkbookMembers()
	t.Run("go-export", func(t *testing.T) {
		if err := SaveFile(context.Background(), filepath.Join(directory, "go-escaped-members.xlsx"), values); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("rust-export", func(t *testing.T) {
		got, err := ReadFile(filepath.Join(directory, "rust-escaped-members.xlsx"))
		if err != nil || !reflect.DeepEqual(got, values) {
			t.Fatal("Rust escaped workbook differs", err)
		}
	})
	t.Run("encoded-xml", func(t *testing.T) {
		data := modified(t, func(parts map[string][]byte) {
			text := string(parts[sheetPart])
			text = strings.Replace(text, `>合成人员</t>`, `>_x005F_x005F_x0041_</t>`, 1)
			text = strings.Replace(text, `>A&amp;B &lt;研发&gt;</t>`, `>_xD83D__xDCF7_</t>`, 1)
			text = strings.Replace(text, `>=SUM(1,2)</t>`, `>before_xD83D_after</t>`, 1)
			text = strings.Replace(text, `<c r="B3" t="inlineStr"><is><t xml:space="preserve"></t></is></c>`, `<c r="B3" t="str"><v>_x0041_</v></c>`, 1)
			parts[sheetPart] = []byte(text)
		})
		if err := os.WriteFile(filepath.Join(directory, "go-encoded-members.xlsx"), data, 0600); err != nil {
			t.Fatal(err)
		}
		want := members()
		want[0].Name, want[0].Department = "_x005FA", "📷"
		want[1].Name, want[1].Department = "before�after", "A"
		got, err := Decode(data)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("encoded XML workbook differs", err)
		}
	})
}

func TestWriteRustWorkbookInteroperability(t *testing.T) {
	directory := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if directory == "" {
		t.Fatal("run pnpm native:compat")
	}
	got, err := ReadFile(filepath.Join(directory, "rust-members.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "合成人员" || got[0].Department != "A&B <研发>" || got[0].SortOrder != -7 || !got[0].IsRecorder || got[1].SortOrder != 9 || got[1].IsRecorder {
		t.Fatal("Rust workbook differs")
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveFile(context.Background(), filepath.Join(directory, "go-members.xlsx"), members()); err != nil {
		t.Fatal(err)
	}
}
