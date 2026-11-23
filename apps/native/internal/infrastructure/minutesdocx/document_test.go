package minutesdocx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"liberty.local/liberty/internal/domain"
)

func parts(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	result := map[string][]byte{}
	for _, f := range r.File {
		in, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(in)
		in.Close()
		if err != nil {
			t.Fatal(err)
		}
		result[f.Name] = b
	}
	return result
}
func TestOriginalTemplatePartsAndSpecialTextRemainValid(t *testing.T) {
	input := domain.MinutesDocument{Title: "合成<&>会议纪要", Info: domain.MinutesInfo{MeetingName: "Synthetic & <meeting>"}, SpeechBlocks: []domain.MinutesSpeechBlock{{Name: "合成人员", Department: "A&B", WeeklySummary: []string{"<内容>", "2、已有序号"}, NextWeekPlan: []string{"\"计划\""}, Summary: []string{"个人总结"}}}, ClosingSummary: []string{"全局结论"}, FallbackOverview: []string{"决策：", "批准"}}
	data, err := Encode(input)
	if err != nil {
		t.Fatal(err)
	}
	original, output := parts(t, template), parts(t, data)
	if len(output) != len(original) {
		t.Fatal("template parts changed")
	}
	for name, content := range original {
		if name != "word/document.xml" && !bytes.Equal(content, output[name]) {
			t.Fatal("template styles/media changed", name)
		}
	}
	d := xml.NewDecoder(bytes.NewReader(output["word/document.xml"]))
	var texts []string
	wordNamespaceAttributes := 0
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if text, ok := token.(xml.CharData); ok {
			texts = append(texts, string(text))
		}
		if start, ok := token.(xml.StartElement); ok {
			for _, a := range start.Attr {
				if a.Name.Space == "http://schemas.openxmlformats.org/wordprocessingml/2006/main" {
					wordNamespaceAttributes++
				}
			}
		}
	}
	if wordNamespaceAttributes == 0 {
		t.Fatal("Word formatting attribute namespaces lost")
	}
	joined := strings.Join(texts, "|")
	for _, want := range []string{input.Title, "Synthetic & <meeting>", "A&B", "合成人员", "1、<内容>", "2、已有序号", "全局总结：", "1、全局结论", "决策、行动项、风险与跟进："} {
		if !strings.Contains(joined, want) {
			t.Error("missing document content", want)
		}
	}
	if _, err := encode(input, []byte("invalid template")); err == nil {
		t.Fatal("invalid template accepted")
	}
}
