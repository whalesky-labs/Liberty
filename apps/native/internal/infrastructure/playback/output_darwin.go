package playback

import (
	"context"
	"liberty.local/liberty/internal/infrastructure/runner"
	"os"
)

func playWave(ctx context.Context, data []byte, directory string) error {
	file, err := os.CreateTemp(directory, ".liberty-playback-*.wav")
	if err != nil {
		return ErrPlayback
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return ErrPlayback
	}
	// afplay is a fixed macOS system binary; user paths never become commands.
	return runner.RunCommand(ctx, "/usr/bin/afplay", []string{file.Name()}, directory, nil, nil)
}
