package jobdb

import (
	"context"
	"database/sql"
	"math"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

func (s *Store) Settings(ctx context.Context) (application.SettingsRecord, error) {
	// Explicit settings access initializes the singleton, like the Rust API.
	if _, err := s.db.ExecContext(ctx, settingsDefaults); err != nil {
		return application.SettingsRecord{}, ErrDatabase
	}
	var r application.SettingsRecord
	v := &r.Snapshot
	err := s.db.QueryRowContext(ctx, `SELECT theme_mode,liquid_glass_style,accent_color,locale,backend_url,processing_mode,default_hotwords,summary_template,concurrency,runner_script_path,local_asr_device,local_asr_threads,local_asr_batch_size_seconds,runtime_download_source,settings_revision,COALESCE(api_token_ref,''),TRIM(api_token)<>'',EXISTS(SELECT 1 FROM app_meta WHERE key='native_hide_performance_indicators' AND value='1'),NOT EXISTS(SELECT 1 FROM app_meta WHERE key='native_live_translation_disabled' AND value='1'),COALESCE((SELECT value FROM app_meta WHERE key='native_live_device_id'),'') FROM app_settings WHERE id=1`).Scan(
		&v.ThemeMode, &v.LiquidGlassStyle, &v.AccentColor, &v.Locale, &v.BackendURL, &v.ProcessingMode, &v.DefaultHotwords, &v.SummaryTemplate, &v.Concurrency, &v.RunnerPath, &v.Device, &v.Threads, &v.BatchSeconds, &v.DownloadSource, &v.Revision, &r.Reference, &r.LegacyPresent, &v.HidePerformanceIndicators, &v.LiveTranslate, &v.LiveDeviceID)
	if err != nil {
		return r, ErrDatabase
	}
	r.Snapshot = r.Snapshot.Normalized()
	return r, nil
}
func (s *Store) PublishSettings(ctx context.Context, r application.SettingsRecord, expected string, retire *application.CredentialCleanup) error {
	v := r.Snapshot.Normalized()
	if v.Revision < 0 || v.Revision == math.MaxInt64 {
		return domain.ErrResourceInvalid
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		// Deliberately omit Python/ffmpeg paths and sources; their generation-aware
		// publisher is the only writer. Revision CAS fences every public save.
		result, err := tx.ExecContext(ctx, `UPDATE app_settings SET theme_mode=?,liquid_glass_style=?,accent_color=?,locale=?,backend_url=?,api_token='',api_token_ref=?,processing_mode=?,default_hotwords=?,summary_template=?,concurrency=?,runner_script_path=?,local_asr_device=?,local_asr_threads=?,local_asr_batch_size_seconds=?,runtime_download_source=?,settings_revision=settings_revision+1 WHERE id=1 AND settings_revision=? AND COALESCE(api_token_ref,'')=? AND TRIM(api_token)=''`,
			v.ThemeMode, v.LiquidGlassStyle, v.AccentColor, v.Locale, v.BackendURL, r.Reference, v.ProcessingMode, v.DefaultHotwords, v.SummaryTemplate, v.Concurrency, v.RunnerPath, v.Device, v.Threads, v.BatchSeconds, v.DownloadSource, v.Revision, expected)
		if err != nil {
			return ErrDatabase
		}
		count, err := result.RowsAffected()
		if err != nil {
			return ErrDatabase
		}
		if count != 1 {
			return domain.ErrSettingsConflict
		}
		if v.LiveTranslate {
			_, err = tx.ExecContext(ctx, "DELETE FROM app_meta WHERE key='native_live_translation_disabled'")
		} else {
			_, err = tx.ExecContext(ctx, "INSERT OR REPLACE INTO app_meta(key,value) VALUES('native_live_translation_disabled','1')")
		}
		if err != nil {
			return ErrDatabase
		}
		if v.LiveDeviceID == "" {
			_, err = tx.ExecContext(ctx, "DELETE FROM app_meta WHERE key='native_live_device_id'")
		} else {
			_, err = tx.ExecContext(ctx, "INSERT OR REPLACE INTO app_meta(key,value) VALUES('native_live_device_id',?)", v.LiveDeviceID)
		}
		if err != nil {
			return ErrDatabase
		}
		if v.HidePerformanceIndicators {
			_, err = tx.ExecContext(ctx, "INSERT OR REPLACE INTO app_meta(key,value) VALUES('native_hide_performance_indicators','1')")
		} else {
			_, err = tx.ExecContext(ctx, "DELETE FROM app_meta WHERE key='native_hide_performance_indicators'")
		}
		if err != nil {
			return ErrDatabase
		}
		return publishCredential(ctx, tx, r.Reference, retire)
	})
}

var _ application.ResourceRepository = (*Store)(nil)
