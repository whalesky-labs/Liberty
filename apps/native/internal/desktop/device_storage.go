package desktop

import (
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/desktop/presentation"
)

type deviceStorageCategory struct {
	key   string
	bytes uint64
	color ui.Color
}

func (v *SettingsView) deviceStorage(c *ui.Context) {
	storage := v.Storage
	if storage.Error != nil {
		ui.Text(c, v.msg("deviceStorageLoadFailed")).FillWidth().TextColor(c.Theme().Danger).Role(ui.RoleAlert)
		if ui.Button(c, presentation.Message(v.Locale, "common.retry")).Disabled(storage.Busy).Clicked() {
			storage.Refresh()
		}
		if storage.Report == nil {
			return
		}
	}
	report := storage.Report
	if report == nil {
		v.storageSkeleton(c)
		return
	}
	settingsGroup(c, v.msg("deviceStorageOverviewGroup"), func() {
		settingsRow(c, v.msg("deviceStorageTotal"), v.msg("deviceStorageTotalHint"), func() {
			ui.Text(c, formatStorageBytes(report.Total)).FontSize(15).Role(ui.RoleStatus).Busy(storage.Busy)
		})
		v.storageBar(c, *report)
		v.storageLegend(c, *report)
	})
	settingsGroup(c, v.msg("deviceStorageDetailsGroup"), func() {
		for _, category := range v.storageCategories(c, *report) {
			settingsRow(c, v.msg("deviceStorage"+strings.ToUpper(category.key[:1])+category.key[1:]), "", func() {
				ui.Text(c, formatStorageBytes(category.bytes)).FontSize(12)
			})
		}
	})
	settingsGroup(c, v.msg("deviceStorageFoldersGroup"), func() {
		settingsRow(c, v.msg("deviceStorageProfileDirectory"), v.msg("deviceStorageProfileDirectoryHint"), func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, report.Directory).FillWidth().MinWidth(0).SingleLine().Tooltip(report.Directory).Selectable().FontSize(12).TextColor(c.Theme().TextMuted)
				button := ui.Button(c, v.msg("deviceStorageBrowse")).Disabled(storage.Busy)
				if button.Clicked() {
					v.storageFolderOpenFailed = false
					address, ok := localDirectoryURL(report.Directory, runtime.GOOS)
					if !ok {
						v.storageFolderOpenFailed = true
					} else {
						c.OpenURLThen(address, func(err error) { v.storageFolderOpenFailed = err != nil })
					}
				}
			})
		})
		if v.storageFolderOpenFailed {
			ui.Text(c, v.msg("deviceStorageOpenFailed")).FillWidth().Padding(12, 0).TextColor(c.Theme().Danger).Role(ui.RoleAlert)
		}
	})
}

func (v *SettingsView) storageSkeleton(c *ui.Context) {
	loading := ui.Column(c.Key("device-storage-skeleton")).FillWidth().Gap(48).
		Role(ui.RoleStatus).Label(v.msg("deviceStorageLoading")).Busy(v.Storage.Busy)
	fill := c.Theme().Text.Alpha(0.1)
	if v.Storage.Busy && !c.Preferences().ReduceMotion {
		fill = c.Theme().Text.Alpha(0.08 + 0.04*loading.Loop("pulse", 1600*time.Millisecond, ui.Bounce(ui.EaseInOut)))
	}
	block := func(width, height float32) ui.Element {
		return ui.Box(c).Width(width).Height(height).Radius(height / 2).Background(fill).Role(ui.RoleNone)
	}
	loading.Children(func() {
		settingsGroup(c, v.msg("deviceStorageOverviewGroup"), func() {
			settingsRow(c, v.msg("deviceStorageTotal"), v.msg("deviceStorageTotalHint"), func() {
				block(84, 16)
			})
			block(0, 14).FillWidth()
			width, _ := c.Size()
			legend := ui.Row(c).FillWidth().Gap(14)
			if width < 900 {
				legend.Column().Gap(7)
			}
			legend.Children(func() {
				for _, key := range []string{"Database", "Jobs", "Runtime", "Other"} {
					label := v.msg("deviceStorage" + key)
					if width < 900 {
						ui.Row(c.Key(key)).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
							block(7, 7).Shrink(0)
							ui.Text(c, label).FontSize(12).Grow(1)
							block(52, 11)
						})
						continue
					}
					ui.Column(c.Key(key)).Grow(1).Basis(0).MinWidth(0).Children(func() {
						ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
							block(7, 7).Shrink(0)
							ui.Text(c, label).SingleLine().FontSize(12)
						})
						ui.Box(c).Padding(0, 14).Height(14).Children(func() { block(52, 11) })
					})
				}
			})
		})
		settingsGroup(c, v.msg("deviceStorageDetailsGroup"), func() {
			for _, key := range []string{"Database", "Jobs", "Runtime", "Other"} {
				settingsRow(c, v.msg("deviceStorage"+key), "", func() { block(68, 12) })
			}
		})
		settingsGroup(c, v.msg("deviceStorageFoldersGroup"), func() {
			settingsRow(c, v.msg("deviceStorageProfileDirectory"), v.msg("deviceStorageProfileDirectoryHint"), func() {
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					block(0, 12).FillWidth().MinWidth(0)
					block(72, 30).Shrink(0).Radius(7)
				})
			})
		})
	})
}

func (v *SettingsView) storageCategories(c *ui.Context, report application.DeviceStorageReport) []deviceStorageCategory {
	return []deviceStorageCategory{
		{key: "Database", bytes: report.Database, color: c.Theme().Accent},
		{key: "Jobs", bytes: report.Jobs, color: c.Theme().Success},
		{key: "Runtime", bytes: report.Runtime, color: c.Theme().Warning},
		{key: "Other", bytes: report.Other, color: c.Theme().TextMuted},
	}
}

func (v *SettingsView) storageBar(c *ui.Context, report application.DeviceStorageReport) {
	categories := v.storageCategories(c, report)
	ui.Row(c).FillWidth().Height(14).Radius(7).Clip().Background(palette(c).Separator).Role(ui.RoleGroup).Label(v.msg("deviceStorageBreakdown")).Children(func() {
		if report.Total == 0 {
			return
		}
		for _, category := range categories {
			if category.bytes == 0 {
				continue
			}
			percentage := float32(float64(category.bytes) / float64(report.Total) * 100)
			ui.Box(c.Key("storage-bar-" + category.key)).WidthPercent(percentage).FillHeight().Background(category.color)
		}
	})
}

func (v *SettingsView) storageLegend(c *ui.Context, report application.DeviceStorageReport) {
	categories := v.storageCategories(c, report)
	width, _ := c.Size()
	if width < 900 {
		ui.Column(c).FillWidth().Gap(7).Children(func() {
			for _, category := range categories {
				v.storageLegendRow(c, category)
			}
		})
		return
	}
	ui.Row(c).FillWidth().Gap(14).Children(func() {
		for _, category := range categories {
			item := category
			ui.Column(c.Key("storage-legend-" + item.key)).Grow(1).Basis(0).MinWidth(0).Role(ui.RoleGroup).
				Label(v.msg("deviceStorage"+strings.ToUpper(item.key[:1])+item.key[1:]) + " · " + formatStorageBytes(item.bytes)).Children(func() {
				ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(7, 7).Shrink(0).Radius(4).Background(item.color)
					ui.Text(c, v.msg("deviceStorage"+strings.ToUpper(item.key[:1])+item.key[1:])).SingleLine().FontSize(12)
				})
				ui.Text(c, formatStorageBytes(item.bytes)).Padding(0, 14).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine()
			})
		}
	})
}

func (v *SettingsView) storageLegendRow(c *ui.Context, category deviceStorageCategory) {
	label := v.msg("deviceStorage" + strings.ToUpper(category.key[:1]) + category.key[1:])
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Role(ui.RoleGroup).
		Label(label + " · " + formatStorageBytes(category.bytes)).Children(func() {
		ui.Box(c).Size(7, 7).Shrink(0).Radius(4).Background(category.color)
		ui.Text(c, label).FontSize(12).Grow(1)
		ui.Text(c, formatStorageBytes(category.bytes)).FontSize(11).TextColor(c.Theme().TextMuted)
	})
}

func formatStorageBytes(bytes uint64) string {
	if bytes < 1000 {
		return fmt.Sprintf("%d B", bytes)
	}
	value := float64(bytes)
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	for _, unit := range units {
		value /= 1000
		if value < 1000 || unit == "PB" {
			return fmt.Sprintf("%.1f %s", value, unit)
		}
	}
	return fmt.Sprintf("%.1f PB", value)
}

func localDirectoryURL(directory, platform string) (string, bool) {
	if platform == "windows" {
		path := strings.ReplaceAll(directory, `\`, "/")
		if len(path) < 3 || path[1] != ':' || path[2] != '/' {
			return "", false
		}
		path = "/" + path
		return (&url.URL{Scheme: "file", Path: path}).String(), true
	}
	if !filepath.IsAbs(directory) {
		return "", false
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(directory)}).String(), true
}
