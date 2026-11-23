package memberxlsx

import (
	"reflect"
	"strings"
	"testing"
)

const sheetPart = "xl/worksheets/sheet1.xml"
const sharedPart = "xl/sharedStrings.xml"
const relsPart = "xl/_rels/workbook.xml.rels"

func sharedWorkbook(parts map[string][]byte) {
	parts[relsPart] = []byte(strings.Replace(string(parts[relsPart]), "</Relationships>", `<Relationship Id="shared" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings" Target="sharedStrings.xml"/></Relationships>`, 1))
	parts[sharedPart] = []byte(`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><si><t>合成人员</t></si></sst>`)
	parts[sheetPart] = []byte(strings.Replace(string(parts[sheetPart]), `<c r="A2" t="inlineStr"><is><t xml:space="preserve">合成人员</t></is></c>`, `<c r="A2" t="s"><v>0</v></c>`, 1))
}

func TestMemberWorkbookRejectsAmbiguousXMLParts(t *testing.T) {
	for _, sample := range []struct {
		name string
		edit func(map[string][]byte)
	}{
		{"worksheet-extra-root", func(p map[string][]byte) { p[sheetPart] = append(p[sheetPart], []byte(`<worksheet/>`)...) }},
		{"worksheet-leading-text", func(p map[string][]byte) { p[sheetPart] = append([]byte("invalid"), p[sheetPart]...) }},
		{"worksheet-trailing-text", func(p map[string][]byte) { p[sheetPart] = append(p[sheetPart], []byte("invalid")...) }},
		{"worksheet-wrong-root", func(p map[string][]byte) {
			p[sheetPart] = []byte(strings.ReplaceAll(string(p[sheetPart]), "worksheet", "unrelated"))
		}},
		{"worksheet-duplicate-data", func(p map[string][]byte) {
			p[sheetPart] = []byte(strings.Replace(string(p[sheetPart]), "</worksheet>", "<sheetData/></worksheet>", 1))
		}},
		{"shared-strings-extra-root", func(p map[string][]byte) {
			sharedWorkbook(p)
			p[sharedPart] = append(p[sharedPart], []byte(`<sst/>`)...)
		}},
		{"shared-strings-wrong-root", func(p map[string][]byte) {
			sharedWorkbook(p)
			p[sharedPart] = []byte(strings.ReplaceAll(string(p[sharedPart]), "sst", "unrelated"))
		}},
		{"shared-strings-trailing-text", func(p map[string][]byte) {
			sharedWorkbook(p)
			p[sharedPart] = append(p[sharedPart], []byte("invalid")...)
		}},
		{"shared-strings-missing-part", func(p map[string][]byte) {
			sharedWorkbook(p)
			delete(p, sharedPart)
			p[sheetPart] = []byte(strings.Replace(string(p[sheetPart]), `<c r="A2" t="s"><v>0</v></c>`, `<c r="A2" t="inlineStr"><is><t>合成人员</t></is></c>`, 1))
		}},
		{"workbook-wrong-root", func(p map[string][]byte) {
			p["xl/workbook.xml"] = []byte(strings.ReplaceAll(string(p["xl/workbook.xml"]), "workbook", "unrelated"))
		}},
		{"relationships-wrong-root", func(p map[string][]byte) {
			p[relsPart] = []byte(strings.ReplaceAll(string(p[relsPart]), "Relationships", "unrelated"))
		}},
		{"duplicate-relationship-id", func(p map[string][]byte) {
			p[relsPart] = []byte(strings.Replace(string(p[relsPart]), "</Relationships>", `<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`, 1))
		}},
		{"unrelated-relationship-type", func(p map[string][]byte) {
			p[relsPart] = []byte(strings.Replace(string(p[relsPart]), "http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet", "urn:synthetic/worksheet", 1))
		}},
		{"invalid-relationship-mode", func(p map[string][]byte) {
			p[relsPart] = []byte(strings.Replace(string(p[relsPart]), `Target="worksheets/sheet1.xml"`, `TargetMode="Unknown" Target="worksheets/sheet1.xml"`, 1))
		}},
		{"duplicate-shared-string-reference", func(p map[string][]byte) {
			sharedWorkbook(p)
			p[relsPart] = []byte(strings.Replace(string(p[relsPart]), "</Relationships>", `<Relationship Id="shared2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings" Target="sharedStrings.xml"/></Relationships>`, 1))
		}},
		{"worksheet-unrelated-namespace", func(p map[string][]byte) {
			p[sheetPart] = []byte(strings.Replace(string(p[sheetPart]), spreadsheetNamespace, "urn:synthetic", 1))
		}},
		{"worksheet-incomplete-tail", func(p map[string][]byte) {
			p[sheetPart] = append(p[sheetPart], []byte("<broken")...)
		}},
		{"missing-sheet-relationship-id", func(p map[string][]byte) {
			p["xl/workbook.xml"] = []byte(strings.Replace(string(p["xl/workbook.xml"]), `r:id="rId1"`, `r:id=""`, 1))
			p[relsPart] = []byte(strings.Replace(string(p[relsPart]), `Id="rId1"`, `Id=""`, 1))
		}},
		{"foreign-sheet-relationship-id", func(p map[string][]byte) {
			p["xl/workbook.xml"] = []byte(strings.Replace(string(p["xl/workbook.xml"]), `r:id="rId1"`, `xmlns:x="urn:synthetic" x:id="rId1"`, 1))
		}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			got, err := Decode(modified(t, sample.edit))
			if err != ErrWorkbook || got != nil {
				t.Fatalf("ambiguous XML returned importable personnel: rows=%d err=%v", len(got), err)
			}
		})
	}
}

func TestMemberWorkbookIgnoresRowsAndStringsOutsideDataContainers(t *testing.T) {
	for _, sample := range []struct {
		name string
		edit func(map[string][]byte)
	}{
		{"worksheet-extension-row", func(p map[string][]byte) {
			p[sheetPart] = []byte(strings.Replace(string(p[sheetPart]), "</worksheet>", `<extLst><ext uri="urn:synthetic"><row r="4"><c r="A4" t="inlineStr"><is><t>扩展区不是人员</t></is></c></row></ext></extLst></worksheet>`, 1))
		}},
		{"foreign-namespace-row", func(p map[string][]byte) {
			p[sheetPart] = []byte(strings.Replace(string(p[sheetPart]), "</sheetData>", `<x:row xmlns:x="urn:synthetic" r="4"><c r="A4" t="inlineStr"><is><t>扩展区不是人员</t></is></c></x:row></sheetData>`, 1))
		}},
		{"shared-string-extension", func(p map[string][]byte) {
			sharedWorkbook(p)
			p[sharedPart] = []byte(strings.Replace(string(p[sharedPart]), "<si>", `<extLst><ext uri="urn:synthetic"><si><t>扩展区不是姓名</t></si></ext></extLst><si>`, 1))
		}},
		{"shared-string-foreign-namespace", func(p map[string][]byte) {
			sharedWorkbook(p)
			p[sharedPart] = []byte(strings.Replace(string(p[sharedPart]), "<si>", `<x:si xmlns:x="urn:synthetic"><t>扩展区不是姓名</t></x:si><si>`, 1))
		}},
		{"foreign-cell", func(p map[string][]byte) {
			p[sheetPart] = []byte(strings.Replace(string(p[sheetPart]), `<row r="2">`, `<row r="2"><x:c xmlns:x="urn:synthetic" r="Z2" t="inlineStr"><is><t>not a cell</t></is></x:c>`, 1))
		}},
		{"foreign-cell-in-name-column", func(p map[string][]byte) {
			p[sheetPart] = []byte(strings.Replace(string(p[sheetPart]), `<row r="2">`, `<row r="2"><x:c xmlns:x="urn:synthetic" r="A2" t="inlineStr"><is><t>not a name</t></is></x:c>`, 1))
		}},
		{"foreign-inline-container", func(p map[string][]byte) {
			p[sheetPart] = []byte(strings.Replace(string(p[sheetPart]), `>合成人员</t></is>`, `>合成人员</t></is><x:is xmlns:x="urn:synthetic"><t>not a name</t></x:is>`, 1))
		}},
		{"foreign-cell-value", func(p map[string][]byte) {
			p[sheetPart] = []byte(strings.Replace(string(p[sheetPart]), `<v>-7</v>`, `<v>-7</v><x:v xmlns:x="urn:synthetic">123</x:v>`, 1))
		}},
		{"foreign-inline-text", func(p map[string][]byte) {
			p[sheetPart] = []byte(strings.Replace(string(p[sheetPart]), `>合成人员</t>`, `>合成人员</t><x:t xmlns:x="urn:synthetic">not a name</x:t>`, 1))
		}},
		{"foreign-shared-text", func(p map[string][]byte) {
			sharedWorkbook(p)
			p[sharedPart] = []byte(strings.Replace(string(p[sharedPart]), `>合成人员</t>`, `>合成人员</t><x:t xmlns:x="urn:synthetic">not a name</x:t>`, 1))
		}},
		{"foreign-rich-text-run", func(p map[string][]byte) {
			sharedWorkbook(p)
			p[sharedPart] = []byte(strings.Replace(string(p[sharedPart]), `</si>`, `<x:r xmlns:x="urn:synthetic"><t>not a name</t></x:r></si>`, 1))
		}},
		{"foreign-sheets-container", func(p map[string][]byte) {
			p["xl/workbook.xml"] = []byte(strings.Replace(string(p["xl/workbook.xml"]), `<sheets>`, `<x:sheets xmlns:x="urn:synthetic"><sheet r:id="missing"/></x:sheets><sheets>`, 1))
		}},
		{"foreign-sheet", func(p map[string][]byte) {
			p["xl/workbook.xml"] = []byte(strings.Replace(string(p["xl/workbook.xml"]), `<sheets>`, `<sheets><x:sheet xmlns:x="urn:synthetic" r:id="missing"/>`, 1))
		}},
		{"foreign-sheet-id-attribute", func(p map[string][]byte) {
			p["xl/workbook.xml"] = []byte(strings.Replace(string(p["xl/workbook.xml"]), `r:id="rId1"`, `r:id="rId1" xmlns:x="urn:synthetic" x:id="missing"`, 1))
		}},
		{"foreign-relationship", func(p map[string][]byte) {
			p[relsPart] = []byte(strings.Replace(string(p[relsPart]), `</Relationships>`, `<x:Relationship xmlns:x="urn:synthetic" Id="rId1" Type="urn:synthetic" Target="not-a-sheet"/></Relationships>`, 1))
		}},
		{"foreign-relationship-id-attribute", func(p map[string][]byte) {
			p[relsPart] = []byte(strings.Replace(string(p[relsPart]), `Id="rId1"`, `Id="rId1" xmlns:x="urn:synthetic" x:Id="missing"`, 1))
		}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			got, err := Decode(modified(t, sample.edit))
			if err != nil || !reflect.DeepEqual(got, members()) {
				t.Fatal("extension content changed personnel data", err)
			}
		})
	}
}

func TestMemberWorkbookAcceptsCommentsSharedRichTextAndStrictNamespaces(t *testing.T) {
	for _, namespace := range []string{"normal", "strict", "unqualified", "unqualified-id"} {
		t.Run(namespace, func(t *testing.T) {
			data := modified(t, func(p map[string][]byte) {
				sharedWorkbook(p)
				p[sharedPart] = []byte(strings.Replace(string(p[sharedPart]), "<t>合成人员</t>", `<r><t>合成</t></r><r><t>人员</t></r>`, 1))
				for _, part := range []string{"xl/workbook.xml", relsPart, sheetPart, sharedPart} {
					p[part] = append([]byte{0xef, 0xbb, 0xbf}, p[part]...)
					p[part] = append(p[part], []byte("\n<!-- complete part -->\n")...)
					if namespace == "strict" {
						p[part] = []byte(strings.ReplaceAll(string(p[part]), "http://schemas.openxmlformats.org/spreadsheetml/2006/main", "http://purl.oclc.org/ooxml/spreadsheetml/main"))
						p[part] = []byte(strings.ReplaceAll(string(p[part]), "http://schemas.openxmlformats.org/officeDocument/2006/relationships", "http://purl.oclc.org/ooxml/officeDocument/relationships"))
					}
					if strings.HasPrefix(namespace, "unqualified") {
						p[part] = []byte(strings.ReplaceAll(string(p[part]), ` xmlns="`+spreadsheetNamespace+`"`, ""))
						p[part] = []byte(strings.ReplaceAll(string(p[part]), ` xmlns="`+packageRelationshipsNamespace+`"`, ""))
					}
				}
				if namespace == "unqualified-id" {
					p["xl/workbook.xml"] = []byte(strings.ReplaceAll(string(p["xl/workbook.xml"]), "r:id=", "id="))
				}
			})
			got, err := Decode(data)
			if err != nil || !reflect.DeepEqual(got, members()) {
				t.Fatal("valid shared-rich-text workbook rejected", err)
			}
		})
	}
}

func TestMemberWorkbookReadsOnlySelectedFirstSheet(t *testing.T) {
	data := modified(t, func(p map[string][]byte) {
		p["xl/workbook.xml"] = []byte(strings.Replace(string(p["xl/workbook.xml"]), "</sheets>", `<sheet name="ignored" sheetId="2" r:id="rId2"/></sheets>`, 1))
		p[relsPart] = []byte(strings.Replace(string(p[relsPart]), "</Relationships>", `<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet2.xml"/></Relationships>`, 1))
		p["xl/worksheets/sheet2.xml"] = []byte(strings.Replace(string(p[sheetPart]), "合成人员", "第二张表不得导入", 1))
	})
	got, err := Decode(data)
	if err != nil || !reflect.DeepEqual(got, members()) {
		t.Fatal("unselected sheet changed the import", err)
	}
}

func TestMemberWorkbookDecodesSpreadsheetEscapes(t *testing.T) {
	for _, sample := range []struct {
		name, encoded, decoded string
	}{
		{"ascii", "_x0041_", "A"},
		{"literal", "_x005F_x0041_", "_x0041_"},
		{"literal-prefix", "_x005F_x005F_x0041_", "_x005FA"},
		{"adjacent", "_x005F__x0041_", "_A"},
		{"shared-underscore", "_x0041_x0042_", "Ax0042_"},
		{"carriage-return", "before_x000D_after", "before\rafter"},
		{"surrogate-pair", "_xD83D__xDCF7_", "📷"},
		{"lowercase-hex", "_x004a_", "J"},
		{"isolated-high-surrogate", "before_xD83D_after", "before�after"},
		{"isolated-low-surrogate", "before_xDCF7_after", "before�after"},
		{"near-patterns", "_x123_ _xGGGG_ _X0041_ _x+041_", "_x123_ _xGGGG_ _X0041_ _x+041_"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			for _, storage := range []string{"inline", "shared", "rich", "cached-string"} {
				t.Run(storage, func(t *testing.T) {
					data := modified(t, func(parts map[string][]byte) {
						if storage == "shared" || storage == "rich" {
							sharedWorkbook(parts)
							text := `<t>` + sample.encoded + `</t>`
							if storage == "rich" {
								text = `<r>` + text + `</r>`
							}
							parts[sharedPart] = []byte(strings.Replace(string(parts[sharedPart]), `<t>合成人员</t>`, text, 1))
							return
						}
						if storage == "cached-string" {
							parts[sheetPart] = []byte(strings.Replace(string(parts[sheetPart]), `<c r="A2" t="inlineStr"><is><t xml:space="preserve">合成人员</t></is></c>`, `<c r="A2" t="str"><v>`+sample.encoded+`</v></c>`, 1))
							return
						}
						parts[sheetPart] = []byte(strings.Replace(string(parts[sheetPart]), `>合成人员</t>`, `>`+sample.encoded+`</t>`, 1))
					})
					want := members()
					want[0].Name = sample.decoded
					got, err := Decode(data)
					if err != nil || !reflect.DeepEqual(got, want) {
						t.Fatal("spreadsheet escape changed personnel data", err)
					}
				})
			}
		})
	}
}

func TestMemberWorkbookKeepsEscapeBoundariesBetweenRichTextRuns(t *testing.T) {
	data := modified(t, func(parts map[string][]byte) {
		sharedWorkbook(parts)
		parts[sharedPart] = []byte(strings.Replace(string(parts[sharedPart]), `<t>合成人员</t>`, `<r><t>before_x00</t></r><r><t>41_after</t></r>`, 1))
	})
	want := members()
	want[0].Name = "before_x0041_after"
	got, err := Decode(data)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("escape crossed rich-text run boundary", err)
	}
}
