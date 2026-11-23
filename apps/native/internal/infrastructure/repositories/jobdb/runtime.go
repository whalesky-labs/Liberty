package jobdb

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"strconv"
	"time"

	"liberty.local/liberty/internal/domain"
)

const settingsDefaults = `INSERT INTO app_settings(id,theme_mode,liquid_glass_style,accent_color,locale,backend_url,api_token,default_hotwords,summary_template,python_path,runner_script_path)
 VALUES(1,'auto','transparent','#2f6dff','zh-CN','','','SeACo-Paraformer, FunASR, 会议纪要','表格版会议纪要','','') ON CONFLICT(id) DO NOTHING`

func (s *Store) RuntimeSettings(ctx context.Context) (domain.RuntimeSettings, error) {
	result := domain.DefaultRuntimeSettings()
	err := s.db.QueryRowContext(ctx, `SELECT python_runtime_source,ffmpeg_runtime_source,python_path,ffmpeg_path,runner_script_path,runtime_download_source,local_asr_device,concurrency,local_asr_threads,local_asr_batch_size_seconds FROM app_settings WHERE id=1`).Scan(
		&result.PythonSource, &result.FFmpegSource, &result.PythonPath, &result.FFmpegPath, &result.RunnerPath, &result.DownloadSource, &result.Device, &result.Concurrency, &result.Threads, &result.BatchSeconds)
	if err == sql.ErrNoRows {
		return result, nil
	}
	if err != nil {
		return result, ErrDatabase
	}
	return result, nil
}
func (s *Store) RuntimeSource(ctx context.Context, component, source string) error {
	if err := domain.RuntimeKey(component, source); err != nil {
		return err
	}
	if component == "model" {
		return domain.ErrRuntimeSource
	}
	column := "python"
	if component == "ffmpeg" {
		column = "ffmpeg"
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := execute(ctx, tx, settingsDefaults); err != nil {
			return err
		}
		return execute(ctx, tx, `UPDATE app_settings SET `+column+`_runtime_source=?,`+column+`_path=CASE WHEN ?='managed' THEN '' ELSE `+column+`_path END,settings_revision=settings_revision+1 WHERE id=1`, source, source)
	})
}
func (s *Store) RuntimeDownloadSource(ctx context.Context, source string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := execute(ctx, tx, settingsDefaults); err != nil {
			return err
		}
		return execute(ctx, tx, `UPDATE app_settings SET runtime_download_source=?,settings_revision=settings_revision+1 WHERE id=1`, source)
	})
}
func loadComponent(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, platform, component, source string) (domain.RuntimeComponent, error) {
	result := domain.NewRuntimeComponent(component, source, strconv.FormatInt(time.Now().UnixMilli(), 10))
	var generation, version, path *string
	err := q.QueryRowContext(ctx, `SELECT availability,active_generation_id,artifact_version,resolved_path,operation_kind,operation_generation,phase,progress,last_error,updated_at
 FROM runtime_component_state WHERE platform_id=? AND component=? AND source=?`, platform, component, source).Scan(&result.Availability, &generation, &version, &path, &result.Operation.Kind, &result.Operation.Generation, &result.Operation.Phase, &result.Operation.Progress, &result.Operation.LastError, &result.UpdatedAt)
	if err == sql.ErrNoRows {
		return result, nil
	}
	if err != nil {
		return result, ErrDatabase
	}
	if generation != nil && version != nil && path != nil {
		result.Artifact = &domain.RuntimeArtifact{GenerationID: *generation, Version: *version, Path: *path}
	}
	return result, nil
}
func (s *Store) RuntimeComponent(ctx context.Context, platform, component, source string) (domain.RuntimeComponent, error) {
	if err := domain.RuntimeKey(component, source); err != nil {
		return domain.RuntimeComponent{}, err
	}
	return loadComponent(ctx, s.db, platform, component, source)
}
func saveComponent(ctx context.Context, tx *sql.Tx, platform string, state domain.RuntimeComponent) error {
	var generation, version, path *string
	if state.Artifact != nil {
		generation = &state.Artifact.GenerationID
		version = &state.Artifact.Version
		path = &state.Artifact.Path
	}
	return execute(ctx, tx, `INSERT INTO runtime_component_state(platform_id,component,source,availability,active_generation_id,artifact_version,resolved_path,operation_kind,operation_generation,phase,progress,last_error,updated_at)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(platform_id,component,source) DO UPDATE SET availability=excluded.availability,active_generation_id=excluded.active_generation_id,artifact_version=excluded.artifact_version,
 resolved_path=excluded.resolved_path,operation_kind=excluded.operation_kind,operation_generation=excluded.operation_generation,phase=excluded.phase,progress=excluded.progress,last_error=excluded.last_error,updated_at=excluded.updated_at`,
		platform, state.Component, state.SourceName(), state.Availability, generation, version, path, state.Operation.Kind, state.Operation.Generation, state.Operation.Phase, state.Operation.Progress, state.Operation.LastError, state.UpdatedAt)
}

// BeginRuntime increments or resumes an operation atomically; a retryable model
// generation can be reused without publishing another component's state.
func (s *Store) BeginRuntime(ctx context.Context, platform, component, source, kind, phase string, reuse bool) (domain.RuntimeComponent, error) {
	var state domain.RuntimeComponent
	if err := domain.RuntimeKey(component, source); err != nil {
		return state, err
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		state, err = loadComponent(ctx, tx, platform, component, source)
		if err != nil {
			return err
		}
		if state.Operation.Generation >= math.MaxInt64 {
			return domain.ErrRuntimeStale
		}
		if !reuse || state.Operation.Generation == 0 {
			state.Operation.Generation++
		}
		state.Operation.Kind = kind
		state.Operation.Phase = phase
		state.Operation.Progress = nil
		state.Operation.LastError = nil
		state.UpdatedAt = strconv.FormatInt(time.Now().UnixMilli(), 10)
		return saveComponent(ctx, tx, platform, state)
	})
	return state, err
}
func (s *Store) UpdateRuntime(ctx context.Context, platform string, state domain.RuntimeComponent) error {
	if err := domain.RuntimeKey(state.Component, state.SourceName()); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		current, err := loadComponent(ctx, tx, platform, state.Component, state.SourceName())
		if err != nil {
			return err
		}
		if current.Operation.Generation != state.Operation.Generation {
			return domain.ErrRuntimeStale
		}
		if err := saveComponent(ctx, tx, platform, state); err != nil {
			return err
		}
		if state.SourceName() == "system" && state.Ready() && (!current.Ready() || !reflect.DeepEqual(current.Artifact, state.Artifact)) {
			if err := execute(ctx, tx, settingsDefaults); err != nil {
				return err
			}
			column := "python"
			if state.Component == "ffmpeg" {
				column = "ffmpeg"
			}
			return execute(ctx, tx, `UPDATE app_settings SET `+column+`_path=?,settings_revision=settings_revision+1 WHERE id=1 AND `+column+`_runtime_source='system'`, state.Artifact.Path)
		}
		return nil
	})
}
func (s *Store) LegacyRuntime(ctx context.Context, platform string) (domain.RuntimeOverview, error) {
	state := domain.RuntimeOverview{PlatformID: platform}
	err := s.db.QueryRowContext(ctx, `SELECT runtime_version,python_version,status,python_executable_path,models_root,install_root,last_error,installed_at,updated_at,last_log_path FROM runtime_state WHERE platform_id=?`, platform).
		Scan(&state.RuntimeVersion, &state.PythonVersion, &state.Status, &state.PythonPath, &state.ModelsRoot, &state.InstallRoot, &state.LastError, &state.InstalledAt, &state.UpdatedAt, &state.LastLogPath)
	if err == sql.ErrNoRows {
		return state, nil
	}
	if err != nil {
		return state, ErrDatabase
	}
	return state, nil
}
func (s *Store) SaveRuntimeOverview(ctx context.Context, state domain.RuntimeOverview) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		return execute(ctx, tx, `INSERT INTO runtime_state(platform_id,runtime_version,python_version,status,python_executable_path,models_root,install_root,last_error,installed_at,updated_at,last_log_path)
 VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(platform_id) DO UPDATE SET runtime_version=excluded.runtime_version,python_version=excluded.python_version,status=excluded.status,python_executable_path=excluded.python_executable_path,
 models_root=excluded.models_root,install_root=excluded.install_root,last_error=excluded.last_error,installed_at=excluded.installed_at,updated_at=excluded.updated_at,last_log_path=excluded.last_log_path`, state.PlatformID, state.RuntimeVersion, state.PythonVersion, state.Status, state.PythonPath, state.ModelsRoot, state.InstallRoot, state.LastError, state.InstalledAt, state.UpdatedAt, state.LastLogPath)
	})
}
