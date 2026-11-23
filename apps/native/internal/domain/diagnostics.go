package domain

import "errors"

var ErrDiagnostics = errors.New("diagnostics_unavailable")
var ErrDiagnosticLog = errors.New("diagnostic_log_unavailable")

type DiagnosticPlatform struct{ ID, Label, GoTarget, ValidationLevel string }

func DiagnosticPlatforms() []DiagnosticPlatform {
	return []DiagnosticPlatform{
		{"darwin-aarch64", "macOS Apple Silicon", "darwin/arm64", "primary"},
		{"darwin-x64", "macOS Intel", "darwin/amd64", "primary"},
		{"windows-x64", "Windows x64", "windows/amd64", "primary"},
	}
}

type SecurityBaseline struct {
	NativeUI                bool
	ScopedCapabilities      bool
	CredentialStoreRequired bool
}

// DiagnosticReport carries neither profile/media paths nor raw runtime errors.
// Platform support is a target matrix; it is not evidence of execution on it.
type DiagnosticReport struct {
	AppVersion             string
	CurrentPlatform        *DiagnosticPlatform
	SupportedPlatforms     []DiagnosticPlatform
	SchemaVersion          int64
	RuntimeStatus          string
	DesktopPetLogAvailable bool
	SecurityBaseline       SecurityBaseline
}
