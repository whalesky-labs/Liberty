package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

type RuntimeView struct {
	Client            *client.RuntimeController
	Live              *client.LiveController
	Locale            string
	Back              func()
	NavigateSection   func(string)
	section           string
	search            string
	logComponent      string
	expandedComponent string
	logsExpanded      bool
}

func (v *RuntimeView) msg(key string) string { return presentation.Message(v.Locale, "settings."+key) }
func (v *RuntimeView) Sidebar(c *ui.Context) {
	settingsSidebar(c, v.Locale, v.currentSection(), &v.search, v.Back, runtimeSettingsSection, func(section string) { v.section = section })
}
func (v *RuntimeView) currentSection() string {
	if runtimeSettingsSection(v.section) {
		return v.section
	}
	return "localRuntime"
}

func (v *RuntimeView) View(c *ui.Context) {
	section := v.currentSection()
	if section == "speechModels" && v.Live != nil {
		v.Live.EnsureModelStatus()
	}
	settingsPageWithHeader(c, v.Locale, section, func() {
		if v.Client.PanelAvailable() {
			status := v.Client.Panel.Overview
			busy := v.Client.Busy || v.Client.Refreshing() || status.Python.Operation.Active() || status.FFmpeg.Operation.Active() || status.Models.Operation.Active() || v.Live != nil && (v.Live.Busy || v.Live.Preparing)
			ui.Text(c, v.msg(runtimeOverviewKey(status.Status))).FontSize(12).Padding(4, 10).Radius(14).Background(palette(c).Selected).Role(ui.RoleStatus).Busy(busy).SingleLine()
		}
	}, func() {
		if v.Client.Error != nil {
			message := localized(v.Locale, "运行环境操作失败，请重试。", "Runtime operation failed. Please retry.")
			if v.Client.ReadError != nil {
				message = localized(v.Locale, "运行环境状态读取失败，请重试。", "Could not read runtime status. Please retry.")
			}
			ui.Text(c, message).TextColor(c.Theme().Danger).Role(ui.RoleAlert).Selectable()
			if ui.Button(c, presentation.Message(v.Locale, "common.retry")).Disabled(v.Client.Busy || v.Client.Refreshing()).Clicked() {
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
		busy := v.Client.Busy || v.Client.Refreshing() || status.Python.Operation.Active() || status.FFmpeg.Operation.Active() || status.Models.Operation.Active() || v.Live != nil && (v.Live.Busy || v.Live.Preparing)
		switch section {
		case "runtimeDownloads":
			settingsGroup(c, v.msg("downloadsGroup"), func() {
				v.row(c, "runtimeDownloadSource", "runtimeDownloadSourceHint", func() {
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
					if selectControl(c, &selected, labels).Width(240).Label(v.msg("runtimeDownloadSource")).Disabled(v.Client.Busy || unsupported).Changed() {
						for j, label := range labels {
							if selected == label && ids[j] != "" {
								v.Client.SelectDownloadSource(ids[j])
								break
							}
						}
					}
				})
			})
			settingsGroup(c, v.msg("installHistoryGroup"), func() { v.installLogs(c, *panel) })
		case "speechModels":
			settingsGroup(c, v.msg("runtimeModelsGroup"), func() { v.component(c, status.Models, panel.DownloadSource, unsupported) })
			if v.Live != nil {
				v.liveModels(c, status.ShellReady, v.Client.Busy || v.Client.Refreshing())
			}
		default:
			settingsGroup(c, v.msg("runtimeSetupGroup"), func() {
				settingsRow(c, v.msg("managedRuntime"), v.msg("runtimeSetupHint"), func() {
					label := "runtimeInstallAction"
					if status.ShellReady {
						label = "runtimeReinstallAction"
					}
					if ui.Button(c, v.msg(label)).Disabled(busy || unsupported || panel.DownloadSource == "").Clicked() {
						v.Client.InstallAll()
					}
				})
			})
			settingsGroup(c, v.msg("runtimeEnvironmentGroup"), func() {
				v.component(c, status.Python, panel.DownloadSource, unsupported)
				v.component(c, status.FFmpeg, panel.DownloadSource, unsupported)
			})
		}
		if section != "runtimeDownloads" && panel.DownloadSource == "" {
			ui.Text(c, v.msg("runtimeDownloadSourceMissingHint")).FillWidth().Padding(0, 14).FontSize(12).TextColor(c.Theme().Warning).Role(ui.RoleStatus)
		}
		settingsGroup(c, v.msg("relatedSettingsGroup"), func() {
			for _, destination := range []string{"localRuntime", "speechModels", "runtimeDownloads"} {
				if destination != section {
					settingsSectionLink(c, v.Locale, destination, true, func() {
						if v.NavigateSection != nil {
							v.NavigateSection(destination)
						} else {
							v.section = destination
						}
					})
				}
			}
		})
	})
}

func (v *RuntimeView) liveModels(c *ui.Context, runtimeReady, runtimeBusy bool) {
	live := v.Live
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		settingsGroupHeading(c, v.msg("liveModelsGroup"), nil)
		ui.Text(c, v.msg("liveModelsHint")).Padding(0, 14).FontSize(12).TextColor(c.Theme().TextMuted)
		if !runtimeReady {
			ui.Text(c, v.msg("liveModelsNeedsRuntime")).FillWidth().Padding(0, 14).FontSize(12).TextColor(c.Theme().Warning).Role(ui.RoleStatus)
		}
		settingsPanel(c, func() {
			settingsRow(c, v.msg("liveASRModel"), "", func() {
				status, busy := v.msg("runtimeResourcePending"), false
				if live.Preparing {
					status, busy = v.msg("runtimeResourceDownloading"), true
				} else if live.ModelStatusBusy || live.Busy {
					status, busy = v.msg("runtimeResourceValidating"), true
				} else if live.Options.ModelReady {
					status = v.msg("runtimeResourceReady")
				}
				ui.Text(c, status).FontSize(12).TextColor(runtimeLiveModelStatusColor(c, busy, live.Options.ModelReady)).Role(ui.RoleStatus).Busy(busy)
			})
			settingsRow(c, v.msg("liveTranslationModels"), "", func() {
				status, busy := v.msg("runtimeResourcePending"), false
				if live.Preparing {
					status, busy = v.msg("runtimeResourceDownloading"), true
				} else if live.ModelStatusBusy || live.Busy {
					status, busy = v.msg("runtimeResourceValidating"), true
				} else if live.Options.TranslationReady {
					status = v.msg("runtimeResourceReady")
				}
				ui.Text(c, status).FontSize(12).TextColor(runtimeLiveModelStatusColor(c, busy, live.Options.TranslationReady)).Role(ui.RoleStatus).Busy(busy)
			})
			if live.Error != nil && !live.Busy && !live.Preparing {
				ui.Text(c, v.msg("liveModelsFailed")).FillWidth().FontSize(12).TextColor(c.Theme().Danger).Role(ui.RoleAlert)
			}
			ui.Row(c).FillWidth().MinHeight(52).AlignItems(ui.Center).Justify(ui.End).Children(func() {
				if live.Preparing {
					if ui.Button(c, presentation.Message(v.Locale, "common.cancel")).MinHeight(36).Clicked() {
						live.CancelPrepare()
					}
					return
				}
				label := "liveModelsDownload"
				if live.Options.ModelReady && live.Options.TranslationReady {
					label = "liveModelsRedownload"
				}
				if ui.Button(c, v.msg(label)).MinHeight(36).Disabled(!runtimeReady || live.Busy || live.ModelStatusBusy || runtimeBusy || domain.LiveActive(live.Snapshot.Session.State)).Clicked() {
					live.Prepare()
				}
			})
		})
	})
}

func runtimeLiveModelStatusColor(c *ui.Context, busy, ready bool) ui.Color {
	if busy {
		return c.Theme().Accent
	}
	if ready {
		return c.Theme().Success
	}
	return c.Theme().TextMuted
}
func (v *RuntimeView) row(c *ui.Context, label, hint string, control func()) {
	if hint != "" {
		hint = v.msg(hint)
	}
	settingsRow(c, v.msg(label), hint, control)
}
func (v *RuntimeView) component(c *ui.Context, s domain.RuntimeComponent, source string, unsupported bool) {
	busy := v.Client.Busy || v.Client.Refreshing() || s.Operation.Active()
	unsupported = unsupported || s.Availability == "unsupported"
	name := v.msg(runtimeNameKey(s.Component))
	status := v.msg(runtimeComponentKey(s, source))
	statusColor := c.Theme().TextMuted
	if s.Operation.Kind == "failed" {
		statusColor = c.Theme().Danger
	} else if s.Operation.Active() {
		statusColor = c.Theme().Accent
	} else if s.Ready() {
		statusColor = c.Theme().Success
	}
	ui.Column(c.Key(s.Component)).FillWidth().Padding(14, 0).Gap(12).Children(func() {
		ui.Row(c).FillWidth().MinHeight(44).Gap(16).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(5).Children(func() {
				ui.Text(c, name).FillWidth().FontSize(14)
				metadata := v.msg(runtimeNameKey(s.Component) + "Hint")
				if s.Component != "model" {
					sourceKey := "runtimeResourceManaged"
					if s.SourceName() == "system" {
						sourceKey = "runtimeResourceSystem"
					}
					metadata = v.msg(sourceKey) + " · " + metadata
				}
				ui.Text(c, metadata).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted)
			})
			ui.Text(c, status).MaxWidth(200).FontSize(12).TextColor(statusColor).SingleLine().Tooltip(status).Role(ui.RoleStatus).Busy(s.Operation.Active())
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if s.Component != "model" {
					label := name + " · " + v.msg("runtimeResourceConfigure")
					if runtimeIconButton(c, "accent", label, busy || unsupported).Expanded(v.expandedComponent == s.Component).Clicked() {
						if v.expandedComponent == s.Component {
							v.expandedComponent = ""
						} else {
							v.expandedComponent = s.Component
						}
					}
				} else {
					ui.Box(c).Size(36, 36)
				}
				label := "runtimeResourceDownload"
				icon := "download"
				if s.Ready() {
					label = "runtimeResourceRedownload"
				}
				if s.SourceName() == "system" {
					label = "runtimeResourceDetect"
					icon = "refresh"
					if s.Ready() {
						label = "runtimeResourceDetectAgain"
					}
				}
				if runtimeIconButton(c, icon, name+" · "+v.msg(label), busy || unsupported || (s.SourceName() == "managed" && source == "")).Clicked() {
					if s.SourceName() == "system" {
						v.Client.Detect(s.Component)
					} else {
						v.Client.Install(s.Component)
					}
				}
			})
		})
		if s.Operation.Active() {
			percent, indeterminate := runtimeProgress(s)
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Basis(0).Children(func() { runtimeProgressBar(c, percent, status, indeterminate) })
				if !indeterminate {
					ui.Text(c, fmt.Sprintf("%.0f%%", percent)).FontSize(11).TextColor(c.Theme().TextMuted)
				}
			})
		}
		if s.Component != "model" && v.expandedComponent == s.Component {
			ui.Row(c).FillWidth().Padding(12).Gap(16).AlignItems(ui.Center).Children(func() {
				ui.Text(c, v.msg("runtimeResourceSource")).Grow(1).FontSize(12)
				labels := []string{v.msg("runtimeResourceManaged"), v.msg("runtimeResourceSystem")}
				selected := labels[0]
				if s.SourceName() == "system" {
					selected = labels[1]
				}
				if selectControl(c, &selected, labels).Width(200).Label(name + " " + v.msg("runtimeResourceSource")).Disabled(busy || unsupported).Changed() {
					next := "managed"
					if selected == labels[1] {
						next = "system"
					}
					v.Client.SwitchSource(s.Component, next)
				}
			})
		}
	})
}

func runtimeIconButton(c *ui.Context, icon, label string, disabled bool) ui.Element {
	button := ui.Button(c, "").Size(36, 36).Radius(6).Justify(ui.Center).AlignItems(ui.Center).Label(label).Tooltip(label).Disabled(disabled)
	return button.Children(func() { ui.Icon(c, presentation.Icon(icon)).Size(16, 16).TextColor(c.Theme().TextMuted) })
}

func (v *RuntimeView) installLogs(c *ui.Context, panel domain.RuntimePanel) {
	ui.Column(c).FillWidth().Padding(10, 0).Gap(10).Children(func() {
		button := ui.Button(c, "").FillWidth().MinHeight(36).Gap(12).Radius(6).Label(v.msg("runtimeInstallLog")).Expanded(v.logsExpanded)
		button.Children(func() {
			ui.Text(c, v.msg("runtimeInstallLog")).Grow(1).FontSize(13)
			icon := "chevronDown"
			if v.logsExpanded {
				icon = "chevronUp"
			}
			ui.Icon(c, presentation.Icon(icon)).Size(14, 14).TextColor(c.Theme().TextMuted)
		})
		if button.Clicked() {
			v.logsExpanded = !v.logsExpanded
		}
		if !v.logsExpanded {
			return
		}
		ui.ScrollHorizontal(c).FillWidth().MinWidth(0).Children(func() {
			segmentedControl(c, &v.logComponent, []string{"", "python", "ffmpeg", "model"},
				localized(v.Locale, "全部", "All"), v.msg(runtimeNameKey("python")), v.msg(runtimeNameKey("ffmpeg")), v.msg(runtimeNameKey("model"))).
				Label(localized(v.Locale, "日志组件", "Log component"))
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
		contentScroll(c).FillWidth().Height(220).Padding(12).Label(v.msg("runtimeInstallLog")).Children(func() { ui.Text(c, text).FontSize(11).FillWidth().Selectable() })
	})
}

func runtimeProgressBar(c *ui.Context, value float32, label string, indeterminate bool) {
	if !indeterminate {
		progressBar(c, value, label)
		return
	}
	p := palette(c)
	value = max(0, min(100, value))
	ui.Row(c).FillWidth().Height(6).Radius(3).Background(p.Separator).Label(label).Role(ui.RoleProgress).Range(0, 100, float64(value)).Busy(true).Children(func() {
		ui.Row(c).WidthPercent(value).Height(6).Radius(3).Background(c.Theme().Accent)
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
