package client

import "liberty.local/liberty/internal/domain"

type StartupPreferences struct {
	Locale    string
	ThemeMode string
}

func (p StartupPreferences) Apply(preferences domain.UIPreferences) domain.UIPreferences {
	if p.Locale != "" {
		preferences.Locale = p.Locale
	}
	if p.ThemeMode != "" {
		preferences.ThemeMode = p.ThemeMode
	}
	return preferences
}

type preferenceSelections struct {
	locale, themeMode *string
}

func (s preferenceSelections) apply(preferences domain.UIPreferences) domain.UIPreferences {
	if s.locale != nil {
		preferences.Locale = *s.locale
	}
	if s.themeMode != nil {
		preferences.ThemeMode = *s.themeMode
	}
	return preferences
}

func (c *SettingsController) UIPreferences() domain.UIPreferences {
	return c.startup.Apply(c.Draft.UIPreferences)
}

func (c *SettingsController) applyPreferences(preferences domain.UIPreferences) {
	c.applied(c.selections.apply(c.startup.Apply(preferences)))
}

func (c *SettingsController) SetLocale(locale string) {
	if c.closed || !c.Loaded || locale != "zh-CN" && locale != "en-US" {
		return
	}
	if !c.Busy && c.startup.Locale == "" && c.selections.locale == nil && c.Draft.Locale == locale && c.original.Locale == locale {
		return
	}
	c.startup.Locale = ""
	c.Draft.Locale = locale
	c.selections.locale = &locale
	c.Save()
}

func (c *SettingsController) SetThemeMode(mode string) {
	if c.closed || !c.Loaded || mode != "auto" && mode != "light" && mode != "dark" {
		return
	}
	if !c.Busy && c.startup.ThemeMode == "" && c.selections.themeMode == nil && c.Draft.ThemeMode == mode && c.original.ThemeMode == mode {
		return
	}
	c.startup.ThemeMode = ""
	c.Draft.ThemeMode = mode
	c.selections.themeMode = &mode
	c.Save()
}
