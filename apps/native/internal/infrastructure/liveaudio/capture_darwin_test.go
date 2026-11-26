package liveaudio

import (
	"strings"
	"testing"
)

func TestLiveDevicesOnlySelectEnumeratedAudioInputs(t *testing.T) {
	output := "[AVFoundation @ 0x1] AVFoundation video devices:\n[AVFoundation @ 0x1] [0] Synthetic camera\n[AVFoundation @ 0x1] AVFoundation audio devices:\n[AVFoundation @ 0x1] [0] Synthetic microphone\n[AVFoundation @ 0x1] [1] Synthetic headset\n"
	devices := parseDevices(output)
	if len(devices) != 2 || devices[0].input != ":0" || devices[1].input != ":1" {
		t.Fatal("device parser mixed video and audio", devices)
	}
	changed := parseDevices(strings.ReplaceAll(output, "Synthetic microphone", "Changed microphone"))
	if changed[0].public.ID == devices[0].public.ID {
		t.Fatal("device index reuse was not detected")
	}
	if got := parseDevices("permission denied"); len(got) != 0 {
		t.Fatal("invented a default device")
	}
	args := strings.Join(captureArgs(devices[0].input), " ")
	if !strings.Contains(args, "-ac 1 -ar 16000 -c:a pcm_s16le") || !strings.HasSuffix(args, "pipe:1") {
		t.Fatal("capture format drift", args)
	}
}
