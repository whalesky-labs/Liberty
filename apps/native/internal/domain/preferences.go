package domain

// UIPreferences contains no processing configuration, paths or credential
// references; result windows need only this existing public preference scope.
type UIPreferences struct {
	ThemeMode        string `json:"themeMode"`
	LiquidGlassStyle string `json:"liquidGlassStyle"`
	AccentColor      string `json:"accentColor"`
	Locale           string `json:"locale"`
}

func DefaultUIPreferences() UIPreferences {
	return UIPreferences{ThemeMode: "auto", LiquidGlassStyle: "transparent", AccentColor: "#2f6dff", Locale: "zh-CN"}
}
