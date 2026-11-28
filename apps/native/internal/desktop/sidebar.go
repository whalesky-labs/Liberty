package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
)

func (s *Shell) activityBar(c *ui.Context, horizontal bool) {
	var rail ui.Element
	if horizontal {
		rail = ui.Row(c.Key("activity-bar")).FillWidth().Height(presentation.ActivityBarWidth).Padding(4, 8).Gap(4).AlignItems(ui.Center)
	} else {
		rail = ui.Column(c.Key("activity-bar")).Width(presentation.ActivityBarWidth).FillHeight().MinHeight(0).Shrink(0).Padding(8, 4).Gap(8).AlignItems(ui.Center)
	}
	current := presentation.NavigationSection(s.Route)
	rail.Role(ui.RoleNavigation).Label(localized(s.Locale, "主导航", "Primary navigation")).Children(func() {
		var modules ui.Element
		if horizontal {
			modules = ui.Row(c).Gap(4)
		} else {
			modules = ui.Scroll(c).FillWidth().Grow(1).MinHeight(0)
		}
		modules.Children(func() {
			var items ui.Element
			if horizontal {
				items = ui.Row(c).Gap(4)
			} else {
				items = ui.Column(c).FillWidth().Gap(6).AlignItems(ui.Center)
			}
			items.Children(func() {
				for _, section := range presentation.Navigation {
					path := ""
					for _, item := range section.Items {
						if s.can(item.Path) {
							path = item.Path
							break
						}
					}
					active := s.Route != "/settings" && current.Key == section.Key
					s.moduleButton(c, section.Key, section.Icon, localized(s.Locale, section.Zh, section.En), path, active)
				}
			})
		})
		if horizontal {
			ui.Box(c).Grow(1)
		} else {
			ui.Box(c).Width(24).Height(1).Background(palette(c).Separator)
		}
		s.moduleButton(c, "settings", "gear", s.msg("nav.settings"), "/settings", s.Route == "/settings")
		if !horizontal {
			ui.Box(c).Absolute().Right(0).Top(8).Bottom(8).Width(1).Background(palette(c).Separator)
		}
	})
}

func (s *Shell) moduleButton(c *ui.Context, key, icon, label, path string, active bool) {
	button := ui.ButtonBase(c.Key(key)).Size(44, 44).Shrink(0).Padding(0).Center().Radius(8).
		Label(label).Tooltip(label).Selected(active).Disabled(path == "" || !s.can(path))
	if active {
		button.Current(ui.CurrentPage)
	}
	_, iconColor := navigationColors(c, button, active)
	button.Children(func() {
		ui.Icon(c, presentation.Icon(icon)).Size(20, 20).TextColor(iconColor)
		if active {
			ui.Box(c).Absolute().Left(1).Top(15).Width(2).Height(14).Radius(1).Background(iconColor)
		}
	})
	// Clicking the current module must not reset a detail page or an active draft.
	if button.Clicked() && !active {
		s.Navigate(path)
	}
}

func (s *Shell) moduleSidebar(c *ui.Context) {
	section := presentation.NavigationSection(s.Route)
	ui.Row(c).FillWidth().Height(52).Padding(8, 16).Gap(8).AlignItems(ui.Center).Children(func() {
		mascot := presentation.Mascot.Still()
		if s.ActiveJobs > 0 {
			mascot = presentation.Mascot.Frame(c)
		}
		ui.Image(c, mascot).Size(28, 28).Label(localized(s.Locale, "Liberty 吉祥物", "Liberty mascot"))
		ui.Text(c, "Liberty").FontSize(17)
	})
	ui.Scroll(c.Key(section.Key)).FillWidth().Grow(1).MinHeight(0).
		Role(ui.RoleNavigation).Label(localized(s.Locale, "模块菜单", "Module navigation")).Children(func() {
		ui.Column(c).FillWidth().Padding(0, 8, 12).Gap(3).Children(func() {
			if section.Key == "work" {
				s.nav(c, presentation.NavItem{Key: "newJob", Path: "/jobs/new", Icon: "plus"})
			}
			ui.Text(c, localized(s.Locale, section.Zh, section.En)).Padding(16, 8, 6).FontSize(12).TextColor(c.Theme().TextMuted)
			for _, item := range section.Items {
				if item.Path != "/jobs/new" {
					s.nav(c, item)
				}
			}
		})
	})
	ui.Column(c).FillWidth().Padding(8, 12, 12).Gap(2).Children(func() {
		separator(c)
		mode := "shell.localMode"
		if s.Source == "remote" {
			mode = "shell.remoteMode"
		}
		s.footer(c, "mode", s.msg("shell.modeLabel"), s.msg(mode))
		s.footer(c, "processing", s.msg("shell.processingLabel"), fmt.Sprint(s.ActiveJobs))
	})
}

func (s *Shell) nav(c *ui.Context, item presentation.NavItem) {
	active := item.Active(s.Route)
	label := s.msg("nav." + item.Key)
	button := ui.ButtonBase(c.Key(item.Path)).FillWidth().MinHeight(32).Padding(5, 8).Gap(8).Radius(7).
		Justify(ui.Start).Label(label).Tooltip(label).Selected(active).Disabled(!s.can(item.Path))
	if active {
		button.Current(ui.CurrentPage)
	}
	if item.Path == "/jobs/new" {
		button.Background(palette(c).NavHover)
	}
	navigationContent(c, button, item.Icon, label, active)
	if button.Clicked() {
		s.Navigate(item.Path)
	}
}

func (s *Shell) footer(c *ui.Context, icon, label, value string) {
	ui.Row(c).FillWidth().MinHeight(28).Padding(4).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, presentation.Icon(icon)).Size(14, 14).TextColor(c.Theme().TextMuted)
		ui.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted).Grow(1)
		ui.Text(c, value).FontSize(11).TextColor(c.Theme().TextMuted)
	})
}
