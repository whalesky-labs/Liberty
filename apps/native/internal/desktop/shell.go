package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

// Shell keeps the original sidebar, title bar and status bar. Routes are enabled
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
	GraphicsMB     int
	PetEnabled     bool
	SourceReady    bool
	TogglePet      func()
	RetryMode      func()
	Navigate       func(string)
	Available      func(string) bool
	Content        func(*ui.Context)
	Sidebar        func(*ui.Context)
}

const shellContentPaddingX = 12

func shellContentWidth(c *ui.Context) float32 {
	width, _ := c.Size()
	return max(0, width-presentation.SidebarWidth-2*shellContentPaddingX)
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
	titlebarHeight, titlebarLeft, titlebarRight := titlebarLayout(c)
	ui.Themed(c, &p.Theme).Fill().Children(func() {
		ui.Column(c).Fill().Background(p.Window).Children(func() {
			ui.Row(c).FillWidth().Height(titlebarHeight).Padding(0, titlebarRight, 0, titlebarLeft).AlignItems(ui.Center).Gap(18).Children(func() {
				values := [3]string{"—", "—", "—"}
				if s.Metrics != nil {
					if s.Metrics.CPUPercent != nil {
						values[0] = fmt.Sprintf("%.1f%%", *s.Metrics.CPUPercent)
					}
					values[1] = fmt.Sprintf("%.0fM", float64(s.Metrics.ResidentBytes)/(1024*1024))
				}
				if s.GraphicsMB > 0 {
					values[2] = fmt.Sprintf("%dM", s.GraphicsMB)
				}
				for index, key := range []string{"cpu", "memory", "graphics"} {
					ui.Row(c.Key(key)).Gap(4).AlignItems(ui.Center).Children(func() {
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
			var body ui.Element
			if compactSettings {
				body = ui.Column(c).FillWidth().Grow(1).MinHeight(0)
			} else {
				body = ui.Row(c).FillWidth().Grow(1).MinHeight(0).AlignItems(ui.Stretch)
			}
			body.Children(func() {
				sidebar := ui.Column(c).Width(presentation.SidebarWidth).Shrink(0).Background(p.Window)
				if settings {
					sidebar.Width(248)
				}
				if compactSettings {
					sidebar.FillWidth()
				}
				sidebar.Children(func() {
					if s.Sidebar != nil {
						s.Sidebar(c)
						return
					}
					ui.Column(c).Padding(16, 12, 12).Gap(6).Children(func() {
						ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
							ui.Image(c, presentation.Mascot.Frame(c)).Size(58, 58).Label("Liberty mascot")
							ui.Text(c, "Liberty.").FontSize(18).Bold()
						})
						ui.Text(c, s.msg("shell.slogan")).FontSize(12).TextColor(c.Theme().TextMuted)
					})
					ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
						ui.Column(c).FillWidth().Padding(6, 8, 12).Gap(14).Label("Liberty").Children(func() {
							for _, section := range presentation.Navigation {
								ui.Column(c.Key(section.En)).FillWidth().Gap(3).Children(func() {
									ui.Text(c, localized(s.Locale, section.Zh, section.En)).Padding(6, 8, 3).FontSize(13).FontWeight(650).TextColor(c.Theme().TextMuted)
									for _, item := range section.Items {
										s.nav(c, item)
									}
								})
							}
						})
					})
					ui.Column(c).FillWidth().Padding(8).Gap(3).Children(func() {
						s.nav(c, presentation.NavItem{Key: "settings", Path: "/settings", Icon: "gear"})
						mode := "shell.localMode"
						if s.Source == "remote" {
							mode = "shell.remoteMode"
						}
						s.footer(c, "mode", s.msg("shell.modeLabel"), s.msg(mode))
						s.footer(c, "processing", s.msg("shell.processingLabel"), fmt.Sprint(s.ActiveJobs))
					})
				})
				content := ui.Column(c).Grow(1).MinWidth(0).MinHeight(0)
				if compactSettings {
					content.FillWidth()
				}
				content.Children(func() {
					ui.Column(c).Grow(1).MinHeight(0).FillWidth().Padding(12, shellContentPaddingX, 14).Background(p.Content).Children(func() {
						if s.Content != nil {
							s.Content(c)
						}
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
func (s *Shell) nav(c *ui.Context, item presentation.NavItem) {
	active := s.Route == item.Path
	if item.Path == "/work-market" {
		active = s.Route == "/work-market" || s.Route == "/farm" || strings.HasPrefix(s.Route, "/work-game/")
	}
	if item.Path == "/jobs" {
		active = s.Route == "/jobs" || (strings.HasPrefix(s.Route, "/jobs/") && s.Route != "/jobs/new")
	}
	b := ui.ButtonBase(c.Key(item.Path)).FillWidth().MinHeight(32).Padding(5, 8).Gap(8).Radius(7).Justify(ui.Start).Label(s.msg("nav." + item.Key)).Selected(active).Disabled(!s.can(item.Path))
	navigationContent(c, b, item.Icon, s.msg("nav."+item.Key), active)
	if b.Clicked() {
		s.Navigate(item.Path)
	}
}
func (s *Shell) footer(c *ui.Context, icon, label, value string) {
	ui.Row(c).FillWidth().MinHeight(32).Padding(5, 8).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, presentation.Icon(icon)).Size(16, 16).TextColor(c.Theme().TextMuted)
		ui.Text(c, label).FontSize(13).Grow(1)
		ui.Text(c, value).FontSize(11).Bold()
	})
}
