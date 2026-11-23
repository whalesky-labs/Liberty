package diagnostics

import (
	"strings"
	"testing"
)

func TestSecretsPathsAndBoundedUnicode(t *testing.T) {
	output := Text(`api_key=hidden_a Bearer hidden_b {"token": "hidden value"} C:\Synthetic Folder\private.wav`, 4096)
	for _, secret := range []string{"hidden", "Synthetic", "private.wav"} {
		if strings.Contains(output, secret) {
			t.Fatal("diagnostic leak")
		}
	}
	if Text("中文示例", 2) != "中文" {
		t.Fatal("invalid unicode truncation")
	}
	if Text("text", -1) != "" {
		t.Fatal("negative bound")
	}
}

func TestTerminalControlsCannotReachUIOrSplitSensitiveKeys(t *testing.T) {
	output := Text("\x1b[31mapi_\x1b[0mkey=hidden_value\x00 \x1b]8;;https://example.invalid/private\x07link\x1b]8;;\x07\rready", 4096)
	if strings.Contains(output, "hidden") || strings.Contains(output, "private") || strings.ContainsAny(output, "\x1b\x00\x07\r") {
		t.Fatal("terminal control or secret leaked")
	}
	if !strings.Contains(output, "ready") {
		t.Fatal("diagnostic content lost")
	}
}
