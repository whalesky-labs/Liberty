// Package minutesdocx fills the original Word template, preserving all other
// ZIP parts and paragraph/table styles. It does not invoke Python or Rust.
package minutesdocx

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"

	"liberty.local/liberty/internal/domain"
)

//go:embed meeting-minutes.docx
var template []byte
var ErrDocument = errors.New("minutes_document_invalid")

const maxDocument = 64 << 20

func Encode(data domain.MinutesDocument) ([]byte, error) { return encode(data, template) }
func encode(data domain.MinutesDocument, source []byte) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	if err != nil {
		return nil, ErrDocument
	}
	var output bytes.Buffer
	w := zip.NewWriter(&output)
	defer w.Close()
	found := false
	for _, f := range r.File {
		if f.UncompressedSize64 > maxDocument {
			return nil, ErrDocument
		}
		in, err := f.Open()
		if err != nil {
			return nil, ErrDocument
		}
		content, readErr := io.ReadAll(io.LimitReader(in, maxDocument+1))
		closeErr := in.Close()
		if readErr != nil || closeErr != nil || len(content) > maxDocument {
			return nil, ErrDocument
		}
		if f.Name == "word/document.xml" {
			if found {
				return nil, ErrDocument
			}
			found = true
			content, err = render(content, data)
			if err != nil {
				return nil, err
			}
		}
		part, err := w.CreateHeader(&zip.FileHeader{Name: f.Name, Method: zip.Deflate})
		if err != nil {
			return nil, ErrDocument
		}
		if _, err = part.Write(content); err != nil {
			return nil, ErrDocument
		}
		if output.Len() > maxDocument {
			return nil, ErrDocument
		}
	}
	if !found || w.Close() != nil || output.Len() > maxDocument {
		return nil, ErrDocument
	}
	return output.Bytes(), nil
}
func render(source []byte, data domain.MinutesDocument) ([]byte, error) {
	root, err := parse(source)
	if err != nil {
		return nil, err
	}
	body := root.first("body")
	if body == nil {
		return nil, ErrDocument
	}
	title := body.first("p")
	table := body.first("tbl")
	if title == nil || table == nil {
		return nil, ErrDocument
	}
	title.setText(data.Title)
	rows := table.elements("tr")
	if len(rows) < 7 {
		return nil, ErrDocument
	}
	for _, cell := range []struct {
		row, column int
		value       string
	}{{0, 1, fallback(data.Info.MeetingName, "待补充")}, {0, 3, fallback(data.Info.MeetingTime, "待补充")}, {0, 5, fallback(data.Info.MeetingLocation, "待补充")}, {0, 6, "记录人： " + fallback(data.Info.Recorder, "待补充")}, {1, 1, fallback(data.Info.Attendees, "待补充")}, {2, 1, data.Info.Absentees}, {3, 1, fallback(data.Topics, "待补充")}, {4, 1, fallback(data.Info.Host, "待补充")}, {4, 3, data.Info.Reviewer}} {
		cells := rows[cell.row].elements("tc")
		if len(cells) <= cell.column {
			return nil, ErrDocument
		}
		cells[cell.column].setText(cell.value)
	}
	sample := rows[6].clone()
	cells := sample.elements("tc")
	if len(cells) < 2 {
		return nil, ErrDocument
	}
	paragraphs := cells[1].elements("p")
	if len(paragraphs) < 13 {
		return nil, ErrDocument
	}
	heading, item := paragraphs[0].clone(), paragraphs[1].clone()
	kept := []*node{}
	count := 0
	for _, c := range table.children {
		if local(c.name) == "tr" {
			count++
			if count > 6 {
				continue
			}
		}
		kept = append(kept, c)
	}
	table.children = kept
	blocks := data.SpeechBlocks
	if len(blocks) == 0 {
		blocks = []domain.MinutesSpeechBlock{{}}
	}
	for _, block := range blocks {
		row := sample.clone()
		cells := row.elements("tc")
		left := cells[0].elements("p")
		if len(left) < 2 {
			return nil, ErrDocument
		}
		department, name := strings.TrimSpace(block.Department), strings.TrimSpace(block.Name)
		if department == "" {
			department = "待补充部门"
		}
		if name == "" {
			name = "待补充姓名"
		}
		left[0].setText(department)
		left[1].setText(name)
		cells[1].removeElements("p")
		for _, section := range []struct {
			heading string
			items   []string
		}{{"上周总结：", block.WeeklySummary}, {"本周计划：", block.NextWeekPlan}, {"个人总结：", block.Summary}} {
			cells[1].children = append(cells[1].children, cloneText(heading, section.heading))
			values := section.items
			if len(values) == 0 {
				values = []string{"待补充"}
			}
			for i, text := range values {
				text = strings.TrimSpace(text)
				if text == "" {
					text = fmt.Sprintf("%d、待补充", i+1)
				} else if !numbered(text) {
					text = fmt.Sprintf("%d、%s", i+1, text)
				}
				cells[1].children = append(cells[1].children, cloneText(item, text))
			}
		}
		table.children = append(table.children, row)
	}
	for _, section := range []struct {
		heading string
		items   []string
	}{{"全局总结：", data.ClosingSummary}, {"决策、行动项、风险与跟进：", data.FallbackOverview}} {
		values := []string{}
		for _, text := range section.items {
			if text = strings.TrimSpace(text); text != "" {
				values = append(values, text)
			}
		}
		if len(values) == 0 {
			continue
		}
		appendBody(body, cloneText(heading, section.heading))
		for i, text := range values {
			if !strings.HasSuffix(text, "：") && !strings.HasSuffix(text, ":") && !numbered(text) {
				text = fmt.Sprintf("%d、%s", i+1, text)
			}
			appendBody(body, cloneText(item, text))
		}
	}
	var output bytes.Buffer
	output.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	root.write(&output)
	if output.Len() > maxDocument {
		return nil, ErrDocument
	}
	// Parse the output with namespace-aware validation before publishing it.
	d := bytes.NewReader(output.Bytes())
	if _, err := parseReader(d); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
func parseReader(r io.Reader) (bool, error) {
	d := xml.NewDecoder(r)
	for {
		_, err := d.Token()
		if err == io.EOF {
			return true, nil
		}
		if err != nil {
			return false, ErrDocument
		}
	}
}
func appendBody(body, p *node) {
	for i, c := range body.children {
		if local(c.name) == "sectPr" {
			body.children = append(body.children, nil)
			copy(body.children[i+1:], body.children[i:])
			body.children[i] = p
			return
		}
	}
	body.children = append(body.children, p)
}
func fallback(value, other string) string {
	value = strings.TrimSpace(value)
	switch value {
	case "", "待补充", "待补充部门", "待补充姓名":
		return other
	}
	return value
}
func numbered(value string) bool {
	seen := false
	for _, r := range strings.TrimSpace(value) {
		if r >= '0' && r <= '9' {
			seen = true
			continue
		}
		return seen && strings.ContainsRune("、.）)", r)
	}
	return false
}
