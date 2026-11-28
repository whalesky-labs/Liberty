//go:build !darwin && !windows

package liveaudio

import (
	"context"
	"liberty.local/liberty/internal/domain"
)

func systemAudioAvailable() bool { return false }
func captureSystemAudio(context.Context, func([]byte) error) error {
	return domain.ErrRuntimeUnsupported
}
