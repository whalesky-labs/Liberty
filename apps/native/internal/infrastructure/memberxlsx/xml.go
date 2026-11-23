package memberxlsx

import (
	"bytes"
	"encoding/xml"
	"io"
	"strconv"
	"strings"
)

const spreadsheetNamespace = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
const strictSpreadsheetNamespace = "http://purl.oclc.org/ooxml/spreadsheetml/main"
const packageRelationshipsNamespace = "http://schemas.openxmlformats.org/package/2006/relationships"
const officeRelationshipsNamespace = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
const strictOfficeRelationshipsNamespace = "http://purl.oclc.org/ooxml/officeDocument/relationships"

// xmlDocument accepts one complete part, including legal surrounding comments
// and processing instructions. A successful payload parse is not sufficient:
// trailing text, another root or a truncated tail must reject the entire import.
// consume must consume the root's matching end element.
func xmlDocument(data []byte, root string, consume func(*xml.Decoder, xml.StartElement) error) error {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	d := xml.NewDecoder(bytes.NewReader(data))
	found := false
	for {
		token, err := d.Token()
		if err == io.EOF {
			if found {
				return nil
			}
			return ErrWorkbook
		}
		if err != nil {
			return ErrWorkbook
		}
		switch value := token.(type) {
		case xml.StartElement:
			if found || value.Name.Local != root || !partNamespace(root, value.Name.Space) {
				return ErrWorkbook
			}
			found = true
			if consume(d, value) != nil {
				return ErrWorkbook
			}
		case xml.CharData:
			if strings.TrimSpace(string(value)) != "" {
				return ErrWorkbook
			}
		case xml.Comment:
		case xml.ProcInst:
			if found && strings.EqualFold(value.Target, "xml") {
				return ErrWorkbook
			}
		default:
			return ErrWorkbook
		}
	}
}

func officeRelationship(value, kind string) bool {
	return value == officeRelationshipsNamespace+"/"+kind ||
		value == strictOfficeRelationshipsNamespace+"/"+kind
}

func partNamespace(root, namespace string) bool {
	// Preserve existing support for unqualified parts as well as both OOXML
	// spreadsheet namespaces; an unrelated namespace is never a spreadsheet.
	if namespace == "" {
		return true
	}
	if root == "Relationships" {
		return namespace == packageRelationshipsNamespace
	}
	return namespace == spreadsheetNamespace || namespace == strictSpreadsheetNamespace
}

// xmlChildren visits immediate children only. visit must consume each child;
// skipping an extension prevents similarly named descendants becoming data.
func xmlChildren(d *xml.Decoder, parent xml.StartElement, visit func(xml.StartElement) error) error {
	for {
		token, err := d.Token()
		if err != nil {
			return ErrWorkbook
		}
		switch value := token.(type) {
		case xml.StartElement:
			if err := visit(value); err != nil {
				return err
			}
		case xml.EndElement:
			if value.Name != parent.Name {
				return ErrWorkbook
			}
			return nil
		case xml.CharData:
			if strings.TrimSpace(string(value)) != "" {
				return ErrWorkbook
			}
		case xml.Comment:
		case xml.ProcInst:
			if strings.EqualFold(value.Target, "xml") {
				return ErrWorkbook
			}
		default:
			return ErrWorkbook
		}
	}
}

func xmlDecode(data []byte, root string, value any) error {
	return xmlDocument(data, root, func(d *xml.Decoder, start xml.StartElement) error {
		return d.DecodeElement(value, &start)
	})
}

func xmlName(start, parent xml.StartElement, local string) bool {
	return start.Name == (xml.Name{Space: parent.Name.Space, Local: local})
}

func xmlAttribute(start xml.StartElement, local string) string {
	for _, attribute := range start.Attr {
		if attribute.Name == (xml.Name{Local: local}) {
			return attribute.Value
		}
	}
	return ""
}

func (value *workbook) UnmarshalXML(decoder *xml.Decoder, root xml.StartElement) error {
	return xmlChildren(decoder, root, func(start xml.StartElement) error {
		if !xmlName(start, root, "sheets") {
			return decoder.Skip()
		}
		return xmlChildren(decoder, start, func(start xml.StartElement) error {
			if !xmlName(start, root, "sheet") {
				return decoder.Skip()
			}
			var identifier string
			found := false
			for _, attribute := range start.Attr {
				if attribute.Name.Local != "id" || (attribute.Name.Space != officeRelationshipsNamespace && attribute.Name.Space != strictOfficeRelationshipsNamespace && !(root.Name.Space == "" && attribute.Name.Space == "")) {
					continue
				}
				if found {
					return ErrWorkbook
				}
				found = true
				identifier = attribute.Value
			}
			value.Sheets = append(value.Sheets, sheet{ID: identifier})
			return decoder.Skip()
		})
	})
}

func (value *relationships) UnmarshalXML(decoder *xml.Decoder, root xml.StartElement) error {
	return xmlChildren(decoder, root, func(start xml.StartElement) error {
		if !xmlName(start, root, "Relationship") {
			return decoder.Skip()
		}
		value.Items = append(value.Items, relationship{
			ID: xmlAttribute(start, "Id"), Target: xmlAttribute(start, "Target"),
			Mode: xmlAttribute(start, "TargetMode"), Type: xmlAttribute(start, "Type"),
		})
		return decoder.Skip()
	})
}

func (value *richText) UnmarshalXML(decoder *xml.Decoder, root xml.StartElement) error {
	var content strings.Builder
	readText := func(start xml.StartElement) error {
		if !xmlName(start, root, "t") {
			return decoder.Skip()
		}
		var text string
		if err := decoder.DecodeElement(&text, &start); err != nil {
			return ErrWorkbook
		}
		content.WriteString(unescapeSpreadsheet(text))
		return nil
	}
	err := xmlChildren(decoder, root, func(start xml.StartElement) error {
		if xmlName(start, root, "r") {
			return xmlChildren(decoder, start, readText)
		}
		return readText(start)
	})
	value.Text = content.String()
	return err
}

func (value *cell) UnmarshalXML(decoder *xml.Decoder, root xml.StartElement) error {
	value.Reference = xmlAttribute(root, "r")
	value.Type = xmlAttribute(root, "t")
	return xmlChildren(decoder, root, func(start xml.StartElement) error {
		switch {
		case xmlName(start, root, "v"):
			return decoder.DecodeElement(&value.Value, &start)
		case xmlName(start, root, "is"):
			return decoder.DecodeElement(&value.Inline, &start)
		default:
			return decoder.Skip()
		}
	})
}

func (value *row) UnmarshalXML(decoder *xml.Decoder, root xml.StartElement) error {
	if index := xmlAttribute(root, "r"); index != "" {
		var err error
		value.Index, err = strconv.Atoi(index)
		if err != nil {
			return ErrWorkbook
		}
	}
	return xmlChildren(decoder, root, func(start xml.StartElement) error {
		if !xmlName(start, root, "c") {
			return decoder.Skip()
		}
		var item cell
		if err := decoder.DecodeElement(&item, &start); err != nil {
			return ErrWorkbook
		}
		value.Cells = append(value.Cells, item)
		return nil
	})
}

func sharedStrings(data []byte) ([]string, error) {
	values := []string{}
	err := xmlDocument(data, "sst", func(d *xml.Decoder, root xml.StartElement) error {
		return xmlChildren(d, root, func(start xml.StartElement) error {
			if start.Name != (xml.Name{Space: root.Name.Space, Local: "si"}) {
				return d.Skip()
			}
			if len(values) >= maxCells {
				return ErrWorkbook
			}
			var text richText
			if err := d.DecodeElement(&text, &start); err != nil {
				return ErrWorkbook
			}
			values = append(values, text.value())
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return values, nil
}

func worksheetRows(data []byte, visit func(row) error) error {
	found := false
	err := xmlDocument(data, "worksheet", func(d *xml.Decoder, root xml.StartElement) error {
		return xmlChildren(d, root, func(start xml.StartElement) error {
			if start.Name != (xml.Name{Space: root.Name.Space, Local: "sheetData"}) {
				return d.Skip()
			}
			if found {
				return ErrWorkbook
			}
			found = true
			return xmlChildren(d, start, func(start xml.StartElement) error {
				if start.Name != (xml.Name{Space: root.Name.Space, Local: "row"}) {
					return d.Skip()
				}
				var value row
				if err := d.DecodeElement(&value, &start); err != nil {
					return ErrWorkbook
				}
				return visit(value)
			})
		})
	})
	if err != nil || !found {
		return ErrWorkbook
	}
	return nil
}
