package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

type RuntimeView struct {
	Client       *client.RuntimeController
	Locale       string
	Back         func()
	search       string
	logComponent string
}

func (v *RuntimeView) msg(key string) string { return presentation.Message(v.Locale, "settings."+key) }
func (v *RuntimeView) Sidebar(c *ui.Context) {
	settingsSidebar(c, v.Locale, "localRuntime", &v.search, v.Back, func(section string) bool { return section == "localRuntime" }, func(string) {})
}
func (v *RuntimeView) View(c *ui.Context) {
	settingsPage(c, v.Locale, "localRuntime", func() {
		if v.Client.Error != nil {
			message := localized(v.Locale, "运行环境操作失败，请重试。", "Runtime operation failed. Please retry.")
			if v.Client.ReadError != nil {
				message = localized(v.Locale, "运行环境状态读取失败，请重试。", "Could not read runtime status. Please retry.")
			}
			ui.Text(c, message).TextColor(c.Theme().Danger).Role(ui.RoleAlert).Selectable()
			if textButton(c, presentation.Message(v.Locale, "common.retry")).Disabled(v.Client.Busy || v.Client.Refreshing()).Clicked() {
				v.Client.Refresh()
			}
		}
		panel := v.Client.Panel
		if !v.Client.PanelAvailable() {
			if v.Client.Error != nil {
				return
			}
			ui.Text(c, localized(v.Locale, "正在读取…", "Loading…")).Role(ui.RoleStatus).Busy(true)
			return
		}
		status := panel.Overview
		unsupported := status.Status == "unsupported"
		settingsPanel(c).Children(func() {
			v.row(c, "managedRuntime", "managedRuntimeHint", func() {
				ui.Text(c, v.msg(runtimeOverviewKey(status.Status))).FontSize(12).Padding(4, 10).Radius(14).Background(palette(c).Selected).Role(ui.RoleStatus).Busy(v.Client.Busy || v.Client.Refreshing() || status.Python.Operation.Active() || status.FFmpeg.Operation.Active() || status.Models.Operation.Active())
			})
			v.row(c, "runtimeDownloadSource", runtimeDescriptionKey(status.Status), func() {
				labels := []string{v.msg("runtimeDownloadSourcePlaceholder")}
				ids := []string{""}
				selected := labels[0]
				for _, source := range panel.Sources {
					label := source.Name
					if v.Locale == "en-US" {
						label = map[string]string{"official": "Official", "nju": "Nanjing University", "tsinghua": "Tsinghua University", "aliyun": "Alibaba Cloud"}[source.ID]
						if label == "" {
							label = source.ID
						}
					}
					labels = append(labels, label)
					ids = append(ids, source.ID)
					if source.ID == panel.DownloadSource {
						selected = label
					}
				}
				if ui.Select(c, &selected, labels).Width(190).MinHeight(40).Label(v.msg("runtimeDownloadSource")).Disabled(v.Client.Busy || unsupported).Changed() {
					for j, label := range labels {
						if selected == label && ids[j] != "" {
							v.Client.SelectDownloadSource(ids[j])
							break
						}
					}
				}
			})
			for _, component := range []domain.RuntimeComponent{status.Python, status.FFmpeg, status.Models} {
				v.component(c, component, panel.DownloadSource, unsupported)
			}
			v.row(c, "runtimeInstallAction", "", func() {
				label := "runtimeInstallAction"
				if status.ShellReady {
					label = "runtimeReinstallAction"
				}
				busy := v.Client.Busy || v.Client.Refreshing() || status.Python.Operation.Active() || status.FFmpeg.Operation.Active() || status.Models.Operation.Active()
				if textButton(c, v.msg(label)).Disabled(busy || unsupported || panel.DownloadSource == "").Clicked() {
					v.Client.InstallAll()
				}
			})
			ui.Column(c).FillWidth().Padding(18, 24).Gap(10).Children(func() {
				ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
					ui.Text(c, v.msg("runtimeInstallLog")).Grow(1).Bold()
					if segmentButton(c, localized(v.Locale, "全部", "All"), v.logComponent == "").Clicked() {
						v.logComponent = ""
					}
					for _, component := range []string{"python", "ffmpeg", "model"} {
						if segmentButton(c, v.msg(runtimeNameKey(component)), v.logComponent == component).Clicked() {
							v.logComponent = component
						}
					}
				})
				text := strings.TrimSpace(panel.Logs[v.logComponent])
				if v.logComponent == "" {
					text = strings.TrimSpace(panel.InstallLog())
				}
				if text == "" {
					text = v.msg("runtimeInstallLogEmpty")
				} else {
					lines := strings.Split(text, "\n")
					if len(lines) > 200 {
						lines = lines[len(lines)-200:]
					}
					for a, b := 0, len(lines)-1; a < b; a, b = a+1, b-1 {
						lines[a], lines[b] = lines[b], lines[a]
					}
					text = strings.Join(lines, "\n")
				}
				ui.Scroll(c).FillWidth().Height(220).Background(palette(c).Subpanel).Radius(8).Padding(12).Label(v.msg("runtimeInstallLog")).Children(func() { ui.Text(c, text).FontSize(11).FillWidth() })
			})
		})
	})
}
func (v *RuntimeView) row(c *ui.Context, label, hint string, control func()) {
	if hint != "" {
		hint = v.msg(hint)
	}
	settingsRow(c, v.msg(label), hint, control)
}
func (v *RuntimeView) component(c *ui.Context, s domain.RuntimeComponent, source string, unsupported bool) {
	v.row(c, runtimeNameKey(s.Component), "", func() {
		ui.Row(c).FillWidth().Gap(12).Wrap().AlignItems(ui.Center).Children(func() {
			busy := v.Client.Busy || v.Client.Refreshing() || s.Operation.Active()
			if s.Component != "model" {
				labels := []string{v.msg("runtimeResourceManaged"), v.msg("runtimeResourceSystem")}
				selected := labels[0]
				if s.SourceName() == "system" {
					selected = labels[1]
				}
				if ui.Select(c, &selected, labels).Width(135).MinHeight(40).Label(v.msg(runtimeNameKey(s.Component)) + " " + v.msg("runtimeResourceSource")).Disabled(busy || unsupported).Changed() {
					next := "managed"
					if selected == labels[1] {
						next = "system"
					}
					v.Client.SwitchSource(s.Component, next)
				}
			}
			ui.Column(c).Grow(1).MinWidth(140).Gap(6).Children(func() {
				percent, indeterminate := runtimeProgress(s)
				label := v.msg(runtimeComponentKey(s, source))
				runtimeProgressBar(c, percent, label, indeterminate)
				ui.Text(c, label).FontSize(11).TextColor(c.Theme().TextMuted)
			})
			label := "runtimeResourceDownload"
			if s.Ready() {
				label = "runtimeResourceRedownload"
			}
			if s.SourceName() == "system" {
				label = "runtimeResourceDetect"
				if s.Ready() {
					label = "runtimeResourceDetectAgain"
				}
			}
			if textButton(c, v.msg(label)).Label(v.msg(runtimeNameKey(s.Component)) + " · " + v.msg(label)).Disabled(busy || unsupported || (s.SourceName() == "managed" && source == "")).Clicked() {
				if s.SourceName() == "system" {
					v.Client.Detect(s.Component)
				} else {
					v.Client.Install(s.Component)
				}
			}
		})
	})
}

func runtimeProgressBar(c *ui.Context, value float32, label string, indeterminate bool) {
	if !indeterminate {
		progressBar(c, value, label)
		return
	}
	p := palette(c)
	value = max(0, min(100, value))
	ui.Row(c).FillWidth().Height(5).Radius(3).Background(p.Separator).Label(label).Role(ui.RoleProgress).Range(0, 100, float64(value)).Busy(true).Children(func() {
		ui.Row(c).WidthPercent(value).Height(5).Radius(3).Background(c.Theme().Accent)
	})
}
func runtimeNameKey(component string) string {
	switch component {
	case "python":
		return "runtimeResourcePython"
	case "ffmpeg":
		return "runtimeResourceFfmpeg"
	default:
		return "runtimeResourceFunasr"
	}
}
func runtimeOverviewKey(status string) string {
	switch status {
	case "ready":
		return "runtimeStatusReady"
	case "system_ready":
		return "runtimeStatusSystemReady"
	case "installing":
		return "runtimeStatusInstalling"
	case "failed":
		return "runtimeStatusFailed"
	case "unsupported":
		return "runtimeStatusUnsupported"
	default:
		return "runtimeStatusMissing"
	}
}
func runtimeDescriptionKey(status string) string {
	switch status {
	case "ready":
		return "runtimeDescriptionReady"
	case "system_ready":
		return "runtimeDescriptionSystemReady"
	case "installing":
		return "runtimeDescriptionInstalling"
	case "failed":
		return "runtimeDescriptionFailed"
	default:
		return "runtimeDescriptionMissing"
	}
}
func runtimeComponentKey(s domain.RuntimeComponent, downloadSource string) string {
	switch s.Operation.Kind {
	case "waiting_for_python":
		return "runtimeResourceWaitingPython"
	case "detecting":
		return "runtimeResourceDetecting"
	case "validating":
		return "runtimeResourceValidating"
	case "downloading", "installing":
		return "runtimeResourceDownloading"
	case "failed":
		if s.SourceName() == "system" {
			return "runtimeResourceNotDetected"
		}
		return "runtimeResourceFailed"
	}
	if s.Availability == "unsupported" {
		return "runtimeStatusUnsupported"
	}
	if s.Ready() {
		if s.SourceName() == "system" {
			return "runtimeResourceSystemSelected"
		}
		return "runtimeResourceReady"
	}
	if s.SourceName() == "managed" && downloadSource == "" {
		return "runtimeDownloadSourceRequired"
	}
	if s.SourceName() == "system" {
		return "runtimeResourceNotDetected"
	}
	return "runtimeResourcePending"
}

func runtimeProgress(s domain.RuntimeComponent) (float32, bool) {
	if s.Operation.Active() {
		if s.Operation.Progress != nil {
			return float32(*s.Operation.Progress), false
		}
		return 36, true
	}
	if s.Ready() {
		return 100, false
	}
	if s.Operation.Progress != nil {
		return float32(*s.Operation.Progress), false
	}
	return 0, false
}
