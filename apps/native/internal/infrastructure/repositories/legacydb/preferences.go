package legacydb

import (
	"context"
	"database/sql"
	"errors"

	"liberty.local/liberty/internal/domain"
)

func (r *Reader) UIPreferences(ctx context.Context) (domain.UIPreferences, error) {
	var p domain.UIPreferences
	err := r.db.QueryRowContext(ctx, "SELECT theme_mode,liquid_glass_style,accent_color,locale,EXISTS(SELECT 1 FROM app_meta WHERE key='native_hide_performance_indicators' AND value='1') FROM app_settings WHERE id=1").Scan(&p.ThemeMode, &p.LiquidGlassStyle, &p.AccentColor, &p.Locale, &p.HidePerformanceIndicators)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DefaultUIPreferences(), nil
	}
	if err != nil {
		return domain.UIPreferences{}, ErrDatabase
	}
	return p, nil
}
