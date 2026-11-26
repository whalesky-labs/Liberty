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
	for _, key := range []string{"runtimeResourceSystemSelected", "runtimeDownloadSourceRequired"} {
		_, progressOK := tester.AccessByRole(ui.RoleProgress, view.msg(key))
		_, captionOK := tester.AccessByRole(ui.RoleText, view.msg(key))
		if !progressOK || !captionOK {
			t.Fatal("runtime source status should be visible beside its progress", key)
		}
	}
}

func TestRuntimeComponentControlColumnsStayAligned(t *testing.T) {
	view := &RuntimeView{Client: &client.RuntimeController{}, Locale: "en-US"}
	var selectorX, progressX, actionX float32
	for index, component := range []string{"python", "ffmpeg", "model"} {
		state := domain.NewRuntimeComponent(component, "managed", "0")
		tester := ui.NewTester(func(context *ui.Context) {
			ui.Column(context).Fill().Children(func() { view.component(context, state, "official", false) })
		}, 1100, 180)
		if index == 0 {
			captureParity(t, tester, "runtime-component-row-en-US")
		}
		progress, ok := tester.AccessByRole(ui.RoleProgress, view.msg("runtimeResourcePending"))
		if !ok || progress.Offscreen {
			t.Fatal("runtime component progress is missing", component, progress)
		}
		buttonLabel := view.msg(runtimeNameKey(component)) + " · " + view.msg("runtimeResourceDownload")
		button, ok := tester.AccessByRole(ui.RoleButton, buttonLabel)
		if !ok || button.Offscreen {
			t.Fatal("runtime component action is missing", component, button)
		}
		if index == 0 {
			selector, ok := tester.AccessByRole(ui.RolePopUpButton, view.msg(runtimeNameKey(component))+" "+view.msg("runtimeResourceSource"))
			if !ok || selector.Offscreen {
				t.Fatal("managed runtime source selector is missing", selector)
			}
			selectorX, progressX, actionX = selector.Bounds.X, progress.Bounds.X, button.Bounds.X
			continue
		}
		if progress.Bounds.X != progressX || button.Bounds.X != actionX {
			t.Fatalf("runtime resource controls are not aligned: %s progress=%+v action=%+v, expected x=%v/%v", component, progress.Bounds, button.Bounds, progressX, actionX)
		}
		if component == "ffmpeg" {
			selector, ok := tester.AccessByRole(ui.RolePopUpButton, view.msg(runtimeNameKey(component))+" "+view.msg("runtimeResourceSource"))
			if !ok || selector.Bounds.X != selectorX {
				t.Fatalf("runtime source selectors are not aligned: %+v, expected x=%v", selector, selectorX)
			}
		}
	}
}
