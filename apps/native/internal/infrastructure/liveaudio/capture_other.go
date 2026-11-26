//go:build !darwin && !windows

package liveaudio

func deviceListArgs() []string     { return nil }
func parseDevices(string) []device { return nil }
func captureArgs(string) []string  { return nil }
