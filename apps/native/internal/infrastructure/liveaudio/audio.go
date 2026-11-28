// Package liveaudio adapts the pinned ffmpeg platform capture devices to PCM.
// It never chooses an unvalidated system executable or an arbitrary input URL.
package liveaudio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/execution"
	"liberty.local/liberty/internal/infrastructure/runner"
)

type Audio struct {
	Runtime   execution.RuntimeResolver
	Directory string
}
type device struct {
	public domain.AudioDevice
	input  string
}

func deviceID(index int, name string) string {
	sum := sha256.Sum256([]byte(name))
	return strconv.Itoa(index) + ":" + hex.EncodeToString(sum[:8])
}

func (a *Audio) devices(ctx context.Context) (execution.Runtime, []device, error) {
	r, err := a.Runtime.Resolve(ctx)
	if err != nil {
		return r, nil, err
	}
	args := deviceListArgs()
	if len(args) == 0 {
		return r, nil, domain.ErrRuntimeUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	output, err := runner.CaptureDeviceOutput(ctx, r.FFmpeg, args, a.Directory)
	if err != nil {
		return r, nil, err
	}
	return r, parseDevices(output), nil
}
func (a *Audio) Devices(ctx context.Context) ([]domain.AudioDevice, error) {
	_, devices, err := a.devices(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.AudioDevice, 0, len(devices))
	for _, d := range devices {
		result = append(result, d.public)
	}
	if systemAudioAvailable() {
		result = append(result, domain.AudioDevice{ID: systemDeviceID, Name: "系统声音 / System audio"})
		for _, d := range devices {
			result = append(result, domain.AudioDevice{ID: mixedDevicePrefix + d.public.ID, Name: "系统声音 + / System + " + d.public.Name})
		}
	}
	return result, nil
}
func (a *Audio) Capture(ctx context.Context, id string, consume func([]byte) error) error {
	if id == systemDeviceID {
		return clockedCapture(ctx, consume, captureSystemAudio)
	}
	if microphone, ok := strings.CutPrefix(id, mixedDevicePrefix); ok {
		if microphone == "" || strings.HasPrefix(microphone, "system") {
			return domain.ErrLiveInput
		}
		return mixCapture(ctx, captureSystemAudio, func(ctx context.Context, emit func([]byte) error) error { return a.Capture(ctx, microphone, emit) }, consume)
	}
	r, devices, err := a.devices(ctx)
	if err != nil {
		return err
	}
	for _, d := range devices {
		if d.public.ID == id {
			return runner.CapturePCM(ctx, r.FFmpeg, captureArgs(d.input), a.Directory, consume)
		}
	}
	return domain.ErrLiveInput
}
