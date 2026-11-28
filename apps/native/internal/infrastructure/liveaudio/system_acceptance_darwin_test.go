//go:build audio_acceptance

package liveaudio

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ebitengine/purego"
	"liberty.local/liberty/internal/domain"
)

func TestSystemAudioSyntheticPlaybackAcceptance(t *testing.T) {
	if !systemAudioAvailable() {
		t.Skip("ScreenCaptureKit audio unavailable")
	}
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		t.Fatal("CoreGraphics unavailable")
	}
	defer purego.Dlclose(lib)
	var allowed func() bool
	purego.RegisterLibFunc(&allowed, lib, "CGPreflightScreenCaptureAccess")
	if !allowed() {
		t.Skip("System audio capture permission is not granted; no prompt or capture was started")
	}
	pcm := make([]byte, 32000*3)
	for i := 0; i < len(pcm)/2; i++ {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(int16(math.Sin(float64(i)*2*math.Pi*440/16000)*8192)))
	}
	wav := make([]byte, 44+len(pcm))
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(36+len(pcm)))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], 16000)
	binary.LittleEndian.PutUint32(wav[28:], 32000)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], uint32(len(pcm)))
	copy(wav[44:], pcm)
	path := filepath.Join(t.TempDir(), "synthetic-tone.wav")
	if os.WriteFile(path, wav, 0600) != nil {
		t.Fatal("synthetic fixture failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	played := make(chan error, 1)
	go func() {
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			played <- ctx.Err()
			return
		case <-timer.C:
		}
		played <- exec.CommandContext(ctx, "/usr/bin/afplay", path).Run()
	}()
	active := 0
	err = captureSystemAudio(ctx, func(frame []byte) error {
		if len(frame) != domain.LiveFrameBytes {
			return domain.ErrLiveCapture
		}
		energy := float64(0)
		for i := 0; i < len(frame); i += 2 {
			v := float64(int16(binary.LittleEndian.Uint16(frame[i:])))
			energy += v * v
		}
		if energy > 1000000 {
			active++
		}
		return nil
	})
	playErr := <-played
	if !errors.Is(err, context.DeadlineExceeded) || active < 5 || playErr != nil {
		t.Fatal("synthetic system capture did not provide valid audio or clean shutdown", err, active, playErr)
	}
	t.Log("System audio delivered non-silent 16 kHz PCM frames and stopped cleanly. No captured audio was saved.")
}
