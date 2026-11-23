use rusqlite::{params, Connection, OptionalExtension, Transaction};

use crate::infrastructure::{
    credentials::{default_credential_store, CredentialStore},
    migrations::{self, Migration},
    repositories::ai_summary_runs,
};
use crate::local_db::{pet_leveling, AiSummaryTemplate, LocalResult};

const MIGRATIONS: &[Migration] = &[
    Migration::new(1, "baseline schema", migrate_v1_baseline_schema),
    Migration::new(2, "versioned columns and settings", migrate_v2_settings),
    Migration::credentials(3, "system credentials"),
    Migration::new(4, "settings revisions", migrate_v4_settings_revisions),
    Migration::new(5, "local job run leases", migrate_v5_job_runs),
    Migration::new(6, "farm and work game tables", migrate_v6_work_tables),
    Migration::new(
        7,
        "persistent AI summary runs",
        ai_summary_runs::migrate_v7_ai_summary_runs,
    ),
    Migration::new(8, "persistent job deletion log", migrate_v8_job_deletions),
    Migration::new(9, "runner protocol metadata", migrate_v9_runner_protocol),
    Migration::new(
        10,
        "credential cleanup intents",
        migrate_v10_credential_cleanup_intents,
    ),
];

pub(crate) fn apply_schema(conn: &Connection) -> LocalResult<()> {
    apply_schema_with_credentials(conn, &default_credential_store())
}

#[cfg(test)]
pub(crate) fn apply_test_schema(conn: &Connection) -> LocalResult<()> {
    apply_schema(conn)
}

fn apply_schema_with_credentials(
    conn: &Connection,
    credential_store: &dyn CredentialStore,
) -> LocalResult<()> {
    migrations::run_migrations(conn, credential_store, MIGRATIONS).map(|_| ())
}

fn migrate_v1_baseline_schema(transaction: &Transaction<'_>) -> LocalResult<()> {
    apply_baseline_schema(transaction)
}

fn apply_baseline_schema(conn: &Connection) -> LocalResult<()> {
    conn.execute_batch(include_str!(
        "../../../../../packages/desktop-resources/database/v01-baseline.sql"
    ))
    .map_err(|err| err.to_string())
}

fn migrate_v2_settings(transaction: &Transaction<'_>) -> LocalResult<()> {
    apply_baseline_schema(transaction)?;

    let [columns @ .., update_processing_mode] = migrations::sql_blocks::<16>(include_str!(
        "../../../../../packages/desktop-resources/database/v02-settings.sql"
    ))?;
    let targets: [(&str, &str); 15] = [
        ("jobs", "active_summary_run_id"),
        ("jobs", "processing_started_at_ms"),
        ("jobs", "processing_finished_at_ms"),
        ("jobs", "processing_duration_seconds"),
        ("app_settings", "local_asr_device"),
        ("app_settings", "local_asr_threads"),
        ("app_settings", "local_asr_batch_size_seconds"),
        ("app_settings", "runtime_download_source"),
        ("app_settings", "ffmpeg_path"),
        ("app_settings", "python_runtime_source"),
        ("app_settings", "ffmpeg_runtime_source"),
        ("app_settings", "api_token_ref"),
        ("app_settings", "processing_mode"),
        ("ai_model_configs", "api_key_ref"),
        ("ai_summary_runs", "minutes_payload_json"),
    ];
    for ((table, column), statement) in targets.into_iter().zip(columns) {
        migrations::add_column_if_missing(transaction, table, column, statement)?;
    }

    transaction
        .execute(update_processing_mode, [])
        .map_err(|err| err.to_string())?;

    migrate_runtime_source_settings(transaction)?;
    migrate_pet_leveling_255(transaction)?;

    Ok(())
}

fn migrate_v4_settings_revisions(transaction: &Transaction<'_>) -> LocalResult<()> {
    migrations::add_column_if_missing(
        transaction,
        "app_settings",
        "settings_revision",
        include_str!(
            "../../../../../packages/desktop-resources/database/v04-settings-revisions.sql"
        ),
    )
}

fn migrate_v5_job_runs(transaction: &Transaction<'_>) -> LocalResult<()> {
    let [create, backfill] = migrations::sql_blocks(include_str!(
        "../../../../../packages/desktop-resources/database/v05-job-runs.sql"
    ))?;
    transaction
        .execute_batch(create)
        .map_err(|err| err.to_string())?;
    transaction
        .execute(backfill, [])
        .map_err(|err| err.to_string())?;
    Ok(())
}

fn migrate_v6_work_tables(transaction: &Transaction<'_>) -> LocalResult<()> {
    transaction
        .execute_batch(include_str!(
            "../../../../../packages/desktop-resources/database/v06-work-tables.sql"
        ))
        .map_err(|error| error.to_string())
}

fn migrate_v8_job_deletions(transaction: &Transaction<'_>) -> LocalResult<()> {
    transaction
        .execute_batch(include_str!(
            "../../../../../packages/desktop-resources/database/v08-job-deletions.sql"
        ))
        .map_err(|err| err.to_string())
}

fn migrate_v9_runner_protocol(transaction: &Transaction<'_>) -> LocalResult<()> {
    transaction
        .execute_batch(include_str!(
            "../../../../../packages/desktop-resources/database/v09-runner-protocol.sql"
        ))
        .map_err(|err| err.to_string())
}

fn migrate_v10_credential_cleanup_intents(transaction: &Transaction<'_>) -> LocalResult<()> {
    transaction
        .execute_batch(include_str!(
            "../../../../../packages/desktop-resources/database/v10-credential-cleanup-intents.sql"
        ))
        .map_err(|error| error.to_string())
}

fn migrate_runtime_source_settings(transaction: &Transaction<'_>) -> LocalResult<()> {
    let migrated = transaction
        .query_row(
            "SELECT value FROM app_meta WHERE key = 'runtime_sources_migrated'",
            [],
            |row| row.get::<_, String>(0),
        )
        .optional()
        .map_err(|err| err.to_string())?;
    if migrated.is_some() {
        return Ok(());
    }

    let [update_sources, record_migration] = migrations::sql_blocks(include_str!(
        "../../../../../packages/desktop-resources/database/v02-runtime-sources.sql"
    ))?;
    transaction
        .execute(update_sources, [])
        .map_err(|err| err.to_string())?;
    transaction
        .execute(record_migration, [])
        .map_err(|err| err.to_string())?;
    Ok(())
}

fn migrate_pet_leveling_255(transaction: &Transaction<'_>) -> LocalResult<()> {
    let migrated = transaction
        .query_row(
            "SELECT value FROM app_meta WHERE key = 'pet_leveling_255_migrated'",
            [],
            |row| row.get::<_, String>(0),
        )
        .optional()
        .map_err(|err| err.to_string())?;
    if migrated.is_some() {
        return Ok(());
    }

    let profile = transaction
        .query_row(
            "SELECT level, experience FROM pet_profile WHERE id = 'default-pet'",
            [],
            |row| Ok((row.get::<_, i64>(0)?, row.get::<_, i64>(1)?)),
        )
        .optional()
        .map_err(|err| err.to_string())?;

    if let Some((stored_level, experience)) = profile {
        let snapshot = pet_leveling::level_snapshot_from_experience(experience);
        let effective_level = stored_level
            .max(snapshot.level)
            .clamp(1, pet_leveling::MAX_PET_LEVEL);
        let effective_experience = if effective_level > snapshot.level {
            pet_leveling::total_required_exp_for_level(effective_level)
        } else {
            experience
        };
        let next_snapshot = pet_leveling::level_snapshot_from_experience(effective_experience);
        transaction
            .execute(
                "UPDATE pet_profile
             SET level = ?2, experience = ?3, stage = ?4, updated_at = ?5
             WHERE id = ?1",
                params![
                    "default-pet",
                    next_snapshot.level,
                    effective_experience,
                    next_snapshot.current_stage,
                    crate::infrastructure::time::utc_now()
                ],
            )
            .map_err(|err| err.to_string())?;
    }

    transaction
        .execute(
            "INSERT OR REPLACE INTO app_meta(key, value) VALUES('pet_leveling_255_migrated', ?1)",
            params!["2026-05-21"],
        )
        .map_err(|err| err.to_string())?;

    Ok(())
}

pub(crate) fn seed_builtin_templates(conn: &Connection) -> LocalResult<()> {
    let templates: Vec<AiSummaryTemplate> = serde_json::from_str(include_str!(
        "../../../../../packages/desktop-resources/database/builtin-summary-templates.json"
    ))
    .map_err(|_| "无法读取内置 AI 总结模板。".to_string())?;

    for template in templates {
        save_ai_template_inner(conn, &template)?;
    }

    Ok(())
}

fn save_ai_template_inner(conn: &Connection, template: &AiSummaryTemplate) -> LocalResult<()> {
    conn.execute(
        "INSERT OR REPLACE INTO ai_summary_templates (
            id, name, description, prompt, include_speaker_by_default,
            include_timestamp_by_default, builtin, created_at, updated_at
         ) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9)",
        params![
            template.id,
            template.name,
            template.description,
            template.prompt,
            if template.include_speaker_by_default {
                1
            } else {
                0
            },
            if template.include_timestamp_by_default {
                1
            } else {
                0
            },
            if template.builtin { 1 } else { 0 },
            template.created_at,
            template.updated_at
        ],
    )
    .map_err(|err| err.to_string())?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use std::{
        cell::{Cell, RefCell},
        collections::HashMap,
        fs,
        sync::atomic::{AtomicU64, Ordering},
    };

    use super::{apply_schema_with_credentials, MIGRATIONS};
    use crate::{
        domain::error::AppError,
        infrastructure::{
            credentials::{CredentialResult, CredentialStore},
            migrations::{self, CURRENT_SCHEMA_VERSION},
        },
    };
    use rusqlite::{params, Connection, OptionalExtension};

    static TEST_FILE_SEQUENCE: AtomicU64 = AtomicU64::new(0);

    #[test]
    #[ignore = "explicit cross-language migration fixtures via pnpm native:compat"]
    fn export_native_migration_fixtures() {
        let directory = std::path::PathBuf::from(
            std::env::var_os("LIBERTY_NATIVE_FIXTURE_DIR").expect("explicit fixture directory"),
        );
        assert!(directory.is_absolute() && directory.is_dir());
        for target in 0..=10 {
            let path = directory.join(format!("migration-v{target}.sqlite3"));
            fs::OpenOptions::new()
                .write(true)
                .create_new(true)
                .open(&path)
                .unwrap();
            let conn = Connection::open(&path).unwrap();
            let vault = TestCredentialStore::default();
            if target > 0 {
                populate_legacy_v1_database(&conn);
                super::apply_baseline_schema(&conn).unwrap();
                conn.execute_batch(
                    "INSERT INTO pet_profile(id,name,level,experience,stage,current_mood,created_at,updated_at)
                     VALUES('default-pet','Synthetic companion',61,5,'baby','happy','fixture','fixture');
                     INSERT INTO jobs(id,title,created_at,lang,enable_speaker,summary_template,upload_status,asr_status,summary_status,overall_status,hotwords_json,export_formats_json)
                     VALUES('completed','Synthetic completed','fixture','zh',1,'','completed','completed','completed','completed','[]','[]'),
                           ('queued','Synthetic queued','fixture','en',0,'','completed','queued','idle','queued','[]','[]'),
                           ('running','Synthetic running','fixture','zh',1,'','completed','transcribing','idle','transcribing','[]','[]'),
                           ('failed','Synthetic failed','fixture','zh',1,'','completed','failed','idle','failed','[]','[]');
                     INSERT INTO transcript_segments(id,job_id,segment_type,start_ms,end_ms,speaker,text,segment_order)
                     VALUES('segment','completed','transcript',0,1234,NULL,'Synthetic transcript',0);
                     INSERT INTO ai_summary_runs(id,job_id,status,result_json,created_at,updated_at)
                     VALUES('summary-completed','completed','completed','{}','fixture','fixture'),
                           ('summary-running','running','running',NULL,'fixture','fixture');"
                ).unwrap();
                for step in MIGRATIONS.iter().take(target).skip(1) {
                    migrations::run_test_migration(&conn, &vault, step).unwrap();
                }
            }
            let expected_path = directory.join(format!("migration-v{target}-expected.sqlite3"));
            fs::OpenOptions::new()
                .write(true)
                .create_new(true)
                .open(&expected_path)
                .unwrap();
            conn.backup(rusqlite::MAIN_DB, &expected_path, None::<fn(_)>)
                .unwrap();
            let expected = Connection::open(&expected_path).unwrap();
            super::apply_schema_with_credentials(&expected, &vault).unwrap();
            super::seed_builtin_templates(&expected).unwrap();
        }
    }

    #[derive(Default)]
    struct TestCredentialStore {
        secrets: RefCell<HashMap<String, String>>,
        fail_key: RefCell<Option<String>>,
        writes: Cell<usize>,
    }

    impl CredentialStore for TestCredentialStore {
        fn get_secret(&self, key: &str) -> CredentialResult<Option<String>> {
            Ok(self.secrets.borrow().get(key).cloned())
        }

        fn set_secret(&self, key: &str, value: &str) -> CredentialResult<()> {
            if self
                .fail_key
                .borrow()
                .as_deref()
                .is_some_and(|prefix| key.starts_with(prefix))
            {
                return Err(AppError::Infrastructure(
                    "injected credential failure".into(),
                ));
            }
            self.writes.set(self.writes.get() + 1);
            self.secrets
                .borrow_mut()
                .insert(key.to_string(), value.to_string());
            Ok(())
        }

        fn delete_secret(&self, key: &str) -> CredentialResult<()> {
            self.secrets.borrow_mut().remove(key);
            Ok(())
        }
    }

    #[test]
    fn rejects_future_database_before_business_ddl() {
        let conn = Connection::open_in_memory().expect("database");
        conn.execute_batch(
            "CREATE TABLE app_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
             INSERT INTO app_meta(key, value) VALUES('schema_version', '99');",
        )
        .expect("future metadata");

        let error = apply_schema_with_credentials(&conn, &TestCredentialStore::default())
            .expect_err("future database must fail");
        assert!(error.contains("高于当前应用支持的版本"));
        let app_settings_exists = conn
            .query_row(
                "SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'app_settings'",
                [],
                |_| Ok(()),
            )
            .optional()
            .expect("inspect schema")
            .is_some();
        assert!(!app_settings_exists);
    }

    #[test]
    fn upgrades_v1_database_and_preserves_legacy_remote_behavior() {
        let conn = legacy_v1_database();
        let store = TestCredentialStore::default();

        apply_schema_with_credentials(&conn, &store).expect("upgrade legacy database");

        assert_eq!(schema_version(&conn), CURRENT_SCHEMA_VERSION);
        let settings = conn
            .query_row(
                "SELECT processing_mode, api_token, api_token_ref,
                        python_runtime_source, ffmpeg_runtime_source
                 FROM app_settings WHERE id = 1",
                [],
                |row| {
                    Ok((
                        row.get::<_, String>(0)?,
                        row.get::<_, String>(1)?,
                        row.get::<_, String>(2)?,
                        row.get::<_, String>(3)?,
                        row.get::<_, String>(4)?,
                    ))
                },
            )
            .expect("migrated settings");
        assert_eq!(settings.0, "remote");
        assert!(settings.1.is_empty());
        assert!(settings
            .2
            .starts_with("settings:remote-api-token:migration:write-"));
        assert_eq!(settings.3, "system");
        assert_eq!(settings.4, "managed");
        assert_eq!(
            store.get_secret(&settings.2).expect("remote credential"),
            Some("legacy-token".into())
        );
        let model_credential_reference = conn
            .query_row(
                "SELECT api_key_ref FROM ai_model_configs WHERE id = 'model-a'",
                [],
                |row| row.get::<_, String>(0),
            )
            .expect("model credential reference");
        assert!(model_credential_reference.starts_with("ai-model:model-a:api-key:migration:write-"));
        assert_eq!(
            store
                .get_secret(&model_credential_reference)
                .expect("model credential"),
            Some("legacy-key".into())
        );
    }

    #[test]
    fn migrates_real_database_file_and_keeps_pre_migration_backup() {
        let root = std::env::temp_dir().join(format!(
            "liberty-schema-migration-{}-{}",
            std::process::id(),
            TEST_FILE_SEQUENCE.fetch_add(1, Ordering::Relaxed)
        ));
        fs::create_dir_all(&root).expect("migration temp directory");
        let database_path = root.join("liberty.sqlite3");
        let conn = Connection::open(&database_path).expect("file database");
        populate_legacy_v1_database(&conn);

        apply_schema_with_credentials(&conn, &TestCredentialStore::default())
            .expect("migrate file database");
        assert_eq!(schema_version(&conn), CURRENT_SCHEMA_VERSION);

        let backups = fs::read_dir(&root)
            .expect("list migration backups")
            .map(|entry| entry.expect("backup entry").path())
            .filter(|path| {
                path.file_name()
                    .and_then(|name| name.to_str())
                    .is_some_and(|name| {
                        name.starts_with("liberty.sqlite3.pre-migration-v1-")
                            && name.ends_with(".bak")
                    })
            })
            .collect::<Vec<_>>();
        assert_eq!(backups.len(), 1);
        let backup = Connection::open(&backups[0]).expect("open migration backup");
        assert_eq!(schema_version(&backup), 1);
        let legacy_token = backup
            .query_row(
                "SELECT api_token FROM app_settings WHERE id = 1",
                [],
                |row| row.get::<_, String>(0),
            )
            .expect("legacy token in backup");
        assert_eq!(legacy_token, "legacy-token");

        drop(backup);
        drop(conn);
        fs::remove_dir_all(root).expect("remove migration temp directory");
    }

    #[test]
    fn repeated_startup_does_not_reapply_migrations() {
        let conn = legacy_v1_database();
        let store = TestCredentialStore::default();
        apply_schema_with_credentials(&conn, &store).expect("first startup");
        conn.execute(
            "UPDATE app_settings SET processing_mode = 'local' WHERE id = 1",
            [],
        )
        .expect("change explicit mode");
        let writes_after_first_startup = store.writes.get();

        apply_schema_with_credentials(&conn, &store).expect("second startup");

        let mode = conn
            .query_row(
                "SELECT processing_mode FROM app_settings WHERE id = 1",
                [],
                |row| row.get::<_, String>(0),
            )
            .expect("processing mode");
        assert_eq!(mode, "local");
        assert_eq!(store.writes.get(), writes_after_first_startup);
    }

    #[test]
    fn credential_failure_keeps_plaintext_and_version_retriable() {
        let conn = legacy_v1_database();
        let failing_store = TestCredentialStore::default();
        *failing_store.fail_key.borrow_mut() = Some("ai-model:model-a:api-key".into());

        let error = migrations::run_migrations(&conn, &failing_store, MIGRATIONS)
            .expect_err("credential migration must fail");

        assert!(error.contains("数据库明文未清除"));
        assert_eq!(schema_version(&conn), 2);
        let plaintext = conn
            .query_row(
                "SELECT api_token, api_token_ref FROM app_settings WHERE id = 1",
                [],
                |row| Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?)),
            )
            .expect("plaintext settings");
        assert_eq!(plaintext, ("legacy-token".into(), String::new()));
        let model_plaintext = conn
            .query_row(
                "SELECT api_key, api_key_ref FROM ai_model_configs WHERE id = 'model-a'",
                [],
                |row| Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?)),
            )
            .expect("plaintext model");
        assert_eq!(model_plaintext, ("legacy-key".into(), String::new()));

        apply_schema_with_credentials(&conn, &TestCredentialStore::default())
            .expect("retry credential migration");
        assert_eq!(schema_version(&conn), CURRENT_SCHEMA_VERSION);
    }

    #[test]
    fn v5_backfills_only_recoverable_job_runs() {
        let conn = Connection::open_in_memory().expect("database");
        conn.execute_batch(
            "PRAGMA foreign_keys = ON;
             CREATE TABLE app_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
             INSERT INTO app_meta(key, value) VALUES('schema_version', '4');
             CREATE TABLE jobs (
               id TEXT PRIMARY KEY,
               enable_speaker INTEGER NOT NULL DEFAULT 1,
               asr_status TEXT NOT NULL DEFAULT 'queued',
               overall_status TEXT NOT NULL,
               processing_started_at_ms INTEGER,
               summary_status TEXT NOT NULL DEFAULT 'idle'
             );
             INSERT INTO jobs(id, overall_status, processing_started_at_ms) VALUES
               ('job-queued', 'queued', NULL),
               ('job-running', 'transcribing', 42),
               ('job-completed', 'completed', 10);",
        )
        .expect("v4 fixture");

        apply_schema_with_credentials(&conn, &TestCredentialStore::default())
            .expect("upgrade to v5");

        let rows = {
            let mut statement = conn
                .prepare(
                    "SELECT job_id, attempt_id, lease_token, status, output_dir
                     FROM job_runs ORDER BY job_id",
                )
                .expect("job runs query");
            statement
                .query_map([], |row| {
                    Ok((
                        row.get::<_, String>(0)?,
                        row.get::<_, i64>(1)?,
                        row.get::<_, i64>(2)?,
                        row.get::<_, String>(3)?,
                        row.get::<_, Option<String>>(4)?,
                    ))
                })
                .expect("job run rows")
                .collect::<Result<Vec<_>, _>>()
                .expect("collect job runs")
        };
        assert_eq!(
            rows,
            vec![
                ("job-queued".into(), 0, 0, "queued".into(), None),
                (
                    "job-running".into(),
                    1,
                    1,
                    "running".into(),
                    Some("attempts/attempt-1-1".into())
                ),
            ]
        );
        assert_eq!(schema_version(&conn), CURRENT_SCHEMA_VERSION);
    }

    #[test]
    fn v9_maps_legacy_diarization_without_changing_segments() {
        let conn = Connection::open_in_memory().expect("database");
        conn.execute_batch(
            "CREATE TABLE app_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
             INSERT INTO app_meta(key, value) VALUES('schema_version', '8');
             CREATE TABLE jobs (
               id TEXT PRIMARY KEY,
               enable_speaker INTEGER NOT NULL,
               asr_status TEXT NOT NULL,
               overall_status TEXT NOT NULL
             );
             CREATE TABLE transcript_segments (
               id TEXT PRIMARY KEY,
               job_id TEXT NOT NULL,
               segment_type TEXT NOT NULL
             );
             INSERT INTO jobs VALUES
               ('disabled', 0, 'completed', 'completed'),
               ('completed', 1, 'completed', 'completed'),
               ('running', 1, 'transcribing', 'transcribing'),
               ('failed', 1, 'failed', 'failed');
             INSERT INTO transcript_segments VALUES
               ('t1', 'completed', 'transcript'),
               ('t2', 'completed', 'transcript'),
               ('s1', 'completed', 'speaker'),
               ('s2', 'completed', 'speaker');",
        )
        .expect("v8 fixture");
        let before: i64 = conn
            .query_row("SELECT COUNT(*) FROM transcript_segments", [], |row| {
                row.get(0)
            })
            .expect("segment count");

        migrations::run_migrations(&conn, &TestCredentialStore::default(), MIGRATIONS)
            .expect("v9 migration");
        migrations::run_migrations(&conn, &TestCredentialStore::default(), MIGRATIONS)
            .expect("idempotent startup");

        let statuses = {
            let mut statement = conn
                .prepare("SELECT id, diarization_status FROM jobs ORDER BY id")
                .expect("status query");
            statement
                .query_map([], |row| {
                    Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?))
                })
                .expect("status rows")
                .collect::<Result<Vec<_>, _>>()
                .expect("collect statuses")
        };
        assert_eq!(
            statuses,
            vec![
                ("completed".into(), "legacy_unverified".into()),
                ("disabled".into(), "disabled".into()),
                ("failed".into(), "failed".into()),
                ("running".into(), "pending".into()),
            ]
        );
        let after: i64 = conn
            .query_row("SELECT COUNT(*) FROM transcript_segments", [], |row| {
                row.get(0)
            })
            .expect("segment count after migration");
        assert_eq!(before, after);
        assert_eq!(schema_version(&conn), CURRENT_SCHEMA_VERSION);
        let metadata = conn
            .query_row(
                "SELECT runner_protocol_version, asr_backend, warnings_json
                 FROM jobs WHERE id = 'completed'",
                [],
                |row| {
                    Ok((
                        row.get::<_, Option<i64>>(0)?,
                        row.get::<_, String>(1)?,
                        row.get::<_, String>(2)?,
                    ))
                },
            )
            .expect("metadata defaults");
        assert_eq!(metadata, (None, "unknown".into(), "[]".into()));
    }

    fn legacy_v1_database() -> Connection {
        let conn = Connection::open_in_memory().expect("database");
        populate_legacy_v1_database(&conn);
        conn
    }

    fn populate_legacy_v1_database(conn: &Connection) {
        conn.execute_batch(
            "CREATE TABLE app_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
             INSERT INTO app_meta(key, value) VALUES('schema_version', '1');
             CREATE TABLE app_settings (
                id INTEGER PRIMARY KEY CHECK (id = 1),
                theme_mode TEXT NOT NULL,
                liquid_glass_style TEXT NOT NULL,
                accent_color TEXT NOT NULL,
                locale TEXT NOT NULL,
                backend_url TEXT NOT NULL,
                api_token TEXT NOT NULL,
                default_hotwords TEXT NOT NULL,
                summary_template TEXT NOT NULL,
                concurrency INTEGER NOT NULL,
                python_path TEXT NOT NULL,
                runner_script_path TEXT NOT NULL
             );
             CREATE TABLE ai_model_configs (
                id TEXT PRIMARY KEY,
                name TEXT NOT NULL,
                base_url TEXT NOT NULL,
                api_key TEXT NOT NULL,
                model TEXT NOT NULL,
                enabled INTEGER NOT NULL DEFAULT 1,
                is_default INTEGER NOT NULL DEFAULT 0,
                created_at TEXT NOT NULL,
                updated_at TEXT NOT NULL
             );",
        )
        .expect("legacy schema");
        conn.execute(
            "INSERT INTO app_settings (
                id, theme_mode, liquid_glass_style, accent_color, locale,
                backend_url, api_token, default_hotwords, summary_template,
                concurrency, python_path, runner_script_path
             ) VALUES (1, 'auto', 'transparent', '#2f6dff', 'zh-CN', ?1, ?2, '', '', 2, '/custom/python', '')",
            params!["https://legacy.example.com", "legacy-token"],
        )
        .expect("legacy settings");
        conn.execute(
            "INSERT INTO ai_model_configs (
                id, name, base_url, api_key, model, enabled, is_default, created_at, updated_at
             ) VALUES ('model-a', 'Model A', 'https://ai.example.com', 'legacy-key', 'example', 1, 1, 'now', 'now')",
            [],
        )
        .expect("legacy model");
    }

    fn schema_version(conn: &Connection) -> i64 {
        conn.query_row(
            "SELECT value FROM app_meta WHERE key = 'schema_version'",
            [],
            |row| row.get::<_, String>(0),
        )
        .expect("schema version")
        .parse()
        .expect("numeric schema version")
    }
}
