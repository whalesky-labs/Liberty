package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeGUIAvailableMatchesSupportedPlatforms(t *testing.T) {
	for _, target := range []struct {
		os, arch  string
		supported bool
	}{
		{"darwin", "arm64", true},
		{"darwin", "amd64", true},
		{"windows", "amd64", true},
		{"windows", "386", false},
		{"windows", "arm64", false},
		{"linux", "amd64", false},
	} {
		if nativeGUIAvailable(target.os, target.arch) != target.supported {
			t.Fatalf("unexpected GUI support for %s/%s", target.os, target.arch)
		}
	}
}

func TestUnsupportedNativeGUIStopsBeforeProfileResolution(t *testing.T) {
	if nativeGUIAvailable(runtime.GOOS, runtime.GOARCH) {
		return // The unsupported path is executed by Linux CI.
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
	if !strings.Contains(message, "此系统架构不受支持") || !strings.Contains(message, "This system architecture is unsupported") || !strings.Contains(message, "Windows x64") {
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
