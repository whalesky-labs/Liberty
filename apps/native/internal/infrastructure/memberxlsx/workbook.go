// Package memberxlsx reads and writes the original four-column member workbook.
// ZIP/XML input is bounded, formulas are never evaluated, and no external links
// or relationships are fetched. File paths stay in the native dialog adapter.
package memberxlsx

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/fileout"
)

var ErrWorkbook = errors.New("member_workbook_invalid")
var ErrFile = errors.New("member_workbook_file_failed")

const maxArchive = 32 << 20
const maxExpanded = 128 << 20
const maxRows = 100000
const maxCells = 1000000

func ReadFile(filename string) ([]domain.MeetingMember, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, ErrFile
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxArchive {
		return nil, ErrFile
	}
	data, err := io.ReadAll(io.LimitReader(f, maxArchive+1))
	if err != nil || len(data) > maxArchive {
		return nil, ErrFile
	}
	return Decode(data)
}
func archive(data []byte) (map[string][]byte, error) {
	if len(data) > maxArchive {
		return nil, ErrWorkbook
	}
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(r.File) > 2048 {
		return nil, ErrWorkbook
	}
	result := make(map[string][]byte)
	var total uint64
	for _, f := range r.File {
		name := f.Name
		if f.FileInfo().IsDir() {
			name = strings.TrimSuffix(name, "/")
		}
		if !filepath.IsLocal(name) || strings.Contains(name, "\\") || path.Clean(name) != name || f.Mode()&os.ModeSymlink != 0 {
			return nil, ErrWorkbook
		}
		if _, ok := result[name]; ok {
			return nil, ErrWorkbook
		}
		if f.UncompressedSize64 > maxExpanded {
			return nil, ErrWorkbook
		}
		total += f.UncompressedSize64
		if total > maxExpanded {
			return nil, ErrWorkbook
		}
		// Still validate every member's checksum/size without extracting it to disk.
		reader, err := f.Open()
		if err != nil {
			return nil, ErrWorkbook
		}
		value, readErr := io.ReadAll(io.LimitReader(reader, int64(f.UncompressedSize64)+1))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || uint64(len(value)) != f.UncompressedSize64 {
			return nil, ErrWorkbook
		}
		result[name] = value
	}
	return result, nil
}

type relationship struct {
	ID     string
	Target string
	Mode   string
	Type   string
}
type relationships struct {
	Items []relationship
}
type sheet struct {
	ID string
}
type workbook struct {
	Sheets []sheet
}
type richText struct {
	Text string
}

func (r richText) value() string {
	return r.Text
}

func spreadsheetEscapeUnit(value string, index int) (uint16, bool) {
	if len(value)-index < 7 || value[index:index+2] != "_x" || value[index+6] != '_' {
		return 0, false
	}
	for _, digit := range value[index+2 : index+6] {
		if !(digit >= '0' && digit <= '9' || digit >= 'a' && digit <= 'f' || digit >= 'A' && digit <= 'F') {
			return 0, false
		}
	}
	unit, err := strconv.ParseUint(value[index+2:index+6], 16, 16)
	return uint16(unit), err == nil
}

func unescapeSpreadsheet(value string) string {
	if !strings.Contains(value, "_x") {
		return value
	}
	var output strings.Builder
	for index := 0; index < len(value); {
		unit, ok := spreadsheetEscapeUnit(value, index)
		if !ok {
			output.WriteByte(value[index])
			index++
			continue
		}
		index += 7
		decoded := rune(unit)
		if unit >= 0xd800 && unit <= 0xdbff {
			low, ok := spreadsheetEscapeUnit(value, index)
			if ok && low >= 0xdc00 && low <= 0xdfff {
				decoded = utf16.DecodeRune(decoded, rune(low))
				index += 7
			} else {
				decoded = utf8.RuneError
			}
		} else if unit >= 0xdc00 && unit <= 0xdfff {
			decoded = utf8.RuneError
		}
		output.WriteRune(decoded)
	}
	return output.String()
}

type cell struct {
	Reference string
	Type      string
	Value     string
	Inline    richText
}
type row struct {
	Index int
	Cells []cell
}

func targetPart(base, target string) (string, error) {
	if strings.ContainsAny(target, "\\:\x00") || strings.Contains(target, "?") || strings.Contains(target, "#") {
		return "", ErrWorkbook
	}
	var name string
	if strings.HasPrefix(target, "/") {
		name = strings.TrimPrefix(target, "/")
	} else {
		name = path.Join(path.Dir(base), target)
	}
	if !filepath.IsLocal(name) || path.Clean(name) != name {
		return "", ErrWorkbook
	}
	return name, nil
}
func Decode(data []byte) ([]domain.MeetingMember, error) {
	parts, err := archive(data)
	if err != nil {
		return nil, err
	}
	var book workbook
	var rels relationships
	if xmlDecode(parts["xl/workbook.xml"], "workbook", &book) != nil || len(book.Sheets) == 0 || strings.TrimSpace(book.Sheets[0].ID) == "" || xmlDecode(parts["xl/_rels/workbook.xml.rels"], "Relationships", &rels) != nil {
		return nil, ErrWorkbook
	}
	sheetPath, sharedPath := "", ""
	seenIDs := make(map[string]bool)
	for _, r := range rels.Items {
		if strings.TrimSpace(r.ID) == "" || seenIDs[r.ID] {
			return nil, ErrWorkbook
		}
		seenIDs[r.ID] = true
		if r.Mode == "External" {
			if r.ID == book.Sheets[0].ID {
				return nil, ErrWorkbook
			}
			continue
		}
		if r.Mode != "" && r.Mode != "Internal" {
			return nil, ErrWorkbook
		}
		if r.ID == book.Sheets[0].ID {
			if !officeRelationship(r.Type, "worksheet") {
				return nil, ErrWorkbook
			}
			sheetPath, err = targetPart("xl/workbook.xml", r.Target)
		}
		if officeRelationship(r.Type, "sharedStrings") {
			if sharedPath != "" {
				return nil, ErrWorkbook
			}
			sharedPath, err = targetPart("xl/workbook.xml", r.Target)
		}
		if err != nil {
			return nil, err
		}
	}
	shared := []string{}
	if sharedPath != "" {
		shared, err = sharedStrings(parts[sharedPath])
		if err != nil {
			return nil, err
		}
	}
	sheetData, ok := parts[sheetPath]
	if !ok {
		return nil, ErrWorkbook
	}
	headers := map[string]int{}
	headerFound := false
	values := []domain.MeetingMember{}
	rowCount, cells := 0, 0
	err = worksheetRows(sheetData, func(r row) error {
		rowCount++
		cells += len(r.Cells)
		if rowCount > maxRows || cells > maxCells {
			return ErrWorkbook
		}
		fields := map[int]string{}
		nextColumn := 0
		nonempty := false
		for _, c := range r.Cells {
			column := nextColumn
			if c.Reference != "" {
				column, err = cellColumn(c.Reference)
				if err != nil {
					return err
				}
			}
			if _, exists := fields[column]; exists {
				return ErrWorkbook
			}
			value, err := cellValue(c, shared)
			if err != nil {
				return err
			}
			fields[column] = strings.TrimSpace(value)
			nonempty = nonempty || fields[column] != ""
			nextColumn = column + 1
		}
		if !headerFound {
			if !nonempty {
				return nil
			}
			for col := 0; col < nextColumn; col++ {
				value := fields[col]
				switch value {
				case "姓名", "name", "Name":
					headers["name"] = col
				case "部门", "department", "Department":
					headers["department"] = col
				case "排序", "sortOrder", "Sort Order":
					headers["sort"] = col
				case "是否设置会议记录人", "会议记录人", "isRecorder", "Recorder":
					headers["recorder"] = col
				}
			}
			if len(headers) != 4 {
				return ErrWorkbook
			}
			headerFound = true
			return nil
		}
		name, dept, sort, recorder := fields[headers["name"]], fields[headers["department"]], fields[headers["sort"]], fields[headers["recorder"]]
		if name == "" && dept == "" && sort == "" && recorder == "" {
			return nil
		}
		order := int64(0)
		if sort != "" {
			order, err = strconv.ParseInt(sort, 10, 64)
			if err != nil {
				return ErrWorkbook
			}
		}
		flag := false
		switch strings.ToLower(recorder) {
		case "", "0", "false", "no", "n", "否":
		case "1", "true", "yes", "y", "是":
			flag = true
		default:
			return ErrWorkbook
		}
		values = append(values, domain.MeetingMember{Name: name, Department: dept, SortOrder: order, IsRecorder: flag})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if domain.ValidateMemberImport(values) != nil {
		return nil, ErrWorkbook
	}
	return values, nil
}
func cellColumn(reference string) (int, error) {
	column, index := 0, 0
	for index < len(reference) && reference[index] >= 'A' && reference[index] <= 'Z' {
		column = column*26 + int(reference[index]-'A'+1)
		if column > 16384 {
			return 0, ErrWorkbook
		}
		index++
	}
	if column == 0 || index == len(reference) {
		return 0, ErrWorkbook
	}
	number, err := strconv.Atoi(reference[index:])
	if err != nil || number < 1 || number > 1048576 {
		return 0, ErrWorkbook
	}
	return column - 1, nil
}
func cellValue(c cell, shared []string) (string, error) {
	switch c.Type {
	case "inlineStr":
		return c.Inline.value(), nil
	case "s":
		index, err := strconv.Atoi(c.Value)
		if err != nil || index < 0 || index >= len(shared) {
			return "", ErrWorkbook
		}
		return shared[index], nil
	case "b":
		if c.Value == "1" {
			return "true", nil
		}
		if c.Value == "0" {
			return "false", nil
		}
		return "", ErrWorkbook
	case "str":
		return unescapeSpreadsheet(c.Value), nil
	case "e", "d":
		return c.Value, nil
	case "", "n":
		if c.Value == "" {
			return "", nil
		}
		if _, err := strconv.ParseInt(c.Value, 10, 64); err == nil {
			return c.Value, nil
		}
		number, err := strconv.ParseFloat(c.Value, 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return "", ErrWorkbook
		}
		return strconv.FormatFloat(number, 'f', -1, 64), nil
	default:
		return "", ErrWorkbook
	}
}
func escape(value string) (string, error) {
	if !utf8.ValidString(value) || len([]rune(value)) > 32767 {
		return "", ErrWorkbook
	}
	for _, r := range value {
		if r < 32 && r != '\t' && r != '\n' && r != '\r' {
			return "", ErrWorkbook
		}
	}
	var protected strings.Builder
	for index := 0; index < len(value); index++ {
		if _, ok := spreadsheetEscapeUnit(value, index); ok {
			protected.WriteString("_x005F")
		}
		protected.WriteByte(value[index])
	}
	var b bytes.Buffer
	if xml.EscapeText(&b, []byte(protected.String())) != nil {
		return "", ErrWorkbook
	}
	return b.String(), nil
}
func Encode(values []domain.MeetingMember) ([]byte, error) {
	if len(values) > maxRows-1 {
		return nil, ErrWorkbook
	}
	var sheet strings.Builder
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	writeRow := func(index int, fields []string) error {
		fmt.Fprintf(&sheet, `<row r="%d">`, index)
		for column, value := range fields {
			text, err := escape(value)
			if err != nil {
				return err
			}
			if column == 2 && index > 1 {
				order, _ := strconv.ParseInt(value, 10, 64)
				if order >= -9007199254740991 && order <= 9007199254740991 {
					fmt.Fprintf(&sheet, `<c r="C%d"><v>%s</v></c>`, index, text)
					continue
				}
			}
			fmt.Fprintf(&sheet, `<c r="%c%d" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, 'A'+column, index, text)
		}
		sheet.WriteString(`</row>`)
		if sheet.Len() > maxExpanded/2 {
			return ErrWorkbook
		}
		return nil
	}
	if err := writeRow(1, []string{"姓名", "部门", "排序", "是否设置会议记录人"}); err != nil {
		return nil, err
	}
	for index, v := range values {
		flag := "否"
		if v.IsRecorder {
			flag = "是"
		}
		if err := writeRow(index+2, []string{v.Name, v.Department, strconv.FormatInt(v.SortOrder, 10), flag}); err != nil {
			return nil, err
		}
	}
	sheet.WriteString(`</sheetData></worksheet>`)
	parts := []struct{ name, body string }{
		{"[Content_Types].xml", `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`},
		{"_rels/.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/workbook.xml", `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="人员信息" sheetId="1" r:id="rId1"/></sheets></workbook>`},
		{"xl/_rels/workbook.xml.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`},
		{"xl/worksheets/sheet1.xml", sheet.String()},
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, part := range parts {
		f, err := writer.Create(part.name)
		if err != nil {
			return nil, ErrWorkbook
		}
		if _, err = f.Write([]byte(part.body)); err != nil {
			return nil, ErrWorkbook
		}
	}
	if writer.Close() != nil || output.Len() > maxArchive {
		return nil, ErrWorkbook
	}
	return output.Bytes(), nil
}

// SaveFile uses the same cancellation and destination checks as other exports.
// Encoding failures never change the destination; fileout stages and syncs the
// complete workbook before checking cancellation and publishing it.
func SaveFile(ctx context.Context, filename string, values []domain.MeetingMember) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(filename) || !strings.EqualFold(filepath.Ext(filename), ".xlsx") {
		return ErrFile
	}
	data, err := Encode(values)
	if err != nil {
		return err
	}
	err = fileout.Write(ctx, filename, data)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if err != nil {
		return ErrFile
	}
	return nil
}
