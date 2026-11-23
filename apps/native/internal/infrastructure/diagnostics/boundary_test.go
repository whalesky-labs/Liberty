package diagnostics

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestSensitiveValuesAreRemovedCompletely(t *testing.T) {
	for name, input := range map[string]string{
		"escaped-double-quote": `{"token":"synthetic\"private_tail", "status":"ready"}`,
		"escaped-single-quote": `secret='synthetic\'private_tail'; status=ready`,
		"python-dict":          `{'token': 'synthetic_private_tail', 'status': 'ready'}`,
		"nested-object":        `{"providerBody":{"first":"synthetic", "choices":[{"text":"private_tail"}]},"status":"ready"}`,
		"nested-array":         `transcript=["synthetic_first",{"text":"synthetic_private_tail"}]; status=ready`,
		"quoted-delimiters":    `providerBody={"text":"braces } ] and \"synthetic_private_tail"}; status=ready`,
	} {
		t.Run(name, func(t *testing.T) {
			output := Text(input, 4096)
			if strings.Contains(output, "synthetic") || strings.Contains(output, "private_tail") {
				t.Fatal("sensitive value was only partially removed")
			}
			if !strings.Contains(output, "[redacted]") || !strings.Contains(output, "ready") {
				t.Fatal("complete value redaction lost the following diagnostic status")
			}
		})
	}
}

func TestIncompleteSensitiveValuesFailClosed(t *testing.T) {
	for name, input := range map[string]string{
		"quoted":     `token="synthetic private_tail`,
		"object":     `providerBody={"text":"synthetic", "more":"private_tail"`,
		"array":      `transcript=["synthetic", "private_tail"`,
		"mismatched": `providerBody={"text":["synthetic"]]; private_tail`,
	} {
		t.Run(name, func(t *testing.T) {
			output := Text(input, 4096)
			if strings.Contains(output, "synthetic") || strings.Contains(output, "private_tail") || !strings.Contains(output, "[redacted]") {
				t.Fatal("incomplete sensitive value leaked a suffix")
			}
		})
	}
}

func TestRedactionPreservesOrdinaryDiagnosticsAndIsRepeatable(t *testing.T) {
	for _, input := range []string{
		"模型加载完成，正在转写。", "stage=transcribing progress=32%", "ready at 2026-11-20T08:00:00Z",
		`{"status":"ready","count":2}`, `token=[redacted]; path="[redacted-path]"; ready`,
	} {
		if Text(input, 4096) != input {
			t.Fatal("ordinary diagnostic or prior redaction changed")
		}
	}
	for _, input := range []string{
		`{"token":"first\"private_tail", "status":"ready"}`,
		`open '/synthetic/private folder.wav' failed`,
		"open /synthetic/private folder.wav\nready",
	} {
		first := Text(input, 4096)
		if Text(first, 4096) != first {
			t.Fatal("a second diagnostic projection changed the redacted message")
		}
	}
}

func FuzzTextBoundsAndStructuredValues(f *testing.F) {
	for _, seed := range []string{"plain", "引号\"和换行\n", "\\\\host\\file", "}]}", "\x1b[31m\xff", strings.Repeat("[", 128)} {
		f.Add(seed, 128)
	}
	f.Fuzz(func(t *testing.T, text string, limit int) {
		// Exercise arbitrary malformed logs without allocating arbitrarily large
		// fixture payloads; the production Runner also bounds each input line.
		if len(text) > 64<<10 {
			t.Skip()
		}
		output := Text(text, limit)
		if !utf8.ValidString(output) || utf8.RuneCountInString(output) > max(0, limit) || strings.IndexFunc(output, unicode.IsControl) >= 0 {
			t.Fatal("diagnostic output is invalid, unbounded or contains controls")
		}
		encoded, err := json.Marshal(map[string]any{"providerBody": []any{text, map[string]string{"text": "private_tail"}}, "status": "ready"})
		if err != nil {
			t.Fatal("synthetic JSON could not be encoded")
		}
		output = Text(string(encoded), 4096)
		if strings.Contains(output, "private_tail") || !strings.Contains(output, `"status":"ready"`) {
			t.Fatal("structured value boundary was not respected")
		}
	})
}

func TestLocationsWithSpacesAndUNCDoNotLeakSuffixes(t *testing.T) {
	for name, input := range map[string]string{
		"quoted-posix":   `open '/synthetic/private_folder private_tail.wav' failed`,
		"unquoted-posix": "open /synthetic/private_folder private_tail.wav\nready",
		"quoted-drive":   `open "C:\synthetic folder\private_tail.wav" failed`,
		"unc":            `open \\synthetic-host\share\private_tail.wav`,
		"quoted-unc":     `open '\\synthetic-host\private folder\private_tail.wav' failed`,
		"json-escaped":   `{"path":"C:\\synthetic folder\\private_tail.wav","status":"ready"}`,
		"escaped-slash":  `{"path":"\/synthetic\/private folder\/private_tail.wav","status":"ready"}`,
	} {
		t.Run(name, func(t *testing.T) {
			output := Text(input, 4096)
			if strings.Contains(output, "synthetic") || strings.Contains(output, "private_tail") || !strings.Contains(output, "[redacted-path]") {
				t.Fatal("location redaction leaked a component")
			}
			if strings.Contains(input, "failed") && !strings.Contains(output, "failed") || strings.Contains(input, "ready") && !strings.Contains(output, "ready") {
				t.Fatal("redaction consumed the status outside the delimited path")
			}
		})
	}
}
