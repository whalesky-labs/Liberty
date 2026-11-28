package desktop

import (
	"context"
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

type runtimeInteractionCall struct{ operation, component, source string }

type runtimeInteractionService struct {
	panel     domain.RuntimePanel
	calls     chan runtimeInteractionCall
	nextError error
}

func (service *runtimeInteractionService) Panel(context.Context) (domain.RuntimePanel, error) {
	return service.panel, nil
}
func (service *runtimeInteractionService) Install(_ context.Context, component string) error {
	service.calls <- runtimeInteractionCall{operation: "install", component: component}
	return service.nextError
}
func (service *runtimeInteractionService) InstallAll(context.Context) error {
	service.calls <- runtimeInteractionCall{operation: "install-all"}
	return service.nextError
}
func (service *runtimeInteractionService) Detect(_ context.Context, component string) error {
	service.calls <- runtimeInteractionCall{operation: "detect", component: component}
	return service.nextError
}
func (service *runtimeInteractionService) SwitchSource(_ context.Context, component, source string) error {
	service.calls <- runtimeInteractionCall{operation: "switch", component: component, source: source}
	return service.nextError
}
func (service *runtimeInteractionService) SelectDownloadSource(_ context.Context, source string) error {
	service.calls <- runtimeInteractionCall{operation: "download-source", source: source}
	return service.nextError
}

func runtimeInteractionPanel() domain.RuntimePanel {
	return domain.RuntimePanel{
		Overview: domain.RuntimeOverview{
			Status: "missing",
			Python: domain.NewRuntimeComponent("python", "managed", "0"),
			FFmpeg: domain.NewRuntimeComponent("ffmpeg", "managed", "0"),
			Models: domain.NewRuntimeComponent("model", "managed", "0"),
		},
		DownloadSource: "official",
		Sources:        []domain.RuntimeSourceOption{{ID: "official", Name: "官方源"}, {ID: "nju", Name: "南京大学"}},
	}
}

func runtimeClickControl(t *testing.T, tester *ui.Tester, role ui.Role, label string) {
	t.Helper()
	button, ok := tester.AccessByRole(role, label)
	if !ok || button.Offscreen || button.Disabled {
		t.Fatal("runtime action is not available", label, button)
	}
	tester.ClickAt(button.Bounds.X+button.Bounds.W/2, button.Bounds.Y+button.Bounds.H/2)
}

func TestRuntimeComponentActionsDelegateTheNamedComponentAndSource(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, component := range []string{"python", "ffmpeg", "model"} {
			for _, source := range []string{"managed", "system"} {
				if component == "model" && source == "system" {
					continue
				}
				t.Run(locale+"/"+component+"/"+source, func(t *testing.T) {
					callbacks := make(chan func(), 8)
					service := &runtimeInteractionService{panel: runtimeInteractionPanel(), calls: make(chan runtimeInteractionCall, 8)}
					controller := client.NewRuntime(service, func(callback func()) { callbacks <- callback }, func() {})
					defer controller.Close()
					controller.Panel = &service.panel
					view := &RuntimeView{Client: controller, Locale: locale}
					state := domain.NewRuntimeComponent(component, source, "0")
					tester := ui.NewTester(func(context *ui.Context) {
						ui.Column(context).Fill().Children(func() { view.component(context, state, "official", false) })
					}, 1100, 400)
					operation, key := "install", "runtimeResourceDownload"
					if source == "system" {
						operation, key = "detect", "runtimeResourceDetect"
					}
					label := view.msg(runtimeNameKey(component)) + " · " + view.msg(key)
					button, ok := tester.AccessByRole(ui.RoleButton, label)
					if !ok || button.Disabled || button.Offscreen {
						t.Fatal("component action has no distinct accessible identity", label, button)
					}
					if err := tester.Click(label); err != nil {
						t.Fatal(err)
					}
					flushView(t, callbacks, tester)
					if got := <-service.calls; got != (runtimeInteractionCall{operation: operation, component: component}) {
						t.Fatal("component action delegated the wrong operation", got)
					}
					if component == "model" {
						if _, ok := tester.AccessByRole(ui.RolePopUpButton, ""); ok {
							t.Fatal("model exposed an unsupported system source selector")
						}
						return
					}
					selector := view.msg(runtimeNameKey(component)) + " " + view.msg("runtimeResourceSource")
					if _, ok := tester.AccessByRole(ui.RolePopUpButton, selector); ok {
						t.Fatal("resource configuration should be collapsed initially")
					}
					configuration := view.msg(runtimeNameKey(component)) + " · " + view.msg("runtimeResourceConfigure")
					runtimeClickControl(t, tester, ui.RoleButton, configuration)
					if button, ok := tester.AccessByRole(ui.RoleButton, configuration); !ok || !button.Expanded {
						t.Fatal("configuration did not announce its expanded state", button)
					}
					if err := tester.Click(selector); err != nil {
						t.Fatal(err)
					}
					tester.Key(0, ui.KeyHome)
					nextSource := "managed"
					if source == "managed" {
						nextSource = "system"
						tester.Key(0, ui.KeyDown)
					}
					tester.Key(0, ui.KeyEnter)
					flushView(t, callbacks, tester)
					if got := <-service.calls; got != (runtimeInteractionCall{operation: "switch", component: component, source: nextSource}) {
						t.Fatal("source selector changed a different runtime resource", got)
					}
					runtimeClickControl(t, tester, ui.RoleButton, configuration)
					if _, ok := tester.AccessByRole(ui.RolePopUpButton, selector); ok {
						t.Fatal("resource configuration did not collapse")
					}
				})
			}
		}
	}
}

func TestRuntimeComponentDisabledStatesNeverDelegate(t *testing.T) {
	for _, component := range []string{"python", "ffmpeg", "model"} {
		for _, reason := range []string{"busy", "active", "unsupported", "component-unsupported", "missing-source"} {
			t.Run(component+"/"+reason, func(t *testing.T) {
				service := &runtimeInteractionService{panel: runtimeInteractionPanel(), calls: make(chan runtimeInteractionCall, 8)}
				controller := client.NewRuntime(service, func(callback func()) { t.Error("disabled control scheduled a callback") }, func() {})
				defer controller.Close()
				controller.Panel = &service.panel
				view := &RuntimeView{Client: controller, Locale: "en-US", expandedComponent: component}
				state := domain.NewRuntimeComponent(component, "managed", "0")
				source := "official"
				if reason == "busy" {
					controller.Busy = true
				}
				if reason == "active" {
					state.Operation.Kind = "validating"
				}
				if reason == "missing-source" {
					source = ""
				}
				if reason == "component-unsupported" {
					state.Availability = "unsupported"
				}
				tester := ui.NewTester(func(context *ui.Context) {
					ui.Column(context).Fill().Children(func() { view.component(context, state, source, reason == "unsupported") })
				}, 1100, 400)
				label := view.msg(runtimeNameKey(component)) + " · " + view.msg("runtimeResourceDownload")
				button, ok := tester.AccessByRole(ui.RoleButton, label)
				if !ok || !button.Disabled {
					t.Fatal("unavailable action is enabled", button)
				}
				if component != "model" {
					configuration := view.msg(runtimeNameKey(component)) + " · " + view.msg("runtimeResourceConfigure")
					button, ok := tester.AccessByRole(ui.RoleButton, configuration)
					if !ok || button.Disabled != (reason != "missing-source") {
						t.Fatal("resource configuration ignored its operation boundary", button)
					}
					selector := view.msg(runtimeNameKey(component)) + " " + view.msg("runtimeResourceSource")
					control, ok := tester.AccessByRole(ui.RolePopUpButton, selector)
					if !ok || control.Disabled != (reason != "missing-source") {
						t.Fatal("source selector ignored the component operation boundary", control)
					}
				}
				tester.ClickAt(button.Bounds.X+button.Bounds.W/2, button.Bounds.Y+button.Bounds.H/2)
				if len(service.calls) != 0 {
					t.Fatal("disabled action reached the runtime service")
				}
			})
		}
	}
}

func TestRuntimeInstallLogsExpandFilterAndCollapse(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			panel := runtimeInteractionPanel()
			panel.Logs = map[string]string{"python": "synthetic python log", "model": "synthetic model log"}
			view := &RuntimeView{Client: &client.RuntimeController{Panel: &panel}, Locale: locale, section: "runtimeDownloads"}
			tester := ui.NewTester(view.View, 1100, 1400)
			label := view.msg("runtimeInstallLog")
			if button, ok := tester.AccessByRole(ui.RoleButton, label); !ok || button.Expanded || tester.HasText("synthetic python log") {
				t.Fatal("install logs should be collapsed initially", button)
			}
			runtimeClickControl(t, tester, ui.RoleButton, label)
			if button, ok := tester.AccessByRole(ui.RoleButton, label); !ok || !button.Expanded {
				t.Fatal("install logs did not announce their expanded state", button)
			}
			combined := "synthetic model log\n[model]\nsynthetic python log\n[python]"
			if !tester.HasText(combined) {
				t.Fatal("expanded logs lost their combined newest-first projection")
			}
			for _, component := range []string{"python", "ffmpeg", "model"} {
				runtimeClickControl(t, tester, ui.RoleRadio, view.msg(runtimeNameKey(component)))
				expected := panel.Logs[component]
				if expected == "" {
					expected = view.msg("runtimeInstallLogEmpty")
				}
				if !tester.HasText(expected) {
					t.Fatal("log filter lost its component content", component)
				}
			}
			runtimeClickControl(t, tester, ui.RoleRadio, localized(locale, "全部", "All"))
			if !tester.HasText(combined) {
				t.Fatal("all log filter did not restore the combined projection")
			}
			captureParity(t, tester, "runtime-expanded-logs-"+locale)
			runtimeClickControl(t, tester, ui.RoleButton, label)
			if button, ok := tester.AccessByRole(ui.RoleButton, label); !ok || button.Expanded || tester.HasText(combined) {
				t.Fatal("install logs did not collapse", button)
			}
		})
	}
}

func TestRuntimeViewInstallAllFailureAndRecoveryUseTheSameGuardedAction(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			callbacks := make(chan func(), 8)
			service := &runtimeInteractionService{panel: runtimeInteractionPanel(), calls: make(chan runtimeInteractionCall, 8), nextError: errors.New("synthetic runtime failure")}
			controller := client.NewRuntime(service, func(callback func()) { callbacks <- callback }, func() {})
			defer controller.Close()
			controller.Panel = &service.panel
			view := &RuntimeView{Client: controller, Locale: locale}
			tester := ui.NewTesterWith(view.View, 1100, 1400, ui.TesterOptions{Dark: locale == "en-US"})
			runtimeClickControl(t, tester, ui.RoleButton, view.msg("runtimeInstallAction"))
			flushView(t, callbacks, tester)
			if got := <-service.calls; got.operation != "install-all" {
				t.Fatal("bulk install did not reach the bulk use case", got)
			}
			message := localized(locale, "运行环境操作失败，请重试。", "Runtime operation failed. Please retry.")
			if _, ok := tester.AccessByRole(ui.RoleAlert, message); !ok || tester.HasText(service.nextError.Error()) {
				t.Fatal("runtime error was inaccessible or exposed an internal failure")
			}
			service.nextError = nil
			if err := tester.Click(presentation.Message(locale, "common.retry")); err != nil {
				t.Fatal(err)
			}
			flushView(t, callbacks, tester)
			if controller.Error != nil || len(service.calls) != 0 {
				t.Fatal("read retry repeated the failed install or failed to clear its error")
			}
			runtimeClickControl(t, tester, ui.RoleButton, view.msg("runtimeInstallAction"))
			flushView(t, callbacks, tester)
			if got := <-service.calls; got != (runtimeInteractionCall{operation: "install-all"}) || controller.Error != nil {
				t.Fatal("bulk install could not recover after failure", got)
			}
			captureParity(t, tester, "runtime-actions-"+locale)
		})
	}
}

func TestRuntimeViewSourceChoiceAndBulkInstallDisabledStates(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			callbacks := make(chan func(), 8)
			service := &runtimeInteractionService{panel: runtimeInteractionPanel(), calls: make(chan runtimeInteractionCall, 8)}
			controller := client.NewRuntime(service, func(callback func()) { callbacks <- callback }, func() {})
			defer controller.Close()
			controller.Panel = &service.panel
			view := &RuntimeView{Client: controller, Locale: locale, section: "runtimeDownloads"}
			tester := ui.NewTester(view.View, 1100, 1400)
			runtimeClickControl(t, tester, ui.RolePopUpButton, view.msg("runtimeDownloadSource"))
			tester.Key(0, ui.KeyEnd)
			tester.Key(0, ui.KeyEnter)
			flushView(t, callbacks, tester)
			if got := <-service.calls; got != (runtimeInteractionCall{operation: "download-source", source: "nju"}) {
				t.Fatal("localized source selection used a label instead of its source ID", got)
			}
			view.section = "localRuntime"
			for _, reason := range []string{"busy", "active", "unsupported", "missing-source"} {
				panel := runtimeInteractionPanel()
				controller.Panel = &panel
				controller.Busy = reason == "busy"
				switch reason {
				case "active":
					panel.Overview.Models.Operation.Kind = "waiting_for_python"
					panel.Overview.Status = "installing"
				case "unsupported":
					panel.Overview.Status = "unsupported"
				case "missing-source":
					panel.DownloadSource = ""
				}
				tester.Frame()
				button, ok := tester.AccessByRole(ui.RoleButton, view.msg("runtimeInstallAction"))
				if !ok || !button.Disabled {
					t.Fatal("bulk installation ignored a precondition", reason, button)
				}
				tester.ClickAt(button.Bounds.X+button.Bounds.W/2, button.Bounds.Y+button.Bounds.H/2)
				if len(service.calls) != 0 {
					t.Fatal("disabled bulk action reached the runtime service", reason)
				}
				status, ok := tester.AccessByRole(ui.RoleStatus, view.msg(runtimeOverviewKey(panel.Overview.Status)))
				if !ok || status.Busy != (reason == "busy" || reason == "active") {
					t.Fatal("runtime status did not expose an active operation", reason, status)
				}
			}
		})
	}
}
