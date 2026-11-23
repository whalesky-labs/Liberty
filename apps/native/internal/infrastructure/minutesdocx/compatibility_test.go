//go:build compatibility

package minutesdocx

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"liberty.local/liberty/internal/domain"
)

func TestWriteRustWordDocumentCompatibility(t *testing.T) {
	dir := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if dir == "" {
		t.Fatal("run pnpm native:compat")
	}
	data, err := os.ReadFile(filepath.Join(dir, "minutes-expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Document domain.MinutesDocument }
	if json.Unmarshal(data, &cases) != nil || len(cases) != 18 {
		t.Fatal("invalid Word fixtures")
	}
	for i, c := range cases {
		data, err := Encode(c.Document)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		got := parts(t, data)["word/document.xml"]
		want, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("minutes-%d.xml", i)))
		if err != nil {
			t.Fatal(err)
		}
		a, b := canonicalXML(t, got), canonicalXML(t, want)
		if !reflect.DeepEqual(a, b) {
			for j := 0; j < min(len(a), len(b)); j++ {
				if a[j] != b[j] {
					t.Fatalf("case %d token %d: native %s / Rust %s", i, j, a[j], b[j])
				}
			}
			t.Fatalf("case %d XML token count differs %d/%d", i, len(a), len(b))
		}
		if i == 0 {
			if os.WriteFile(filepath.Join(dir, "go-minutes.docx"), data, 0600) != nil {
				t.Fatal("Word fixture write")
			}
		}
	}
}

func TestCanonicalXMLPreservesAttributeNamespaceIdentity(t *testing.T) {
	original := canonicalXML(t, []byte(`<root xmlns:w="urn:word" xmlns:r="urn:relationship" w:id="word" r:id="relationship" id="plain" xml:space="preserve"/>`))
	aliases := canonicalXML(t, []byte(`<root xmlns:a="urn:word" xmlns:b="urn:relationship" a:id="word" b:id="relationship" id="plain" xml:space="preserve"/>`))
	if !reflect.DeepEqual(original, aliases) {
		t.Fatal("equivalent namespace URIs with different prefixes must compare equally")
	}
	for _, changed := range []string{
		`<root xmlns:w="urn:different" xmlns:r="urn:relationship" w:id="word" r:id="relationship" id="plain" xml:space="preserve"/>`,
		`<root xmlns:w="urn:word" xmlns:r="urn:relationship" w:id="relationship" r:id="word" id="plain" xml:space="preserve"/>`,
		`<root xmlns:w="urn:word" xmlns:r="urn:relationship" w:id="word" r:id="relationship" id="plain" space="preserve"/>`,
	} {
		if reflect.DeepEqual(original, canonicalXML(t, []byte(changed))) {
			t.Errorf("attribute namespace change was ignored: %s", changed)
		}
	}
}

func TestCanonicalXMLPreservesWordTextWhitespace(t *testing.T) {
	original := canonicalXML(t, []byte(`<w:p xmlns:w="urn:word">
  <w:r><w:t xml:space="preserve"> </w:t><w:t> 混合 &amp; &lt;文本&gt; </w:t></w:r>
</w:p>`))
	equivalent := canonicalXML(t, []byte(`<w:p xmlns:w="urn:word"><w:r><w:t xml:space="preserve">&#32;</w:t><w:t> 混合 &#38; &lt;文本&gt; </w:t></w:r></w:p>`))
	if !reflect.DeepEqual(original, equivalent) {
		t.Fatal("structural indentation and equivalent XML escapes must compare equally")
	}
	missingSpace := canonicalXML(t, []byte(`<w:p xmlns:w="urn:word"><w:r><w:t xml:space="preserve"></w:t><w:t> 混合 &amp; &lt;文本&gt; </w:t></w:r></w:p>`))
	if reflect.DeepEqual(original, missingSpace) {
		t.Fatal("significant whitespace inside Word text was ignored")
	}
}

func canonicalXML(t *testing.T, data []byte) []string {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(data))
	out := []string{}
	var elements []xml.Name
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch v := token.(type) {
		case xml.StartElement:
			elements = append(elements, v.Name)
			attrs := []string{}
			for _, a := range v.Attr {
				if a.Name.Space == "xmlns" || a.Name.Space == "" && a.Name.Local == "xmlns" {
					continue
				}
				attrs = append(attrs, fmt.Sprintf("{%s}%s=%q", a.Name.Space, a.Name.Local, a.Value))
			}
			sort.Strings(attrs)
			out = append(out, "start:"+v.Name.Space+":"+v.Name.Local+" "+strings.Join(attrs, "|"))
		case xml.EndElement:
			out = append(out, "end:"+v.Name.Space+":"+v.Name.Local)
			elements = elements[:len(elements)-1]
		case xml.CharData:
			if strings.TrimSpace(string(v)) != "" || len(elements) > 0 && elements[len(elements)-1].Local == "t" {
				out = append(out, "text:"+string(v))
			}
		}
	}
	return out
}
