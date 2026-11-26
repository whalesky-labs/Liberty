package desktop

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

// Shell provides a module rail, contextual sidebar, title bar and status bar. Routes are enabled
// only once the corresponding application capability is supplied by the shell.
// A missing capability must not open an inert, apparently working page.
type Shell struct {
	Preferences    domain.UIPreferences
	Locale         string
	Route          string
	Source         string
	Version        string
	ActiveJobs     int
	StatusMessage  string
	StatusTone     string
	ModeReadFailed bool
	ModeReadBusy   bool
	Metrics        *domain.ProcessMetrics
	PetEnabled     bool
	SourceReady    bool
	TogglePet      func()
	RetryMode      func()
	Navigate       func(string)
	Available      func(string) bool
	Content        func(*ui.Context)
	Sidebar        func(*ui.Context)
}

const (
	shellContentPaddingX = 12
	shellContentMaxWidth = 1180
)

func shellMenuCollapsed(c *ui.Context) *bool {
	// The workspace rebuilds Shell every frame; keep this window-local UI state
	// on the stable root so navigation and preference updates do not reset it.
	return ui.Local(c.Root(), "shell-menu-collapsed", func() bool { return false })
}

func shellNavigationWidth(c *ui.Context) float32 {
	if *shellMenuCollapsed(c) {
		return presentation.ActivityBarWidth
	}
	return presentation.SidebarWidth
}

func shellContentWidth(c *ui.Context) float32 {
	width, _ := c.Size()
	return min(shellContentMaxWidth, max(0, width-shellNavigationWidth(c)-2*shellContentPaddingX))
}

func (s *Shell) msg(key string) string { return presentation.Message(s.Locale, key) }
func (s *Shell) can(path string) bool {
	return s.Navigate != nil && (s.Available == nil || s.Available(path))
}

func titlebarLayout(c *ui.Context) (float32, float32, float32) {
	bar := c.TitleBar()
	height := float32(presentation.TitlebarHeight)
	if bar.Height > height {
		height = bar.Height
	}
	left, right := float32(16), float32(16)
	if bar.Left > 0 && float32(bar.Left+16) > left {
		left = float32(bar.Left + 16)
	}
	if bar.Right > 0 && float32(bar.Right+12) > right {
		right = float32(bar.Right + 12)
	}
	return height, left, right
}

func (s *Shell) View(c *ui.Context) {
	p := presentation.PreferenceColors(c.Theme().Dark, s.Preferences)
	background := p.ChromeBackground(s.Preferences.LiquidGlassStyle, c.Vibrancy(), c.Preferences())
	// The root and Themed both paint an opaque background by default. Leave
	// one tone layer over the system material, including behind the sidebar.
	c.Root().Background(ui.Transparent)
	menuCollapsed := shellMenuCollapsed(c)
	titlebarHeight, titlebarLeft, titlebarRight := titlebarLayout(c)
	ui.Themed(c, &p.Theme).Fill().Background(background).Children(func() {
		ui.Column(c).Fill().Children(func() {
			ui.Row(c).FillWidth().Height(titlebarHeight).Padding(0, titlebarRight, 0, titlebarLeft).AlignItems(ui.Center).Gap(18).Children(func() {
				menuLabel := s.msg("shell.collapseMenu")
				if *menuCollapsed {
					menuLabel = s.msg("shell.expandMenu")
				}
				menuToggle := ui.ButtonBase(c.Key("toggle-module-menu")).Size(32, 30).Shrink(0).Padding(0).Center().Radius(6).
					Label(menuLabel).Tooltip(menuLabel).Expanded(!*menuCollapsed)
				_, menuColor := navigationColors(c, menuToggle, false)
				menuToggle.Children(func() {
					ui.Icon(c, presentation.Icon("sidebar")).Size(18, 18).TextColor(menuColor)
				})
				if menuToggle.Clicked() {
					*menuCollapsed = !*menuCollapsed
					c.Invalidate()
				}
				values := [2]string{"—", "—"}
				memoryHint := "memoryUsageHint"
				if s.Metrics != nil && time.Since(s.Metrics.SampledAt) <= 3*time.Second {
					if s.Metrics.CPUPercent != nil {
						values[0] = fmt.Sprintf("%.1f%%", *s.Metrics.CPUPercent)
					}
					values[1] = fmt.Sprintf("%.0f MiB", float64(s.Metrics.MemoryBytes)/(1024*1024))
					if s.Metrics.MemoryKind == domain.ProcessMemoryFootprint {
						memoryHint = "memoryFootprintHint"
					} else if s.Metrics.MemoryKind == domain.ProcessMemoryWorkingSet {
						memoryHint = "memoryWorkingSetHint"
					}
				}
				for index, key := range []string{"cpu", "memory"} {
					hint := s.msg("shell." + memoryHint)
					if key == "cpu" {
						hint = s.msg("shell.cpuUsageHint")
					}
					ui.Row(c.Key(key)).Gap(4).AlignItems(ui.Center).Tooltip(hint).Description(hint).Children(func() {
						ui.Icon(c, presentation.Icon(key)).Size(15, 15).TextColor(c.Theme().TextMuted)
						ui.Text(c, s.msg("shell."+key)).FontSize(14).TextColor(c.Theme().TextMuted)
						ui.Text(c, values[index]).FontSize(14).Bold()
					})
				}
				ui.Row(c).Grow(1).FillHeight().DragWindow()
				petLabel, petHint := s.msg("shell.petDisabled"), s.msg("shell.petEnableTitle")
				if s.PetEnabled {
					petLabel, petHint = s.msg("shell.petEnabled"), s.msg("shell.petDisableTitle")
				}
				petDot := c.Theme().TextMuted
				if s.PetEnabled {
					petDot = c.Theme().Success
				}
				toggle := ui.ButtonBase(c).Padding(3).Gap(5).Label(s.msg("shell.statusActions.togglePet")).Tooltip(petHint).
					Role(ui.RoleToggleButton).Checked(s.PetEnabled).Disabled(s.TogglePet == nil)
				toggle.Children(func() {
					ui.Box(c).Size(6, 6).Radius(3).Background(petDot)
					ui.Text(c, petLabel).FontSize(14)
				})
				if toggle.Clicked() {
					s.TogglePet()
				}
				modeKey, readyKey := "shell.localMode", "shell.localReady"
				if s.Source == "remote" {
					modeKey, readyKey = "shell.remoteMode", "shell.remoteReady"
				}
				statusColor := c.Theme().TextMuted
				if s.SourceReady {
					modeKey, statusColor = readyKey, c.Theme().Success
				}
				ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(6, 6).Radius(3).Background(statusColor)
					ui.Text(c, s.msg(modeKey)).FontSize(14)
				})
				link := ui.ButtonBase(c).Size(24, 24).Padding(0).Center().Label(s.msg("shell.github")).Tooltip(s.msg("shell.github"))
				link.Children(func() { ui.Icon(c, presentation.Icon("github")).Size(17, 17) })
				if link.Clicked() {
					c.OpenURL("https://github.com/westng/Liberty")
				}
			})
			width, _ := c.Size()
			settings := s.Route == "/settings" && s.Sidebar != nil
			compactSettings := settings && width <= 760
			immersive := s.Route == "/farm" || strings.HasPrefix(s.Route, "/work-game/")
			contentWidth := width - shellNavigationWidth(c) - 2*shellContentPaddingX
			if compactSettings {
				contentWidth = float32(width - 2*shellContentPaddingX)
			}
			contentWidth = max(0, contentWidth)
			if !immersive {
				contentWidth = min(shellContentMaxWidth, contentWidth)
			}
			var body ui.Element
			if compactSettings {
				body = ui.Column(c).FillWidth().Grow(1).MinHeight(0)
			} else {
				body = ui.Row(c).FillWidth().Grow(1).MinHeight(0).AlignItems(ui.Stretch)
			}
			body.Children(func() {
				s.activityBar(c, compactSettings)
				if !*menuCollapsed {
					sidebar := ui.Column(c.Key("shell-sidebar")).Width(presentation.SidebarMenuWidth).Shrink(0).MinHeight(0)
					if compactSettings {
						sidebar.FillWidth()
					}
					sidebar.Children(func() {
						if s.Sidebar != nil {
							s.Sidebar(c)
							return
						}
						s.moduleSidebar(c)
					})
				}
				content := ui.Column(c.Key("shell-content")).Grow(1).MinWidth(0).MinHeight(0)
				if compactSettings {
					content.FillWidth()
				}
				content.Children(func() {
					ui.Column(c).Grow(1).MinHeight(0).FillWidth().Padding(12, shellContentPaddingX, 14).Radius(12, 0, 0, 12).Clip().Background(p.Content).AlignItems(ui.Center).Children(func() {
						ui.Column(c).Width(contentWidth).FillHeight().MinWidth(0).MinHeight(0).Clip().Children(func() {
							if s.Content != nil {
								s.Content(c)
							}
						})
					})
					ui.Row(c).FillWidth().Height(presentation.StatusbarHeight).Padding(0, 12).AlignItems(ui.Center).Gap(8).Children(func() {
						statusMessage, statusColor := s.StatusMessage, c.Theme().Success
						if statusMessage == "" {
							statusMessage = s.msg("shell.statusReady")
							statusColor = c.Theme().TextMuted
						} else {
							switch s.StatusTone {
							case "error":
								statusColor = c.Theme().Danger
							case "progress":
								statusColor = c.Theme().Warning
							}
						}
						ui.Row(c).Size(5, 5).Radius(3).Background(statusColor)
						ui.Text(c, statusMessage).FontSize(11).TextColor(statusColor).Grow(1).MinWidth(0).SingleLine().Tooltip(statusMessage).Role(ui.RoleStatus)
						if s.ModeReadFailed {
							retry := textButton(c, s.msg("common.retry")).Disabled(s.ModeReadBusy || s.RetryMode == nil)
							if retry.Clicked() {
								s.RetryMode()
							}
						}
						ui.Text(c, "v"+s.Version).FontSize(11).TextColor(c.Theme().TextMuted)
					})
				})
			})
		})
	})
}
