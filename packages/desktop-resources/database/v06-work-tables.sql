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
             CREATE INDEX IF NOT EXISTS idx_farm_plots_status
               ON farm_plots(status, next_care_at);
             CREATE INDEX IF NOT EXISTS idx_farm_harvest_created_at
               ON farm_harvest_ledger(created_at DESC);
             CREATE INDEX IF NOT EXISTS idx_work_game_tasks_status
               ON work_game_tasks(game_key, status, next_care_at, claimable_at);
             CREATE INDEX IF NOT EXISTS idx_work_game_rewards_created_at
               ON work_game_reward_ledger(game_key, created_at DESC);
