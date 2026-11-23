PRAGMA foreign_keys = ON;

        CREATE TABLE IF NOT EXISTS app_meta (
          key TEXT PRIMARY KEY,
          value TEXT NOT NULL
        );

        CREATE TABLE IF NOT EXISTS app_settings (
          id INTEGER PRIMARY KEY CHECK (id = 1),
          theme_mode TEXT NOT NULL,
          liquid_glass_style TEXT NOT NULL,
          accent_color TEXT NOT NULL,
          locale TEXT NOT NULL,
          backend_url TEXT NOT NULL,
          api_token TEXT NOT NULL,
          api_token_ref TEXT NOT NULL DEFAULT '',
          processing_mode TEXT NOT NULL DEFAULT 'local',
          settings_revision INTEGER NOT NULL DEFAULT 0,
          default_hotwords TEXT NOT NULL,
          summary_template TEXT NOT NULL,
          concurrency INTEGER NOT NULL DEFAULT 2,
          python_path TEXT NOT NULL,
          ffmpeg_path TEXT NOT NULL DEFAULT '',
          python_runtime_source TEXT NOT NULL DEFAULT 'managed',
          ffmpeg_runtime_source TEXT NOT NULL DEFAULT 'managed',
          runner_script_path TEXT NOT NULL,
          local_asr_device TEXT NOT NULL DEFAULT 'auto',
          local_asr_threads INTEGER NOT NULL DEFAULT 0,
          local_asr_batch_size_seconds INTEGER NOT NULL DEFAULT 300,
          runtime_download_source TEXT NOT NULL DEFAULT ''
        );

        CREATE TABLE IF NOT EXISTS runtime_state (
          platform_id TEXT PRIMARY KEY,
          runtime_version TEXT NOT NULL,
          python_version TEXT NOT NULL,
          status TEXT NOT NULL,
          python_executable_path TEXT,
          models_root TEXT,
          install_root TEXT,
          last_error TEXT,
          installed_at TEXT,
          updated_at TEXT NOT NULL,
          last_log_path TEXT
        );

        CREATE TABLE IF NOT EXISTS runtime_component_state (
          platform_id TEXT NOT NULL,
          component TEXT NOT NULL,
          source TEXT NOT NULL,
          availability TEXT NOT NULL DEFAULT 'unavailable',
          active_generation_id TEXT,
          artifact_version TEXT,
          resolved_path TEXT,
          operation_kind TEXT NOT NULL DEFAULT 'idle',
          operation_generation INTEGER NOT NULL DEFAULT 0,
          phase TEXT NOT NULL DEFAULT 'idle',
          progress INTEGER,
          last_error TEXT,
          updated_at TEXT NOT NULL,
          PRIMARY KEY(platform_id, component, source)
        );

        CREATE TABLE IF NOT EXISTS jobs (
          id TEXT PRIMARY KEY,
          title TEXT NOT NULL,
          created_at TEXT NOT NULL,
          duration_minutes INTEGER NOT NULL DEFAULT 0,
          lang TEXT NOT NULL,
          enable_speaker INTEGER NOT NULL DEFAULT 1,
          summary_template TEXT NOT NULL,
         upload_status TEXT NOT NULL,
         asr_status TEXT NOT NULL,
         summary_status TEXT NOT NULL,
         overall_status TEXT NOT NULL,
          processing_started_at_ms INTEGER,
          processing_finished_at_ms INTEGER,
          processing_duration_seconds INTEGER,
          failure_reason TEXT,
          process_log TEXT,
          python_path TEXT,
          runner_script_path TEXT,
          active_summary_run_id TEXT,
          last_exported_at TEXT,
          hotwords_json TEXT NOT NULL,
          export_formats_json TEXT NOT NULL
        );

        CREATE TABLE IF NOT EXISTS job_source_files (
          id TEXT PRIMARY KEY,
          job_id TEXT NOT NULL,
          name TEXT NOT NULL,
          path TEXT,
          size_label TEXT NOT NULL,
          kind TEXT NOT NULL,
          FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE
        );

        CREATE TABLE IF NOT EXISTS transcript_segments (
          id TEXT PRIMARY KEY,
          job_id TEXT NOT NULL,
          segment_type TEXT NOT NULL,
          start_ms INTEGER NOT NULL,
          end_ms INTEGER NOT NULL,
          speaker TEXT,
          text TEXT NOT NULL,
          segment_order INTEGER NOT NULL,
          FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE
        );

        CREATE TABLE IF NOT EXISTS ai_model_configs (
          id TEXT PRIMARY KEY,
          name TEXT NOT NULL,
          base_url TEXT NOT NULL,
          api_key TEXT NOT NULL,
          api_key_ref TEXT NOT NULL DEFAULT '',
          model TEXT NOT NULL,
          enabled INTEGER NOT NULL DEFAULT 1,
          is_default INTEGER NOT NULL DEFAULT 0,
          created_at TEXT NOT NULL,
          updated_at TEXT NOT NULL
        );

        CREATE TABLE IF NOT EXISTS ai_summary_templates (
          id TEXT PRIMARY KEY,
          name TEXT NOT NULL,
          description TEXT NOT NULL,
          prompt TEXT NOT NULL,
          include_speaker_by_default INTEGER NOT NULL DEFAULT 1,
          include_timestamp_by_default INTEGER NOT NULL DEFAULT 1,
          builtin INTEGER NOT NULL DEFAULT 0,
          created_at TEXT NOT NULL,
          updated_at TEXT NOT NULL
        );

        CREATE TABLE IF NOT EXISTS ai_summary_runs (
          id TEXT PRIMARY KEY,
          job_id TEXT NOT NULL,
          model_config_id TEXT,
          template_id TEXT,
          include_speaker INTEGER NOT NULL DEFAULT 1,
          include_timestamp INTEGER NOT NULL DEFAULT 1,
          extra_instructions TEXT NOT NULL DEFAULT '',
          status TEXT NOT NULL,
          error_message TEXT,
          prompt_preview TEXT,
          raw_response TEXT,
          result_json TEXT,
          minutes_payload_json TEXT,
          created_at TEXT NOT NULL,
          updated_at TEXT NOT NULL,
          FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE
        );

        CREATE TABLE IF NOT EXISTS job_events (
          id TEXT PRIMARY KEY,
          job_id TEXT NOT NULL,
          event_type TEXT NOT NULL,
          message TEXT NOT NULL DEFAULT '',
          metadata_json TEXT,
          created_at TEXT NOT NULL,
          FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE
        );

        CREATE TABLE IF NOT EXISTS meeting_members (
          id TEXT PRIMARY KEY,
          name TEXT NOT NULL,
          department TEXT NOT NULL DEFAULT '',
          sort_order INTEGER NOT NULL DEFAULT 0,
          is_recorder INTEGER NOT NULL DEFAULT 0,
          created_at TEXT NOT NULL,
          updated_at TEXT NOT NULL
        );

        CREATE TABLE IF NOT EXISTS pet_profile (
          id TEXT PRIMARY KEY,
          name TEXT NOT NULL,
          level INTEGER NOT NULL DEFAULT 1,
          experience INTEGER NOT NULL DEFAULT 0,
          stage TEXT NOT NULL,
          current_mood TEXT NOT NULL,
          created_at TEXT NOT NULL,
          updated_at TEXT NOT NULL
        );

        CREATE TABLE IF NOT EXISTS pet_settings (
          pet_id TEXT PRIMARY KEY,
          desktop_enabled INTEGER NOT NULL DEFAULT 1,
          always_on_top INTEGER NOT NULL DEFAULT 1,
          muted INTEGER NOT NULL DEFAULT 0,
          focus_mode_enabled INTEGER NOT NULL DEFAULT 0,
          proactive_level INTEGER NOT NULL DEFAULT 2,
          last_window_x REAL,
          last_window_y REAL,
          updated_at TEXT NOT NULL,
          FOREIGN KEY(pet_id) REFERENCES pet_profile(id) ON DELETE CASCADE
        );

        CREATE TABLE IF NOT EXISTS pet_cosmetic_unlocks (
          id TEXT PRIMARY KEY,
          pet_id TEXT NOT NULL,
          cosmetic_type TEXT NOT NULL,
          cosmetic_key TEXT NOT NULL,
          unlocked_at TEXT NOT NULL,
          equipped INTEGER NOT NULL DEFAULT 0,
          FOREIGN KEY(pet_id) REFERENCES pet_profile(id) ON DELETE CASCADE
        );

        CREATE TABLE IF NOT EXISTS pet_event_ledger (
          id TEXT PRIMARY KEY,
          pet_id TEXT NOT NULL,
          event_type TEXT NOT NULL,
          event_source TEXT NOT NULL,
          event_value INTEGER NOT NULL DEFAULT 0,
          event_time TEXT NOT NULL,
          metadata TEXT,
          FOREIGN KEY(pet_id) REFERENCES pet_profile(id) ON DELETE CASCADE
        );

        CREATE TABLE IF NOT EXISTS pet_wallets (
          pet_id TEXT NOT NULL,
          currency_key TEXT NOT NULL,
          balance INTEGER NOT NULL DEFAULT 0,
          lifetime_earned INTEGER NOT NULL DEFAULT 0,
          lifetime_spent INTEGER NOT NULL DEFAULT 0,
          updated_at TEXT NOT NULL,
          PRIMARY KEY(pet_id, currency_key),
          FOREIGN KEY(pet_id) REFERENCES pet_profile(id) ON DELETE CASCADE
        );

        CREATE TABLE IF NOT EXISTS pet_inventory (
          id TEXT PRIMARY KEY,
          pet_id TEXT NOT NULL,
          item_key TEXT NOT NULL,
          item_type TEXT NOT NULL,
          slot TEXT NOT NULL,
          quantity INTEGER NOT NULL DEFAULT 1,
          equipped INTEGER NOT NULL DEFAULT 0,
          source TEXT NOT NULL,
          purchased_at TEXT NOT NULL,
          updated_at TEXT NOT NULL,
          UNIQUE(pet_id, item_key),
          FOREIGN KEY(pet_id) REFERENCES pet_profile(id) ON DELETE CASCADE
        );

        CREATE TABLE IF NOT EXISTS pet_economy_ledger (
          id TEXT PRIMARY KEY,
          pet_id TEXT NOT NULL,
          entry_type TEXT NOT NULL,
          currency_key TEXT NOT NULL,
          amount INTEGER NOT NULL,
          balance_after INTEGER NOT NULL,
          source_type TEXT NOT NULL,
          source_key TEXT NOT NULL,
          metadata TEXT,
          created_at TEXT NOT NULL,
          UNIQUE(pet_id, currency_key, source_type, source_key),
          FOREIGN KEY(pet_id) REFERENCES pet_profile(id) ON DELETE CASCADE
        );

        CREATE TABLE IF NOT EXISTS pet_redeem_key_redemptions (
          id TEXT PRIMARY KEY,
          pet_id TEXT NOT NULL,
          key_hash TEXT NOT NULL,
          code_prefix TEXT NOT NULL,
          campaign_id TEXT NOT NULL,
          reward_json TEXT NOT NULL,
          status TEXT NOT NULL,
          redeemed_at TEXT NOT NULL,
          metadata TEXT,
          UNIQUE(pet_id, key_hash),
          FOREIGN KEY(pet_id) REFERENCES pet_profile(id) ON DELETE CASCADE
        );

        CREATE TABLE IF NOT EXISTS pet_blind_box_draws (
          id TEXT PRIMARY KEY,
          pet_id TEXT NOT NULL,
          draw_date TEXT NOT NULL,
          item_key TEXT NOT NULL,
          item_type TEXT NOT NULL,
          quantity INTEGER NOT NULL DEFAULT 1,
          duplicate_compensation_lp INTEGER NOT NULL DEFAULT 0,
          created_at TEXT NOT NULL,
          FOREIGN KEY(pet_id) REFERENCES pet_profile(id) ON DELETE CASCADE
        );

        CREATE TABLE IF NOT EXISTS pet_daily_check_ins (
          id TEXT PRIMARY KEY,
          pet_id TEXT NOT NULL,
          check_in_date TEXT NOT NULL,
          streak_count INTEGER NOT NULL DEFAULT 1,
          cycle_day INTEGER NOT NULL DEFAULT 1,
          reward_lp INTEGER NOT NULL DEFAULT 0,
          growth_value INTEGER NOT NULL DEFAULT 0,
          reward_items_json TEXT NOT NULL DEFAULT '[]',
          created_at TEXT NOT NULL,
          UNIQUE(pet_id, check_in_date),
          FOREIGN KEY(pet_id) REFERENCES pet_profile(id) ON DELETE CASCADE
        );

        CREATE TABLE IF NOT EXISTS pet_store_daily_limits (
          pet_id TEXT NOT NULL,
          item_key TEXT NOT NULL,
          limit_date TEXT NOT NULL,
          free_claimed INTEGER NOT NULL DEFAULT 0,
          updated_at TEXT NOT NULL,
          PRIMARY KEY(pet_id, item_key, limit_date),
          FOREIGN KEY(pet_id) REFERENCES pet_profile(id) ON DELETE CASCADE
        );

        CREATE TABLE IF NOT EXISTS pet_milestone_counters (
          pet_id TEXT NOT NULL,
          counter_key TEXT NOT NULL,
          counter_value INTEGER NOT NULL DEFAULT 0,
          last_event_key TEXT NOT NULL DEFAULT '',
          updated_at TEXT NOT NULL,
          PRIMARY KEY(pet_id, counter_key),
          FOREIGN KEY(pet_id) REFERENCES pet_profile(id) ON DELETE CASCADE
        );

        CREATE TABLE IF NOT EXISTS farm_plots (
          id TEXT PRIMARY KEY,
          plot_index INTEGER NOT NULL UNIQUE,
          crop_key TEXT NOT NULL DEFAULT '',
          status TEXT NOT NULL DEFAULT 'empty',
          stage_index INTEGER NOT NULL DEFAULT 0,
          planted_at TEXT,
          last_watered_at TEXT,
          next_care_at TEXT,
          mature_at TEXT,
          updated_at TEXT NOT NULL
        );

        CREATE TABLE IF NOT EXISTS farm_harvest_ledger (
          id TEXT PRIMARY KEY,
          plot_id TEXT NOT NULL,
          crop_key TEXT NOT NULL,
          rewards_json TEXT NOT NULL,
          lp_reward INTEGER NOT NULL DEFAULT 0,
          created_at TEXT NOT NULL
        );

        CREATE TABLE IF NOT EXISTS work_game_tasks (
          id TEXT PRIMARY KEY,
          game_key TEXT NOT NULL,
          slot_index INTEGER NOT NULL,
          job_key TEXT NOT NULL DEFAULT '',
          status TEXT NOT NULL DEFAULT 'idle',
          stage_index INTEGER NOT NULL DEFAULT 0,
          started_at TEXT,
          last_cared_at TEXT,
          next_care_at TEXT,
          claimable_at TEXT,
          updated_at TEXT NOT NULL,
          UNIQUE(game_key, slot_index)
        );

        CREATE TABLE IF NOT EXISTS work_game_reward_ledger (
          id TEXT PRIMARY KEY,
          game_key TEXT NOT NULL,
          task_id TEXT NOT NULL,
          job_key TEXT NOT NULL,
          rewards_json TEXT NOT NULL,
          lp_reward INTEGER NOT NULL DEFAULT 0,
          created_at TEXT NOT NULL
        );

        CREATE INDEX IF NOT EXISTS idx_jobs_created_at ON jobs(created_at DESC);
        CREATE INDEX IF NOT EXISTS idx_job_source_files_job_id ON job_source_files(job_id);
        CREATE INDEX IF NOT EXISTS idx_segments_job_id ON transcript_segments(job_id, segment_type, segment_order);
        CREATE INDEX IF NOT EXISTS idx_ai_runs_job_id ON ai_summary_runs(job_id, updated_at DESC);
        CREATE INDEX IF NOT EXISTS idx_job_events_job_id ON job_events(job_id, created_at ASC);
        CREATE INDEX IF NOT EXISTS idx_meeting_members_sort_order ON meeting_members(sort_order ASC, updated_at DESC);
        CREATE INDEX IF NOT EXISTS idx_pet_event_ledger_time ON pet_event_ledger(event_time DESC);
        CREATE INDEX IF NOT EXISTS idx_pet_cosmetic_unlocks_pet_id ON pet_cosmetic_unlocks(pet_id, unlocked_at DESC);
        CREATE INDEX IF NOT EXISTS idx_pet_inventory_pet_id ON pet_inventory(pet_id, updated_at DESC);
        CREATE INDEX IF NOT EXISTS idx_pet_economy_pet_id ON pet_economy_ledger(pet_id, created_at DESC);
        CREATE INDEX IF NOT EXISTS idx_pet_redeem_key_pet_time ON pet_redeem_key_redemptions(pet_id, redeemed_at DESC);
        CREATE INDEX IF NOT EXISTS idx_pet_blind_box_draws_pet_date ON pet_blind_box_draws(pet_id, draw_date, created_at DESC);
        CREATE INDEX IF NOT EXISTS idx_pet_daily_check_ins_pet_date ON pet_daily_check_ins(pet_id, check_in_date DESC);
        CREATE INDEX IF NOT EXISTS idx_pet_store_daily_limits_pet_date ON pet_store_daily_limits(pet_id, limit_date DESC);
        CREATE INDEX IF NOT EXISTS idx_pet_milestones_pet_id ON pet_milestone_counters(pet_id, counter_key);
        CREATE INDEX IF NOT EXISTS idx_farm_plots_status ON farm_plots(status, next_care_at);
        CREATE INDEX IF NOT EXISTS idx_farm_harvest_created_at ON farm_harvest_ledger(created_at DESC);
        CREATE INDEX IF NOT EXISTS idx_work_game_tasks_status ON work_game_tasks(game_key, status, next_care_at, claimable_at);
        CREATE INDEX IF NOT EXISTS idx_work_game_rewards_created_at ON work_game_reward_ledger(game_key, created_at DESC);
        CREATE INDEX IF NOT EXISTS idx_runtime_state_status ON runtime_state(status);
        CREATE INDEX IF NOT EXISTS idx_runtime_component_operation ON runtime_component_state(operation_kind, component);
