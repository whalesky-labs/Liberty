package desktop

import (
	"errors"
	"fmt"
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

func TestRuntimeViewShowsProgressOnlyForActiveResources(t *testing.T) {
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
	for _, key := range []string{"runtimeResourceSystemSelected", "runtimeDownloadSourceMissingHint"} {
		_, progressOK := tester.AccessByRole(ui.RoleProgress, view.msg(key))
		_, statusOK := tester.AccessByRole(ui.RoleStatus, view.msg(key))
		if progressOK || !statusOK {
			t.Fatal("idle runtime resources should expose status without a progress bar", key)
		}
	}
}

func TestRuntimeComponentResourceActionsStayAligned(t *testing.T) {
	view := &RuntimeView{Client: &client.RuntimeController{}, Locale: "en-US"}
	var configurationX, actionX float32
	for index, component := range []string{"python", "ffmpeg", "model"} {
		state := domain.NewRuntimeComponent(component, "managed", "0")
		tester := ui.NewTester(func(context *ui.Context) {
			ui.Column(context).Fill().Children(func() { view.component(context, state, "official", false) })
		}, 1100, 180)
		if index == 0 {
			captureParity(t, tester, "runtime-component-row-en-US")
		}
		if _, ok := tester.AccessByRole(ui.RoleProgress, view.msg("runtimeResourcePending")); ok {
			t.Fatal("idle resource should not display an empty progress bar", component)
		}
		if status, ok := tester.AccessByRole(ui.RoleStatus, view.msg("runtimeResourcePending")); !ok || status.Offscreen {
			t.Fatal("runtime component status is missing", component, status)
		}
		buttonLabel := view.msg(runtimeNameKey(component)) + " · " + view.msg("runtimeResourceDownload")
		button, ok := tester.AccessByRole(ui.RoleButton, buttonLabel)
		if !ok || button.Offscreen {
			t.Fatal("runtime component action is missing", component, button)
		}
		if index == 0 {
			configuration, ok := tester.AccessByRole(ui.RoleButton, view.msg(runtimeNameKey(component))+" · "+view.msg("runtimeResourceConfigure"))
			if !ok || configuration.Offscreen {
				t.Fatal("managed runtime configuration action is missing", configuration)
			}
			configurationX, actionX = configuration.Bounds.X, button.Bounds.X
			continue
		}
		if button.Bounds.X != actionX {
			t.Fatalf("runtime resource actions are not aligned: %s action=%+v, expected x=%v", component, button.Bounds, actionX)
		}
		if component == "ffmpeg" {
			configuration, ok := tester.AccessByRole(ui.RoleButton, view.msg(runtimeNameKey(component))+" · "+view.msg("runtimeResourceConfigure"))
			if !ok || configuration.Bounds.X != configurationX {
				t.Fatalf("runtime configuration actions are not aligned: %+v, expected x=%v", configuration, configurationX)
			}
		}
	}
}

func TestRuntimeResourceListLayoutsAcrossOperationStates(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, dark := range []bool{false, true} {
			for _, width := range []int{1440, 1100, 760} {
				for _, state := range []string{"missing", "installing", "failed", "ready"} {
					name := fmt.Sprintf("%s-%s-%d-dark-%v", state, locale, width, dark)
					t.Run(name, func(t *testing.T) {
						panel := runtimeInteractionPanel()
						switch state {
						case "installing":
							progress := uint32(25)
							panel.Overview.Python.Operation = domain.RuntimeOperation{Kind: "validating", Progress: &progress}
							panel.Overview.FFmpeg.Operation.Kind = "downloading"
							panel.Overview.Models.Operation.Kind = "waiting_for_python"
						case "failed":
							panel.Overview.Python.Operation.Kind = "failed"
							panel.Overview.FFmpeg = domain.NewRuntimeComponent("ffmpeg", "system", "0")
							panel.Overview.Models.Operation.Kind = "failed"
						case "ready":
							for _, component := range []*domain.RuntimeComponent{&panel.Overview.Python, &panel.Overview.FFmpeg, &panel.Overview.Models} {
								component.Availability = "ready"
								component.Artifact = &domain.RuntimeArtifact{Path: "synthetic-runtime"}
							}
						}
						panel.Overview.Aggregate()
						view := &RuntimeView{Client: &client.RuntimeController{Panel: &panel}, Locale: locale}
						shell := Shell{Locale: locale, Route: "/settings", Preferences: domain.DefaultUIPreferences(), Sidebar: view.Sidebar, Content: view.View}
						tester := ui.NewTesterWith(shell.View, width, 1100, ui.TesterOptions{Dark: dark})
						for _, component := range []domain.RuntimeComponent{panel.Overview.Python, panel.Overview.FFmpeg, panel.Overview.Models} {
							view.section = "localRuntime"
							if component.Component == "model" {
								view.section = "speechModels"
							}
							tester.Frame()
							key := "runtimeResourceDownload"
							if component.Ready() {
								key = "runtimeResourceRedownload"
							}
							if component.SourceName() == "system" {
								key = "runtimeResourceDetect"
							}
							label := view.msg(runtimeNameKey(component.Component)) + " · " + view.msg(key)
							button, ok := tester.AccessByRole(ui.RoleButton, label)
							if !ok || button.Offscreen || button.Bounds.W < 36 || button.Bounds.X+button.Bounds.W > float32(width) || button.Disabled != component.Operation.Active() {
								t.Fatal("resource action was clipped or lost its operation state", component.Component, button)
							}
						}
						progressCount := 0
						hasPercent := false
						for _, section := range []string{"localRuntime", "speechModels"} {
							view.section = section
							tester.Frame()
							hasPercent = hasPercent || tester.HasText("25%")
							if tester.HasText("36%") {
								t.Fatal("indeterminate progress presented as a percentage")
							}
							for _, node := range tester.AccessAll() {
								if node.Role == ui.RoleProgress {
									progressCount++
									if node.Offscreen || node.Bounds.W <= 0 || node.Bounds.X+node.Bounds.W > float32(width) {
										t.Fatal("active progress clipped", node)
									}
								}
							}
						}
						if state == "installing" && (progressCount != 3 || !hasPercent) || state != "installing" && progressCount != 0 {
							t.Fatal("progress state changed", progressCount)
						}
						view.section = "localRuntime"
						tester.Frame()
						if dark && width == 1100 {
							captureParity(t, tester, "runtime-resource-list-"+name)
						}
						if state == "missing" {
							for _, component := range []string{"python", "ffmpeg"} {
								label := view.msg(runtimeNameKey(component)) + " · " + view.msg("runtimeResourceConfigure")
								runtimeClickControl(t, tester, ui.RoleButton, label)
								selector := view.msg(runtimeNameKey(component)) + " " + view.msg("runtimeResourceSource")
								if control, ok := tester.AccessByRole(ui.RolePopUpButton, selector); !ok || control.Offscreen || control.Bounds.X+control.Bounds.W > float32(width) {
									t.Fatal("expanded resource configuration was clipped", control)
								}
							}
							if _, ok := tester.AccessByRole(ui.RolePopUpButton, view.msg("runtimeResourcePython")+" "+view.msg("runtimeResourceSource")); ok {
								t.Fatal("opening a different configuration left the previous resource expanded")
							}
							if dark && width == 1100 {
								captureParity(t, tester, "runtime-configuration-"+locale)
							}
						}
					})
				}
			}
		}
	}
}
