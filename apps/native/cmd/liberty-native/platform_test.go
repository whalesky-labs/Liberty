package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUnsupportedNativeGUIStopsBeforeProfileResolution(t *testing.T) {
	if nativeGUIAvailable(runtime.GOOS, runtime.GOARCH) {
		return // The unsupported path is executed by Linux and Windows x86 CI.
	}
	oldArgs, oldChannel := os.Args, launchChannel
	t.Cleanup(func() { os.Args, launchChannel = oldArgs, oldChannel })
	launchChannel = "candidate"
	directory := filepath.Join(t.TempDir(), "uncreated-profile")
	os.Args = []string{"liberty-native", "--profile-directory", directory, "--writable", "--prepare-database"}
	err := runWithProfileResolver(func(bool) (string, error) {
		t.Fatal("unsupported GUI resolved or prepared a profile")
		return "", nil
	})
	if !errors.Is(err, errNativeGUIUnsupported) {
		t.Fatal("unsupported GUI did not stop with a stable error", err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("unsupported GUI created a profile", err)
	}
	message := startupFailureMessage(errNativeGUIUnsupported)
	if !strings.Contains(message, "32 位 Windows") || !strings.Contains(message, "32-bit Windows") || !strings.Contains(message, "x64") {
		t.Fatal("unsupported GUI lacks actionable bilingual guidance")
	}
	os.Args = []string{"liberty-native", "--version"}
	if err := runWithProfileResolver(func(bool) (string, error) {
		t.Fatal("version output resolved a profile")
		return "", nil
	}); err != nil {
		t.Fatal("version metadata should remain available", err)
	}
}
