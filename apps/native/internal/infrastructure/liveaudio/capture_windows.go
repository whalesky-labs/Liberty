package liveaudio

import (
	"liberty.local/liberty/internal/domain"
	"regexp"
	"strings"
)

func deviceListArgs() []string {
	return []string{"-hide_banner", "-nostdin", "-list_devices", "true", "-f", "dshow", "-i", "dummy"}
}

var dsDevice = regexp.MustCompile(`"(.+)" \(audio\)`)
var dsAlternative = regexp.MustCompile(`Alternative name "(.+)"`)

func parseDevices(output string) []device {
	result := []device{}
	pending := -1
	for _, line := range strings.Split(output, "\n") {
		if match := dsDevice.FindStringSubmatch(line); len(match) == 2 && len(match[1]) <= 256 && len(result) < 128 {
			pending = len(result)
			result = append(result, device{domain.AudioDevice{ID: deviceID(pending, match[1]), Name: match[1]}, "audio=" + match[1]})
			continue
		}
		if pending >= 0 {
			if match := dsAlternative.FindStringSubmatch(line); len(match) == 2 && len(match[1]) <= 512 {
				result[pending].input = "audio=" + match[1]
				result[pending].public.ID = deviceID(pending, match[1])
				pending = -1
			} else if strings.Contains(line, "(video)") {
				pending = -1
			}
		}
	}
	return result
}
func captureArgs(input string) []string {
	return []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-f", "dshow", "-audio_buffer_size", "100", "-i", input, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", "-f", "s16le", "pipe:1"}
}
