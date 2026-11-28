package liveaudio

import "testing"

// Loading APIs does not enumerate content or request recording permission.
func TestScreenAudioLoadsNativeInterfacesWithoutStartingCapture(t *testing.T) {
	api, err := loadScreenAudio()
	if err != nil {
		t.Skip("ScreenCaptureKit audio requires macOS 13 or later")
	}
	if api.output == 0 || api.buffers == nil || api.description == nil {
		t.Fatal("native audio interfaces incomplete")
	}
}
