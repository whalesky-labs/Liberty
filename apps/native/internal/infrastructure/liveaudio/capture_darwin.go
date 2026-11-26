package liveaudio

import (
	"liberty.local/liberty/internal/domain"
	"regexp"
	"strconv"
	"strings"
)

func deviceListArgs() []string {
	return []string{"-hide_banner", "-nostdin", "-f", "avfoundation", "-list_devices", "true", "-i", ""}
}

var avDevice = regexp.MustCompile(`\[(\d+)\] (.+)$`)

func parseDevices(output string) []device {
	result := []device{}
	audio := false
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "AVFoundation audio devices:") {
			audio = true
			continue
		}
		if strings.Contains(line, "AVFoundation video devices:") {
			audio = false
			continue
		}
		if !audio {
			continue
		}
		match := avDevice.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}
		index, err := strconv.Atoi(match[1])
		name := strings.TrimSpace(match[2])
		if err != nil || index > 1024 || len(name) > 256 || len(result) >= 128 {
			continue
		}
		result = append(result, device{domain.AudioDevice{ID: deviceID(index, name), Name: name}, ":" + match[1]})
	}
	return result
}
func captureArgs(input string) []string {
	return []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-f", "avfoundation", "-i", input, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", "-f", "s16le", "pipe:1"}
}
