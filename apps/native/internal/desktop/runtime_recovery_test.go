package desktop

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

func TestRuntimeViewReadFailureHidesStaleControlsAndLoading(t *testing.T) {
	controller := &client.RuntimeController{Error: errors.New("runtime read failed"), ReadError: errors.New("runtime read failed")}
	view := &RuntimeView{Client: controller, Locale: "en-US"}
	tester := ui.NewTester(view.View, 1100, 1400)
	alert, ok := tester.AccessByRole(ui.RoleAlert, "Could not read runtime status. Please retry.")
	if !ok || alert.Offscreen {
		t.Fatal("runtime read failure did not expose an accessible alert", alert)
	}
	if tester.HasText("Loading…") || tester.HasText(view.msg("runtimeInstallAction")) {
		t.Fatal("runtime read failure exposed loading or stale controls")
	}
}

func TestRuntimeViewMatchesLegacyProgressAndSourceStatus(t *testing.T) {
	progress := uint32(25)
	python := domain.NewRuntimeComponent("python", "managed", "")
	python.Availability = "ready"
	python.Artifact = &domain.RuntimeArtifact{Path: "managed-python"}
	python.Operation = domain.RuntimeOperation{Kind: "validating", Progress: &progress}
	system := domain.NewRuntimeComponent("ffmpeg", "system", "")
	system.Availability = "ready"
	system.Artifact = &domain.RuntimeArtifact{Path: "system-ffmpeg"}
	models := domain.NewRuntimeComponent("model", "managed", "")
	panel := domain.RuntimePanel{
		Overview:       domain.RuntimeOverview{Status: "system_ready", Python: python, FFmpeg: system, Models: models},
		DownloadSource: "",
		Sources:        []domain.RuntimeSourceOption{{ID: "official", Name: "Official"}},
	}
	controller := &client.RuntimeController{Panel: &panel}
	view := &RuntimeView{Client: controller, Locale: "en-US"}
	tester := ui.NewTester(view.View, 1100, 1400)

	progressAccess, ok := tester.AccessByRole(ui.RoleProgress, view.msg("runtimeResourceValidating"))
	if !ok || progressAccess.Now != 25 {
		t.Fatal("active runtime validation was rendered as ready", progressAccess)
	}
	if !tester.HasText(view.msg("runtimeResourceSystemSelected")) || !tester.HasText(view.msg("runtimeDownloadSourceRequired")) {
		t.Fatal("runtime source status did not match the legacy settings panel")
	}
}
