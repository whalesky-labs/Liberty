package desktop

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

// Shell provides a module rail, contextual sidebar, title bar and status bar. Routes are enabled
// only once the corresponding application capability is supplied by the shell.
// A missing capability must not open an inert, apparently working page.
type Shell struct {
	Preferences     domain.UIPreferences
	Locale          string
	Route           string
	Source          string
	Version         string
	ActiveJobs      int
	StatusMessage   string
	StatusTone      string
	ModeReadFailed  bool
	ModeReadBusy    bool
	Metrics         *domain.ProcessMetrics
	PetEnabled      bool
	SourceReady     bool
	TogglePet       func()
	RetryMode       func()
	Back            func()
	Forward         func()
	CanBack         bool
	CanForward      bool
	Navigate        func(string)
	Available       func(string) bool
	Content         func(*ui.Context)
	Sidebar         func(*ui.Context)
	TitlebarActions func(*ui.Context)
	TrailingPane    *ui.Handle
}

const (
	shellContentPaddingX = 12
	shellContentMaxWidth = 1180
)

func contentScroll(c *ui.Context) ui.Element {
	// MyGo centers the thumb 3 DIPs inside its track by default. A negative
	// right inset cancels that offset so the thumb itself meets the viewport edge.
	return ui.Scroll(c).ScrollbarInsets(-3)
}

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
	return min(shellContentMaxWidth, max(0, width-shellNavigationWidth(c)-shellContentPaddingX))
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
	width, _ := c.Size()
	settings := s.Route == "/settings" && s.Sidebar != nil
	compactSettings := settings && width <= 760
	ui.Themed(c, &p.Theme).Fill().Background(background).Children(func() {
		if !compactSettings {
			// One continuous reading surface includes the header and status bar.
			// Its left corners meet the navigation chrome at the window edges.
			surface := ui.Box(c.Key("shell-content-surface")).Absolute().Left(shellNavigationWidth(c)).Right(0).Top(0).Bottom(0).
				Radius(12, 0, 0, 12).Background(p.Content).ZIndex(-1).PassThrough()
			if pane := s.TrailingPane; pane != nil {
				border := c.Theme().Border
				surface.Draw(func(painter *ui.Painter, r ui.Rect) {
					bounds := pane.Bounds(c)
					if bounds.W > 0 {
						painter.Fill(ui.Rect{X: r.X, Y: bounds.Y - 1, W: r.W, H: 1}, border, 0)
						// Continue the pane's one-DIP leading divider behind the window chrome.
						painter.Fill(ui.Rect{X: bounds.X - 1, Y: r.Y, W: 1, H: bounds.Y - r.Y}, border, 0)
						bottom := bounds.Y + bounds.H
						painter.Fill(ui.Rect{X: bounds.X - 1, Y: bottom, W: 1, H: r.Y + r.H - bottom}, border, 0)
					}
				})
			}
		}
		ui.Column(c).Fill().Children(func() {
			s.header(c, titlebarHeight, titlebarLeft, titlebarRight)
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
					content.FillWidth().Radius(12, 0, 0, 12).Clip().Background(p.Content)
				}
				content.Children(func() {
					frame := ui.Column(c).Grow(1).MinHeight(0).FillWidth().Clip().AlignItems(ui.Center)
					if s.Route == "/live" {
						// The live panes provide their own insets so the splitter can span the body.
						frame.Padding(0, shellContentPaddingX)
					} else {
						frame.Padding(12, 0, 14, shellContentPaddingX)
					}
					frame.Children(func() {
						ui.Column(c).FillWidth().FillHeight().MinWidth(0).MinHeight(0).Clip().Children(func() {
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

func (s *Shell) header(c *ui.Context, height, left, right float32) {
	// When the menu is collapsed, controls may extend over the content header
	// to keep clear of native traffic lights without widening the module rail.
	controlsWidth := max(shellNavigationWidth(c), left+120)
	ui.Row(c.Key("shell-header")).FillWidth().Height(height).Shrink(0).Children(func() {
		ui.Row(c.Key("shell-navigation-controls")).Width(controlsWidth).Shrink(0).FillHeight().Padding(0, 12, 0, left).Gap(4).AlignItems(ui.Center).DragWindow().Children(func() {
			collapsed := shellMenuCollapsed(c)
			label := s.msg("shell.collapseMenu")
			if *collapsed {
				label = s.msg("shell.expandMenu")
			}
			toggle := ui.ButtonBase(c.Key("toggle-module-menu")).Size(32, 30).Shrink(0).Padding(0).Center().Radius(6).
				Label(label).Tooltip(label).Expanded(!*collapsed)
			_, color := navigationColors(c, toggle, false)
			toggle.Children(func() { ui.Icon(c, presentation.Icon("sidebar")).Size(18, 18).TextColor(color) })
			if toggle.Clicked() {
				*collapsed = !*collapsed
				c.Invalidate()
			}
			ui.Box(c).Grow(1).FillHeight().DragWindow()
			s.historyButton(c, "back", s.CanBack, s.Back)
			s.historyButton(c, "forward", s.CanForward, s.Forward)
		})
		ui.Row(c.Key("shell-content-header")).Grow(1).MinWidth(0).FillHeight().Padding(0, right, 0, 12).AlignItems(ui.Center).Gap(16).DragWindow().Children(func() {
			if !s.Preferences.HidePerformanceIndicators {
				s.processMetrics(c)
			}
			ui.Box(c).Grow(1).FillHeight().DragWindow()
			s.headerActions(c)
		})
	})
}

func (s *Shell) historyButton(c *ui.Context, key string, available bool, action func()) {
	label := s.msg("shell." + key)
	button := ui.ButtonBase(c.Key(key)).Size(32, 30).Shrink(0).Padding(0).Center().Radius(6).
		Label(label).Tooltip(label).Disabled(!available || action == nil)
	_, color := navigationColors(c, button, false)
	button.Children(func() { ui.Icon(c, presentation.Icon(key)).Size(16, 16).TextColor(color) })
	if button.Clicked() {
		action()
	}
}

func (s *Shell) processMetrics(c *ui.Context) {
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
	ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
		for index, key := range []string{"cpu", "memory"} {
			hint := s.msg("shell." + memoryHint)
			if key == "cpu" {
				hint = s.msg("shell.cpuUsageHint")
			}
			ui.Row(c.Key(key)).Gap(4).AlignItems(ui.Center).Tooltip(hint).Description(hint).Children(func() {
				ui.Icon(c, presentation.Icon(key)).Size(15, 15).TextColor(c.Theme().TextMuted)
				ui.Text(c, s.msg("shell."+key)).FontSize(13).TextColor(c.Theme().TextMuted)
				ui.Text(c, values[index]).FontSize(13)
			})
		}
	})
}

func (s *Shell) headerActions(c *ui.Context) {
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
		c.OpenURL(presentation.RepositoryURL)
	}
	if s.TitlebarActions != nil {
		s.TitlebarActions(c)
	}
}
