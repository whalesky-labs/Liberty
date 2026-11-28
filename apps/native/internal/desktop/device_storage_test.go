package desktop

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"runtime"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func TestDeviceStorageLoadingSkeletonKeepsLayoutAndAccessibleRecovery(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, width := range []int{1440, 1100, 760} {
			t.Run(fmt.Sprintf("%s-%d", locale, width), func(t *testing.T) {
				view := settingsParityView(locale, "deviceStorage")
				report := view.Storage.Report
				view.Storage.Report, view.Storage.Loaded, view.Storage.Busy = nil, false, true
				shell := Shell{Locale: locale, Route: "/settings", Preferences: domain.DefaultUIPreferences(), Sidebar: view.Sidebar, Content: view.View}
				tester := ui.NewTesterWith(shell.View, width, 1800, ui.TesterOptions{Dark: locale == "zh-CN"})
				loading, ok := tester.AccessByRole(ui.RoleStatus, view.msg("deviceStorageLoading"))
				if !ok || !loading.Busy || loading.Offscreen || loading.Bounds.W <= 0 || loading.Bounds.H < 300 {
					t.Fatal("skeleton lacks an accessible loading state or content-sized bounds", loading)
				}
				_, visibleLoadingText := tester.AccessByRole(ui.RoleText, view.msg("deviceStorageLoading"))
				if visibleLoadingText || tester.HasText("0 B") {
					t.Fatal("loading should show skeleton shapes without placeholder text or invented usage")
				}
				if _, ok := tester.AccessByRole(ui.RoleButton, view.msg("deviceStorageBrowse")); ok {
					t.Fatal("loading skeleton exposes an unavailable folder action")
				}
				groups := map[string]ui.Rect{}
				for _, key := range []string{"deviceStorageOverviewGroup", "deviceStorageDetailsGroup", "deviceStorageFoldersGroup"} {
					heading, ok := tester.AccessByRole(ui.RoleHeading, view.msg(key))
					if !ok || heading.Offscreen || heading.Bounds.X < 0 || heading.Bounds.X+heading.Bounds.W > float32(width) {
						t.Fatal("loading content is missing or clipped", key, heading)
					}
					groups[key] = heading.Bounds
				}
				captureParity(t, tester, fmt.Sprintf("device-storage-loading-%s-%d", locale, width))

				view.Storage.Report, view.Storage.Loaded, view.Storage.Busy = report, true, false
				tester.Frame()
				if _, ok := tester.AccessByRole(ui.RoleStatus, view.msg("deviceStorageLoading")); ok || !tester.HasText("10.0 KB") {
					t.Fatal("report did not replace the loading skeleton")
				}
				for key, before := range groups {
					heading, ok := tester.AccessByRole(ui.RoleHeading, view.msg(key))
					if !ok || math.Abs(float64(heading.Bounds.Y-before.Y)) > 2 {
						t.Fatal("content jumps when the report replaces the skeleton", key, before, heading.Bounds)
					}
				}
				view.Storage.Busy = true
				tester.Frame()
				if _, ok := tester.AccessByRole(ui.RoleStatus, view.msg("deviceStorageLoading")); ok || !tester.HasText("10.0 KB") {
					t.Fatal("refresh replaced an existing report with a loading skeleton")
				}

				view.Storage.Report, view.Storage.Busy, view.Storage.Error = nil, false, errors.New("synthetic storage failure")
				tester.Frame()
				if _, ok := tester.AccessByRole(ui.RoleStatus, view.msg("deviceStorageLoading")); ok {
					t.Fatal("failed initial load still presents a loading skeleton")
				}
				if _, ok := tester.AccessByRole(ui.RoleAlert, view.msg("deviceStorageLoadFailed")); !ok {
					t.Fatal("failed initial load lacks an accessible error")
				}
				retry, ok := tester.AccessByRole(ui.RoleButton, presentation.Message(locale, "common.retry"))
				if !ok || retry.Disabled || retry.Offscreen {
					t.Fatal("failed initial load lacks an available retry action", retry)
				}
			})
		}
	}
}

func TestDeviceStorageSkeletonPulseRespectsReducedMotion(t *testing.T) {
	for _, reduceMotion := range []bool{false, true} {
		t.Run(fmt.Sprintf("reduce-motion-%t", reduceMotion), func(t *testing.T) {
			view := settingsParityView("en-US", "deviceStorage")
			view.Storage.Report, view.Storage.Busy = nil, true
			tester := ui.NewTesterWith(view.View, 1100, 1200, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: reduceMotion}})
			before := append([]byte(nil), tester.Image().Pix...)
			tester.Advance(800 * time.Millisecond)
			changed := !bytes.Equal(before, tester.Image().Pix)
			if changed != !reduceMotion {
				t.Fatalf("skeleton pulse changed=%t with reduce motion=%t", changed, reduceMotion)
			}
		})
	}
}

func TestDeviceStorageNavigationAndReportAreAccessibleInBothLocales(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, width := range []int{1440, 1100, 760} {
			t.Run(fmt.Sprintf("%s-%d", locale, width), func(t *testing.T) {
				view := settingsParityView(locale, "deviceStorage")
				tester := ui.NewTester(view.View, width, 1800)
				for _, key := range []string{"deviceStorageTotal", "deviceStorageDatabase", "deviceStorageJobs", "deviceStorageRuntime", "deviceStorageOther", "deviceStorageProfileDirectory"} {
					if !tester.HasText(view.msg(key)) {
						t.Fatal("device storage page is missing a category", locale, key)
					}
				}
				if !tester.HasText("10.0 KB") {
					t.Fatal("device storage total is missing")
				}
				bar, ok := tester.AccessByRole(ui.RoleGroup, view.msg("deviceStorageBreakdown"))
				if !ok || bar.Offscreen || bar.Bounds.W <= 0 {
					t.Fatal("storage breakdown is not exposed accessibly", bar)
				}
				browse, ok := tester.AccessByRole(ui.RoleButton, view.msg("deviceStorageBrowse"))
				if !ok || browse.Disabled || browse.Offscreen || browse.Bounds.X+browse.Bounds.W > float32(width) {
					t.Fatal("data folder action is unavailable or clipped", browse)
				}
				tester.FailOpenURL(errors.New("synthetic file manager failure"))
				if err := tester.Click(view.msg("deviceStorageBrowse")); err != nil {
					t.Fatal(err)
				}
				if _, ok := tester.AccessByRole(ui.RoleAlert, view.msg("deviceStorageOpenFailed")); !ok {
					t.Fatal("failed folder launch lacks accessible recovery guidance")
				}
				tester.FailOpenURL(nil)
				if err := tester.Click(view.msg("deviceStorageBrowse")); err != nil {
					t.Fatal(err)
				}
				opened := tester.OpenedURLs()
				if len(opened) != 2 || opened[1] != "file:///synthetic/liberty-profile" {
					t.Fatal("folder action did not open the profile directory", opened)
				}
			})
		}
	}
}

func TestDeviceStorageNavigationIsUnderSystemSettings(t *testing.T) {
	for _, group := range settingsNavigation {
		for _, item := range group.items {
			if item.key == "deviceStorage" && group.key != "systemGroup" {
				t.Fatal("device storage must be under the system settings group", group.key)
			}
		}
	}
}

func TestLocalDirectoryURLHandlesNativeAbsolutePaths(t *testing.T) {
	if got, ok := localDirectoryURL(`/Users/Someone/Liberty data #1`, "darwin"); !ok || got != "file:///Users/Someone/Liberty%20data%20%231" {
		t.Fatal("macOS folder URL was not escaped safely", got, ok)
	}
	if got, ok := localDirectoryURL(`C:\Users\Someone\Liberty data`, "windows"); !ok || got != "file:///C:/Users/Someone/Liberty%20data" {
		t.Fatal("Windows folder URL was not formed safely", got, ok)
	}
	if _, ok := localDirectoryURL("relative/path", runtime.GOOS); ok {
		t.Fatal("relative folder path must not be opened")
	}
}

func TestDeviceStorageControllerReportFixtureIsNotMutatedByView(t *testing.T) {
	report := application.DeviceStorageReport{Directory: "/synthetic/liberty-profile", Total: 10_000, Database: 1_000, Jobs: 2_000, Runtime: 3_000, Other: 4_000}
	view := &SettingsView{Locale: "en-US", section: "deviceStorage", Storage: &client.DeviceStorageController{Loaded: true, Report: &report}}
	tester := ui.NewTester(view.View, 1100, 1500)
	if !tester.HasText("1.0 KB") || !tester.HasText("2.0 KB") || !tester.HasText("3.0 KB") || !tester.HasText("4.0 KB") {
		t.Fatal("storage category values are missing")
	}
	if report.Total != 10_000 {
		t.Fatal("view changed its storage report fixture")
	}
}
