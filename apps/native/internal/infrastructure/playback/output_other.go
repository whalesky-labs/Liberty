//go:build !darwin && !windows

package playback

import "context"

func playWave(context.Context, []byte, string) error { return ErrPlayback }
