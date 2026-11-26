use std::{fs, path::PathBuf};

use rusqlite::{params, Connection, OptionalExtension, Transaction, TransactionBehavior};
use serde::{Deserialize, Serialize};

use crate::infrastructure::{
    credentials::{
        credential_key_for_ai_model, credential_key_for_remote_api_token, CredentialStore,
    },
    ids,
    repositories::credential_cleanup,
    restore_credentials,
};
use crate::local_db::LocalResult;

pub const CURRENT_SCHEMA_VERSION: i64 = 12;
const CREDENTIAL_JOURNAL_KEY: &str = "native_credential_migration_v3";
const CREDENTIAL_JOURNAL_ERROR: &str = "无法恢复系统凭据迁移记录。";

pub type MigrationFn = for<'transaction> fn(&Transaction<'transaction>) -> LocalResult<()>;

#[derive(Clone, Copy)]
enum MigrationKind {
    Sql(MigrationFn),
    Credentials,
}

#[derive(Clone, Copy)]
pub struct Migration {
    pub version: i64,
    pub name: &'static str,
    kind: MigrationKind,
}

impl Migration {
    pub const fn new(version: i64, name: &'static str, apply: MigrationFn) -> Self {
        Self {
            version,
            name,
            kind: MigrationKind::Sql(apply),
        }
    }

    pub const fn credentials(version: i64, name: &'static str) -> Self {
        Self {
            version,
            name,
            kind: MigrationKind::Credentials,
        }
    }
}

pub fn ensure_schema_version(conn: &Connection) -> LocalResult<i64> {
    let version = read_schema_version(conn)?;
    reject_unsupported_version(version)?;
    Ok(version)
}

pub fn run_migrations(
    conn: &Connection,
    credential_store: &dyn CredentialStore,
    migrations: &[Migration],
) -> LocalResult<i64> {
    let mut version = ensure_schema_version(conn)?;
    validate_migration_sequence(migrations)?;
    let restore_credentials = restore_credentials::read(conn)?;
    if version < CURRENT_SCHEMA_VERSION {
        create_consistent_backup(conn)?;
        version = ensure_schema_version(conn)?;
    }
    recover_credential_migration(conn, credential_store)?;

    while version < CURRENT_SCHEMA_VERSION {
        let migration = &migrations[version as usize];
        if migration.version != version + 1 {
            return Err(format!(
                "数据库迁移不连续: 当前版本 {version}，下一迁移版本 {}。",
                migration.version
            ));
        }

        version = match migration.kind {
            MigrationKind::Sql(apply) => run_sql_migration(conn, migration, apply)?,
            MigrationKind::Credentials => {
                run_credential_migration(conn, credential_store, migration)?
            }
        };
    }

    if version != CURRENT_SCHEMA_VERSION {
        return Err(format!(
            "数据库迁移未完成: 当前版本 {version}，应用需要版本 {CURRENT_SCHEMA_VERSION}。"
        ));
    }

    if let Some(records) = restore_credentials {
        records.recover(conn)?;
    }
    Ok(version)
}

fn run_sql_migration(
    conn: &Connection,
    migration: &Migration,
    apply: MigrationFn,
) -> LocalResult<i64> {
    let transaction = exclusive_transaction(conn, migration)?;
    let version = ensure_schema_version(&transaction)?;
    if version >= migration.version {
        return Ok(version);
    }
    ensure_next_version(version, migration)?;
    apply(&transaction).map_err(|error| migration_error(migration, error))?;
    record_schema_version(&transaction, migration)?;
    transaction.commit().map_err(|error| {
        format!(
            "无法提交数据库迁移 v{} ({}): {error}",
            migration.version, migration.name
        )
    })?;
    Ok(migration.version)
}

#[cfg(test)]
pub(crate) fn run_test_migration(
    conn: &Connection,
    credential_store: &dyn CredentialStore,
    migration: &Migration,
) -> LocalResult<i64> {
    match migration.kind {
        MigrationKind::Sql(apply) => run_sql_migration(conn, migration, apply),
        MigrationKind::Credentials => run_credential_migration(conn, credential_store, migration),
    }
}

fn run_credential_migration(
    conn: &Connection,
    credential_store: &dyn CredentialStore,
    migration: &Migration,
) -> LocalResult<i64> {
    recover_credential_migration(conn, credential_store)?;
    ensure_credential_journal_durability(conn)?;
    let plan = {
        let transaction = exclusive_transaction(conn, migration)?;
        let version = ensure_schema_version(&transaction)?;
        if version >= migration.version {
            return Ok(version);
        }
        ensure_next_version(version, migration)?;
        let plan = CredentialMigrationPlan::read(&transaction)?;
        let mut journal = read_credential_journal(&transaction)?.unwrap_or_default();
        journal
            .entries
            .extend(plan.entries.iter().map(|entry| CredentialJournalEntry {
                reference: entry.staged_reference.clone(),
                previous: entry.previous_reference.clone(),
            }));
        save_credential_journal(&transaction, &journal)?;
        transaction
            .commit()
            .map_err(|_| CREDENTIAL_JOURNAL_ERROR.to_string())?;
        plan
    };
    if let Err(error) = plan.stage(credential_store) {
        return Err(credential_migration_failure(conn, credential_store, error));
    }
    let outcome = match publish_credential_migration(conn, migration, &plan) {
        Ok(outcome) => outcome,
        Err(error) => {
            return Err(credential_migration_failure(conn, credential_store, error));
        }
    };
    recover_credential_migration(conn, credential_store)?;
    match outcome {
        PublishOutcome::Applied => Ok(migration.version),
        PublishOutcome::AlreadyApplied(version) => Ok(version),
    }
}

fn credential_migration_failure(
    conn: &Connection,
    credential_store: &dyn CredentialStore,
    error: String,
) -> String {
    match recover_credential_migration(conn, credential_store) {
        Ok(()) => error,
        Err(cleanup_error) => format!("{error}；{cleanup_error}"),
    }
}

fn publish_credential_migration(
    conn: &Connection,
    migration: &Migration,
    plan: &CredentialMigrationPlan,
) -> LocalResult<PublishOutcome> {
    let transaction = exclusive_transaction(conn, migration)?;
    let version = ensure_schema_version(&transaction)?;
    if version >= migration.version {
        return Ok(PublishOutcome::AlreadyApplied(version));
    }
    ensure_next_version(version, migration)?;
    plan.apply(&transaction)
        .map_err(|error| migration_error(migration, error))?;
    let mut journal = read_credential_journal(&transaction)?
        .ok_or_else(|| CREDENTIAL_JOURNAL_ERROR.to_string())?;
    if !plan.entries.iter().all(|entry| {
        journal.entries.iter().any(|record| {
            record.reference == entry.staged_reference
                && record.previous == entry.previous_reference
        })
    }) {
        return Err(CREDENTIAL_JOURNAL_ERROR.to_string());
    }
    journal.published = true;
    save_credential_journal(&transaction, &journal)?;
    record_schema_version(&transaction, migration)?;
    transaction.commit().map_err(|error| {
        format!(
            "无法提交数据库迁移 v{} ({}): {error}",
            migration.version, migration.name
        )
    })?;
    Ok(PublishOutcome::Applied)
}

fn exclusive_transaction<'connection>(
    conn: &'connection Connection,
    migration: &Migration,
) -> LocalResult<Transaction<'connection>> {
    Transaction::new_unchecked(conn, TransactionBehavior::Exclusive).map_err(|error| {
        format!(
            "无法获取数据库迁移排他锁 v{} ({}): {error}",
            migration.version, migration.name
        )
    })
}

fn ensure_next_version(version: i64, migration: &Migration) -> LocalResult<()> {
    if migration.version == version + 1 {
        Ok(())
    } else {
        Err(format!(
            "数据库迁移不连续: 当前版本 {version}，下一迁移版本 {}。",
            migration.version
        ))
    }
}

fn record_schema_version(transaction: &Transaction<'_>, migration: &Migration) -> LocalResult<()> {
    transaction
        .execute(
            "INSERT INTO app_meta(key, value) VALUES('schema_version', ?1)
             ON CONFLICT(key) DO UPDATE SET value = excluded.value",
            params![migration.version.to_string()],
        )
        .map_err(|error| {
            format!(
                "无法记录数据库迁移 v{} ({}): {error}",
                migration.version, migration.name
            )
        })?;
    Ok(())
}

fn migration_error(migration: &Migration, error: String) -> String {
    format!(
        "数据库迁移 v{} ({}) 失败: {error}",
        migration.version, migration.name
    )
}

fn create_consistent_backup(conn: &Connection) -> LocalResult<Option<PathBuf>> {
    let version = ensure_schema_version(conn)?;
    if version == 0 || version >= CURRENT_SCHEMA_VERSION {
        return Ok(None);
    }

    let Some(backup_path) = migration_backup_path(conn, version) else {
        // In-memory databases are used by isolated tests and have no durable
        // source file to preserve. Production databases always have a path.
        return Ok(None);
    };
    // SQLite's backup API owns the source read snapshot. Starting it from a
    // write transaction on the same connection can wait on its own lock.
    if let Err(error) = conn.backup(rusqlite::MAIN_DB, &backup_path, None::<fn(_)>) {
        let _ = fs::remove_file(&backup_path);
        return Err(format!(
            "无法创建数据库迁移前备份 {}: {error}",
            backup_path.display()
        ));
    }
    Ok(Some(backup_path))
}

fn migration_backup_path(conn: &Connection, version: i64) -> Option<PathBuf> {
    if version <= 0 {
        return None;
    }
    let source = PathBuf::from(conn.path()?.trim());
    if source.as_os_str().is_empty() {
        return None;
    }
    let file_name = source.file_name()?.to_string_lossy();
    Some(source.with_file_name(format!(
        "{file_name}.pre-migration-v{version}-{}.bak",
        ids::timestamped_id("snapshot")
    )))
}

#[derive(Clone, Copy)]
enum PublishOutcome {
    Applied,
    AlreadyApplied(i64),
}

pub fn add_column_if_missing(
    conn: &Connection,
    table: &str,
    column: &str,
    statement: &str,
) -> LocalResult<()> {
    if table_has_column(conn, table, column)? {
        return Ok(());
    }

    conn.execute(statement, []).map_err(|err| err.to_string())?;
    Ok(())
}

pub(crate) fn sql_blocks<const COUNT: usize>(
    source: &'static str,
) -> LocalResult<[&'static str; COUNT]> {
    let blocks = source
        .split("-- next block")
        .map(str::trim)
        .collect::<Vec<_>>();
    if blocks.iter().any(|block| block.is_empty()) {
        return Err("共享数据库迁移包含空 SQL 块。".into());
    }
    blocks
        .try_into()
        .map_err(|_| format!("共享数据库迁移需要 {COUNT} 个 SQL 块。"))
}

struct CredentialMigrationPlan {
    entries: Vec<CredentialMigrationEntry>,
}

struct CredentialMigrationEntry {
    target: CredentialTarget,
    plaintext: String,
    previous_reference: String,
    staged_reference: String,
}

enum CredentialTarget {
    Settings,
    AiModel(String),
}

impl CredentialMigrationPlan {
    fn read(conn: &Connection) -> LocalResult<Self> {
        let mut entries = Vec::new();
        if let Some((plaintext, previous_reference)) = conn
            .query_row(
                "SELECT api_token, COALESCE(api_token_ref, '')
                 FROM app_settings WHERE id = 1",
                [],
                |row| Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?)),
            )
            .optional()
            .map_err(|error| error.to_string())?
        {
            if !plaintext.trim().is_empty() {
                entries.push(CredentialMigrationEntry::new(
                    CredentialTarget::Settings,
                    plaintext,
                    previous_reference,
                    credential_key_for_remote_api_token(),
                ));
            }
        }

        let model_credentials = {
            let mut statement = conn
                .prepare(
                    "SELECT id, api_key, COALESCE(api_key_ref, '')
                     FROM ai_model_configs
                     WHERE TRIM(api_key) <> ''
                     ORDER BY id",
                )
                .map_err(|error| error.to_string())?;
            let credentials = statement
                .query_map([], |row| {
                    Ok((
                        row.get::<_, String>(0)?,
                        row.get::<_, String>(1)?,
                        row.get::<_, String>(2)?,
                    ))
                })
                .map_err(|error| error.to_string())?
                .collect::<Result<Vec<_>, _>>()
                .map_err(|error| error.to_string())?;
            credentials
        };
        for (model_id, plaintext, previous_reference) in model_credentials {
            if plaintext.trim().is_empty() {
                continue;
            }
            entries.push(CredentialMigrationEntry::new(
                CredentialTarget::AiModel(model_id.clone()),
                plaintext,
                previous_reference,
                &credential_key_for_ai_model(&model_id),
            ));
        }
        Ok(Self { entries })
    }

    fn stage(&self, credential_store: &dyn CredentialStore) -> LocalResult<()> {
        for entry in &self.entries {
            let plaintext = entry.plaintext.trim();
            if credential_store
                .set_secret(&entry.staged_reference, plaintext)
                .is_err()
                || !credential_store
                    .get_secret(&entry.staged_reference)
                    .is_ok_and(|stored| stored.as_deref() == Some(plaintext))
            {
                return Err(format!(
                    "无法把{}迁移到系统凭据库；数据库明文未清除，可修复凭据库后重试。",
                    entry.label()
                ));
            }
        }
        Ok(())
    }
}

impl CredentialMigrationEntry {
    fn new(
        target: CredentialTarget,
        plaintext: String,
        previous_reference: String,
        base_reference: &str,
    ) -> Self {
        Self {
            target,
            plaintext,
            previous_reference,
            staged_reference: format!(
                "{base_reference}:migration:{}",
                ids::timestamped_id("write")
            ),
        }
    }

    fn label(&self) -> String {
        match &self.target {
            CredentialTarget::Settings => "远端 API token".into(),
            CredentialTarget::AiModel(model_id) => format!("AI 模型 {model_id} API key"),
        }
    }
}

impl CredentialMigrationPlan {
    fn apply(&self, transaction: &Transaction<'_>) -> LocalResult<()> {
        for entry in &self.entries {
            let updated = match &entry.target {
                CredentialTarget::Settings => transaction.execute(
                    "UPDATE app_settings
                     SET api_token = '', api_token_ref = ?1
                     WHERE id = 1 AND api_token = ?2
                       AND COALESCE(api_token_ref, '') = ?3",
                    params![
                        entry.staged_reference,
                        entry.plaintext,
                        entry.previous_reference
                    ],
                ),
                CredentialTarget::AiModel(model_id) => transaction.execute(
                    "UPDATE ai_model_configs
                     SET api_key = '', api_key_ref = ?1
                     WHERE id = ?2 AND api_key = ?3
                       AND COALESCE(api_key_ref, '') = ?4",
                    params![
                        entry.staged_reference,
                        model_id,
                        entry.plaintext,
                        entry.previous_reference
                    ],
                ),
            }
            .map_err(|error| error.to_string())?;
            if updated != 1 {
                return Err(format!(
                    "{}在凭据暂存期间发生变化，迁移已取消。",
                    entry.label()
                ));
            }
        }
        Ok(())
    }
}

#[derive(Default, Deserialize, Serialize)]
struct CredentialMigrationJournal {
    published: bool,
    #[serde(deserialize_with = "deserialize_journal_entries")]
    entries: Vec<CredentialJournalEntry>,
}

#[derive(Deserialize, Serialize)]
struct CredentialJournalEntry {
    reference: String,
    previous: String,
}

fn deserialize_journal_entries<'de, Deserializer>(
    deserializer: Deserializer,
) -> Result<Vec<CredentialJournalEntry>, Deserializer::Error>
where
    Deserializer: serde::Deserializer<'de>,
{
    Ok(Option::<Vec<CredentialJournalEntry>>::deserialize(deserializer)?.unwrap_or_default())
}

fn read_credential_journal(conn: &Connection) -> LocalResult<Option<CredentialMigrationJournal>> {
    let failed = || CREDENTIAL_JOURNAL_ERROR.to_string();
    let has_metadata = conn
        .query_row(
            "SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'app_meta')",
            [],
            |row| row.get::<_, bool>(0),
        )
        .map_err(|_| failed())?;
    if !has_metadata {
        return Ok(None);
    }
    let text = conn
        .query_row(
            "SELECT value FROM app_meta WHERE key = ?1",
            params![CREDENTIAL_JOURNAL_KEY],
            |row| row.get::<_, String>(0),
        )
        .optional()
        .map_err(|_| failed())?;
    let Some(text) = text else {
        return Ok(None);
    };
    let journal: CredentialMigrationJournal = serde_json::from_str(&text).map_err(|_| failed())?;
    if journal
        .entries
        .iter()
        .any(|entry| entry.reference.trim().is_empty())
    {
        return Err(failed());
    }
    Ok(Some(journal))
}

fn save_credential_journal(
    conn: &Connection,
    journal: &CredentialMigrationJournal,
) -> LocalResult<()> {
    let failed = || CREDENTIAL_JOURNAL_ERROR.to_string();
    let text = serde_json::to_string(journal).map_err(|_| failed())?;
    conn.execute(
        "INSERT INTO app_meta(key, value) VALUES(?1, ?2)
         ON CONFLICT(key) DO UPDATE SET value = excluded.value",
        params![CREDENTIAL_JOURNAL_KEY, text],
    )
    .map_err(|_| failed())?;
    Ok(())
}

fn ensure_credential_journal_durability(conn: &Connection) -> LocalResult<()> {
    conn.pragma_update(None, "synchronous", "FULL")
        .map_err(|_| CREDENTIAL_JOURNAL_ERROR.to_string())
}

fn migration_reference_is_live(conn: &Connection, reference: &str) -> LocalResult<bool> {
    let failed = || CREDENTIAL_JOURNAL_ERROR.to_string();
    let version = ensure_schema_version(conn).map_err(|_| failed())?;
    let mut referenced = false;
    for (table, column, reference_sql) in [
        (
            "ai_model_configs",
            "api_key_ref",
            "SELECT EXISTS (SELECT 1 FROM ai_model_configs WHERE api_key_ref = ?1)",
        ),
        (
            "app_settings",
            "api_token_ref",
            "SELECT EXISTS (SELECT 1 FROM app_settings WHERE api_token_ref = ?1)",
        ),
    ] {
        if !table_has_column(conn, table, column).map_err(|_| failed())? {
            if version >= 3 {
                return Err(failed());
            }
            continue;
        }
        referenced |= conn
            .query_row(reference_sql, params![reference], |row| {
                row.get::<_, bool>(0)
            })
            .map_err(|_| failed())?;
    }
    Ok(referenced)
}

fn recover_credential_migration(
    conn: &Connection,
    credential_store: &dyn CredentialStore,
) -> LocalResult<()> {
    let Some(journal) = read_credential_journal(conn)? else {
        return Ok(());
    };
    ensure_credential_journal_durability(conn)?;
    let mut retained = false;
    for entry in &journal.entries {
        let references = [
            entry.reference.as_str(),
            if journal.published {
                entry.previous.as_str()
            } else {
                ""
            },
        ];
        for reference in references {
            if reference.is_empty() || migration_reference_is_live(conn, reference)? {
                continue;
            }
            if credential_cleanup::backup_is_referenced(conn, reference)? {
                retained = true;
                continue;
            }
            credential_store
                .delete_secret(reference)
                .map_err(|_| "系统凭据迁移清理仍有待重试项。".to_string())?;
        }
    }
    if !retained {
        conn.execute(
            "DELETE FROM app_meta WHERE key = ?1",
            params![CREDENTIAL_JOURNAL_KEY],
        )
        .map_err(|_| CREDENTIAL_JOURNAL_ERROR.to_string())?;
    }
    Ok(())
}

fn read_schema_version(conn: &Connection) -> LocalResult<i64> {
    let has_metadata_table = conn
        .query_row(
            "SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'app_meta'",
            [],
            |_| Ok(()),
        )
        .optional()
        .map_err(|err| format!("无法检查数据库元数据表: {err}"))?
        .is_some();
    if !has_metadata_table {
        return Ok(0);
    }

    let stored_version = conn
        .query_row(
            "SELECT value FROM app_meta WHERE key = 'schema_version'",
            [],
            |row| row.get::<_, String>(0),
        )
        .optional()
        .map_err(|err| format!("无法读取数据库 schema_version: {err}"))?;
    let Some(stored_version) = stored_version else {
        return Ok(0);
    };

    let version = stored_version
        .parse::<i64>()
        .map_err(|err| format!("数据库 schema_version 无效: {err}"))?;
    if version < 0 {
        return Err(format!("数据库 schema_version 无效: {version}"));
    }
    Ok(version)
}

fn reject_unsupported_version(version: i64) -> LocalResult<()> {
    if version > CURRENT_SCHEMA_VERSION {
        return Err(format!(
            "数据库版本 {version} 高于当前应用支持的版本 {CURRENT_SCHEMA_VERSION}。"
        ));
    }
    Ok(())
}

fn validate_migration_sequence(migrations: &[Migration]) -> LocalResult<()> {
    if migrations.len() != CURRENT_SCHEMA_VERSION as usize {
        return Err(format!(
            "数据库迁移列表不完整: 需要 {CURRENT_SCHEMA_VERSION} 个版本，实际为 {} 个。",
            migrations.len()
        ));
    }

    for (index, migration) in migrations.iter().enumerate() {
        let expected_version = index as i64 + 1;
        if migration.version != expected_version {
            return Err(format!(
                "数据库迁移列表顺序无效: 期望 v{expected_version}，实际为 v{}。",
                migration.version
            ));
        }
    }
    Ok(())
}

fn table_has_column(conn: &Connection, table: &str, column: &str) -> LocalResult<bool> {
    if !table
        .chars()
        .all(|character| character.is_ascii_alphanumeric() || character == '_')
    {
        return Err(format!("数据库表名无效: {table}"));
    }

    let mut statement = conn
        .prepare(&format!("PRAGMA table_info({table})"))
        .map_err(|err| err.to_string())?;
    let columns = statement
        .query_map([], |row| row.get::<_, String>(1))
        .map_err(|err| err.to_string())?;
    for existing_column in columns {
        if existing_column.map_err(|err| err.to_string())? == column {
            return Ok(true);
        }
    }
    Ok(false)
}

#[cfg(test)]
mod tests {
    use std::{
        cell::{Cell, RefCell},
        collections::HashMap,
    };

    use super::*;
    use crate::{domain::error::AppError, infrastructure::credentials::CredentialResult};

    const TEST_SCHEMA: &str = "
        CREATE TABLE app_meta(key TEXT PRIMARY KEY, value TEXT NOT NULL);
        CREATE TABLE app_settings(id INTEGER PRIMARY KEY, api_token TEXT NOT NULL, api_token_ref TEXT);
        CREATE TABLE ai_model_configs(id TEXT PRIMARY KEY, api_key TEXT NOT NULL, api_key_ref TEXT);";
    const REMOTE_MATERIAL: &str = " synthetic-remote-material ";
    const MODEL_MATERIAL: &str = "\tsynthetic-model-material\n";

    fn test_baseline(transaction: &Transaction<'_>) -> LocalResult<()> {
        transaction
            .execute_batch(TEST_SCHEMA)
            .map_err(|error| error.to_string())
    }

    fn test_noop(_transaction: &Transaction<'_>) -> LocalResult<()> {
        Ok(())
    }

    const TEST_MIGRATIONS: &[Migration] = &[
        Migration::new(1, "test baseline", test_baseline),
        Migration::new(2, "test v2", test_noop),
        Migration::credentials(3, "system credentials"),
        Migration::new(4, "test v4", test_noop),
        Migration::new(5, "test v5", test_noop),
        Migration::new(6, "test v6", test_noop),
        Migration::new(7, "test v7", test_noop),
        Migration::new(8, "test v8", test_noop),
        Migration::new(9, "test v9", test_noop),
        Migration::new(10, "test v10", test_noop),
        Migration::new(11, "test v11", test_noop),
        Migration::new(12, "test v12", test_noop),
    ];

    struct TestDirectory(PathBuf);

    impl Drop for TestDirectory {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    struct TestProfile {
        connection: Connection,
        directory: TestDirectory,
    }

    impl TestProfile {
        fn empty() -> Self {
            let directory = std::env::temp_dir().join(format!(
                "liberty-migration-journal-{}-{}",
                std::process::id(),
                ids::timestamped_id("test")
            ));
            fs::create_dir(&directory).unwrap();
            let directory = TestDirectory(directory);
            let connection = Connection::open(directory.0.join("synthetic #资料.sqlite3")).unwrap();
            Self {
                connection,
                directory,
            }
        }

        fn legacy() -> Self {
            let profile = Self::empty();
            profile.connection.execute_batch(TEST_SCHEMA).unwrap();
            profile
                .connection
                .execute("INSERT INTO app_meta VALUES ('schema_version', '2')", [])
                .unwrap();
            profile
                .connection
                .execute(
                    "INSERT INTO app_settings VALUES (1, ?1, 'synthetic-remote-before')",
                    [REMOTE_MATERIAL],
                )
                .unwrap();
            profile
                .connection
                .execute(
                    "INSERT INTO ai_model_configs VALUES ('model-a', ?1, 'synthetic-model-before')",
                    [MODEL_MATERIAL],
                )
                .unwrap();
            profile
        }

        fn path(&self) -> PathBuf {
            self.directory.0.join("synthetic #资料.sqlite3")
        }

        fn reopen(&mut self) {
            let old =
                std::mem::replace(&mut self.connection, Connection::open_in_memory().unwrap());
            drop(old);
            self.connection = Connection::open(self.path()).unwrap();
        }

        fn backups(&self) -> Vec<PathBuf> {
            fs::read_dir(&self.directory.0)
                .unwrap()
                .map(|entry| entry.unwrap().path())
                .filter(|path| {
                    let filename = path.file_name().unwrap().to_string_lossy();
                    filename.starts_with("synthetic #资料.sqlite3.pre-")
                        && filename.ends_with(".bak")
                })
                .collect()
        }

        fn remove_backups(&self) {
            for backup in self.backups() {
                fs::remove_file(backup).unwrap();
            }
        }
    }

    struct TestCredentialStore {
        path: PathBuf,
        secrets: RefCell<HashMap<String, String>>,
        writes: RefCell<Vec<String>>,
        deletes: RefCell<Vec<String>>,
        fail_set_at: Cell<Option<usize>>,
        omit_write: Cell<bool>,
        fail_read: Cell<bool>,
        fail_delete: Cell<bool>,
    }

    impl TestCredentialStore {
        fn new(profile: &TestProfile) -> Self {
            Self {
                path: profile.path(),
                secrets: RefCell::new(HashMap::from([
                    (
                        "synthetic-remote-before".into(),
                        "synthetic-previous-remote".into(),
                    ),
                    (
                        "synthetic-model-before".into(),
                        "synthetic-previous-model".into(),
                    ),
                ])),
                writes: RefCell::new(Vec::new()),
                deletes: RefCell::new(Vec::new()),
                fail_set_at: Cell::new(None),
                omit_write: Cell::new(false),
                fail_read: Cell::new(false),
                fail_delete: Cell::new(false),
            }
        }
    }

    impl CredentialStore for TestCredentialStore {
        fn get_secret(&self, key: &str) -> CredentialResult<Option<String>> {
            if self.fail_read.get() {
                return Err(AppError::Infrastructure("synthetic-read-failure".into()));
            }
            Ok(self.secrets.borrow().get(key).cloned())
        }

        fn set_secret(&self, key: &str, value: &str) -> CredentialResult<()> {
            let connection = Connection::open(&self.path).unwrap();
            let journal = read_credential_journal(&connection).unwrap().unwrap();
            assert!(journal.entries.len() >= 2);
            assert!(journal.entries.iter().any(|entry| entry.reference == key));
            let text = serde_json::to_string(&journal).unwrap();
            assert!(!text.contains(value));
            self.writes.borrow_mut().push(key.to_string());
            if !self.omit_write.get() {
                self.secrets
                    .borrow_mut()
                    .insert(key.to_string(), value.to_string());
            }
            if self.fail_set_at.get() == Some(self.writes.borrow().len()) {
                return Err(AppError::Infrastructure(
                    "synthetic-partial-set-failure".into(),
                ));
            }
            Ok(())
        }

        fn delete_secret(&self, key: &str) -> CredentialResult<()> {
            self.deletes.borrow_mut().push(key.to_string());
            if self.fail_delete.get() {
                return Err(AppError::Infrastructure("synthetic-delete-failure".into()));
            }
            self.secrets.borrow_mut().remove(key);
            Ok(())
        }
    }

    fn assert_legacy_unpublished(connection: &Connection) {
        assert_eq!(ensure_schema_version(connection).unwrap(), 2);
        let token = connection
            .query_row(
                "SELECT api_token, api_token_ref FROM app_settings WHERE id = 1",
                [],
                |row| Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?)),
            )
            .unwrap();
        assert_eq!(
            token,
            (
                REMOTE_MATERIAL.to_string(),
                "synthetic-remote-before".to_string()
            )
        );
        let model = connection
            .query_row(
                "SELECT api_key, api_key_ref FROM ai_model_configs WHERE id = 'model-a'",
                [],
                |row| Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?)),
            )
            .unwrap();
        assert_eq!(
            model,
            (
                MODEL_MATERIAL.to_string(),
                "synthetic-model-before".to_string()
            )
        );
    }

    fn assert_published_material(connection: &Connection, store: &TestCredentialStore) {
        let mut statement = connection
            .prepare("SELECT api_token, api_token_ref FROM app_settings UNION ALL SELECT api_key, api_key_ref FROM ai_model_configs")
            .unwrap();
        let values = statement
            .query_map([], |row| {
                Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?))
            })
            .unwrap()
            .collect::<Result<Vec<_>, _>>()
            .unwrap();
        assert_eq!(values.len(), 2);
        for ((plaintext, reference), expected) in
            values.into_iter().zip([REMOTE_MATERIAL, MODEL_MATERIAL])
        {
            assert!(plaintext.is_empty());
            assert_eq!(
                store.secrets.borrow().get(&reference).map(String::as_str),
                Some(expected.trim())
            );
        }
    }

    #[test]
    fn migration_backup_keeps_previous_credentials_until_removed_after_reopen() {
        let mut profile = TestProfile::legacy();
        let store = TestCredentialStore::new(&profile);

        assert_eq!(
            run_migrations(&profile.connection, &store, TEST_MIGRATIONS).unwrap(),
            CURRENT_SCHEMA_VERSION
        );

        assert_published_material(&profile.connection, &store);
        let journal = read_credential_journal(&profile.connection)
            .unwrap()
            .unwrap();
        assert!(journal.published);
        assert_eq!(journal.entries.len(), 2);
        assert!(store.deletes.borrow().is_empty());
        let text = serde_json::to_value(&journal).unwrap();
        assert_eq!(text.as_object().unwrap().len(), 2);
        for entry in text["entries"].as_array().unwrap() {
            assert_eq!(entry.as_object().unwrap().len(), 2);
            assert!(entry["reference"].is_string());
            assert!(entry["previous"].is_string());
        }
        assert_eq!(profile.backups().len(), 1);
        let backup = Connection::open(&profile.backups()[0]).unwrap();
        assert_legacy_unpublished(&backup);
        drop(backup);

        profile.reopen();
        run_migrations(&profile.connection, &store, TEST_MIGRATIONS).unwrap();
        assert_eq!(store.writes.borrow().len(), 2);
        assert!(read_credential_journal(&profile.connection)
            .unwrap()
            .is_some());
        profile.remove_backups();
        profile.reopen();
        run_migrations(&profile.connection, &store, TEST_MIGRATIONS).unwrap();

        assert!(read_credential_journal(&profile.connection)
            .unwrap()
            .is_none());
        assert!(!store
            .secrets
            .borrow()
            .contains_key("synthetic-remote-before"));
        assert!(!store
            .secrets
            .borrow()
            .contains_key("synthetic-model-before"));
        assert_eq!(store.writes.borrow().len(), 2);
        assert_published_material(&profile.connection, &store);
    }

    #[test]
    fn partial_stage_and_failed_rollback_are_journaled_before_every_write() {
        let mut profile = TestProfile::legacy();
        let store = TestCredentialStore::new(&profile);
        store.fail_set_at.set(Some(2));
        store.fail_delete.set(true);

        let error = run_migrations(&profile.connection, &store, TEST_MIGRATIONS).unwrap_err();

        assert!(error.contains("数据库明文未清除"));
        assert!(error.contains("待重试项"));
        assert_legacy_unpublished(&profile.connection);
        let journal = read_credential_journal(&profile.connection)
            .unwrap()
            .unwrap();
        assert!(!journal.published);
        assert_eq!(journal.entries.len(), 2);
        let failed_references = store.writes.borrow().clone();
        assert_eq!(failed_references.len(), 2);
        for reference in &failed_references {
            assert!(journal
                .entries
                .iter()
                .any(|entry| &entry.reference == reference));
            assert!(store.secrets.borrow().contains_key(reference));
        }

        profile.reopen();
        store.fail_set_at.set(None);
        store.fail_delete.set(false);
        run_migrations(&profile.connection, &store, TEST_MIGRATIONS).unwrap();

        for reference in &failed_references {
            assert!(!store.secrets.borrow().contains_key(reference));
        }
        assert_eq!(store.writes.borrow().len(), 4);
        assert_published_material(&profile.connection, &store);
    }

    #[test]
    fn missing_or_unreadable_staged_value_never_publishes_plaintext_removal() {
        for unreadable in [false, true] {
            let mut profile = TestProfile::legacy();
            let store = TestCredentialStore::new(&profile);
            store.omit_write.set(!unreadable);
            store.fail_read.set(unreadable);
            store.fail_delete.set(true);

            assert!(run_migrations(&profile.connection, &store, TEST_MIGRATIONS).is_err());

            assert_legacy_unpublished(&profile.connection);
            let journal = read_credential_journal(&profile.connection)
                .unwrap()
                .unwrap();
            assert!(!journal.published);
            assert_eq!(journal.entries.len(), 2);
            let attempted = store.writes.borrow()[0].clone();
            profile.reopen();
            store.omit_write.set(false);
            store.fail_read.set(false);
            store.fail_delete.set(false);
            run_migrations(&profile.connection, &store, TEST_MIGRATIONS).unwrap();
            assert!(!store.secrets.borrow().contains_key(&attempted));
            assert_published_material(&profile.connection, &store);
        }
    }

    #[test]
    fn published_cleanup_failure_is_retried_from_v3_without_restaging() {
        let mut profile = TestProfile::legacy();
        let store = TestCredentialStore::new(&profile);
        store.fail_delete.set(true);

        let error =
            run_test_migration(&profile.connection, &store, &TEST_MIGRATIONS[2]).unwrap_err();

        assert!(error.contains("待重试项"));
        assert_eq!(ensure_schema_version(&profile.connection).unwrap(), 3);
        assert!(
            read_credential_journal(&profile.connection)
                .unwrap()
                .unwrap()
                .published
        );
        assert_published_material(&profile.connection, &store);
        profile.reopen();
        store.fail_delete.set(false);
        run_migrations(&profile.connection, &store, TEST_MIGRATIONS).unwrap();

        assert_eq!(
            ensure_schema_version(&profile.connection).unwrap(),
            CURRENT_SCHEMA_VERSION
        );
        assert_eq!(store.writes.borrow().len(), 2);
        assert!(read_credential_journal(&profile.connection)
            .unwrap()
            .is_none());
        assert!(!store
            .secrets
            .borrow()
            .contains_key("synthetic-remote-before"));
        assert!(!store
            .secrets
            .borrow()
            .contains_key("synthetic-model-before"));
        assert_published_material(&profile.connection, &store);
    }

    #[test]
    fn publication_and_journal_phase_rollback_in_the_same_transaction() {
        let mut profile = TestProfile::legacy();
        profile
            .connection
            .execute_batch(
                "CREATE TRIGGER reject_published_journal BEFORE UPDATE OF value ON app_meta
             WHEN NEW.key = 'native_credential_migration_v3'
              AND instr(NEW.value, '\"published\":true') > 0
             BEGIN SELECT RAISE(ABORT, 'synthetic-publication-failure'); END;",
            )
            .unwrap();
        let store = TestCredentialStore::new(&profile);
        store.fail_delete.set(true);

        assert!(run_test_migration(&profile.connection, &store, &TEST_MIGRATIONS[2]).is_err());

        assert_legacy_unpublished(&profile.connection);
        assert!(
            !read_credential_journal(&profile.connection)
                .unwrap()
                .unwrap()
                .published
        );
        let attempted = store.writes.borrow().clone();
        profile
            .connection
            .execute_batch("DROP TRIGGER reject_published_journal")
            .unwrap();
        profile.reopen();
        store.fail_delete.set(false);
        run_migrations(&profile.connection, &store, TEST_MIGRATIONS).unwrap();
        for reference in attempted {
            assert!(!store.secrets.borrow().contains_key(&reference));
        }
        assert_published_material(&profile.connection, &store);
    }

    #[test]
    fn journal_persistence_failure_prevents_all_vault_writes() {
        let profile = TestProfile::legacy();
        profile
            .connection
            .execute_batch(
                "CREATE TRIGGER reject_new_journal BEFORE INSERT ON app_meta
             WHEN NEW.key = 'native_credential_migration_v3'
             BEGIN SELECT RAISE(ABORT, 'synthetic-journal-failure'); END;",
            )
            .unwrap();
        let store = TestCredentialStore::new(&profile);

        assert!(run_test_migration(&profile.connection, &store, &TEST_MIGRATIONS[2]).is_err());

        assert_legacy_unpublished(&profile.connection);
        assert!(read_credential_journal(&profile.connection)
            .unwrap()
            .is_none());
        assert!(store.writes.borrow().is_empty());
        assert!(store.deletes.borrow().is_empty());
    }

    #[test]
    fn v0_without_metadata_and_go_null_entries_are_recoverable_without_vault_calls() {
        let mut profile = TestProfile::empty();
        let store = TestCredentialStore::new(&profile);

        assert_eq!(
            run_migrations(&profile.connection, &store, TEST_MIGRATIONS).unwrap(),
            CURRENT_SCHEMA_VERSION
        );

        profile
            .connection
            .execute(
                "INSERT INTO app_meta VALUES (?1, ?2)",
                params![
                    CREDENTIAL_JOURNAL_KEY,
                    r#"{"published":false,"entries":null}"#
                ],
            )
            .unwrap();
        profile.reopen();
        run_migrations(&profile.connection, &store, TEST_MIGRATIONS).unwrap();
        assert!(read_credential_journal(&profile.connection)
            .unwrap()
            .is_none());
        assert!(store.writes.borrow().is_empty());
        assert!(store.deletes.borrow().is_empty());
    }

    #[test]
    fn current_schema_recovers_go_journal_using_live_references_and_phase() {
        for published in [false, true] {
            let mut profile = TestProfile::legacy();
            profile
                .connection
                .execute_batch("UPDATE app_meta SET value = '10'")
                .unwrap();
            let store = TestCredentialStore::new(&profile);
            store.secrets.borrow_mut().insert(
                "liberty:migration:synthetic-unused".into(),
                "synthetic-orphan".into(),
            );
            let text = format!(
                "{{\n  \"published\": {published},\n  \"entries\": [\n    {{\"reference\": \"synthetic-model-before\", \"previous\": \"\"}},\n    {{\"reference\": \"liberty:migration:synthetic-unused\", \"previous\": \"synthetic-remote-before\"}}\n  ]\n}}"
            );
            profile
                .connection
                .execute(
                    "INSERT INTO app_meta VALUES (?1, ?2)",
                    params![CREDENTIAL_JOURNAL_KEY, text],
                )
                .unwrap();
            profile.reopen();

            run_migrations(&profile.connection, &store, TEST_MIGRATIONS).unwrap();

            assert!(read_credential_journal(&profile.connection)
                .unwrap()
                .is_none());
            assert!(store
                .secrets
                .borrow()
                .contains_key("synthetic-model-before"));
            assert!(store
                .secrets
                .borrow()
                .contains_key("synthetic-remote-before"));
            assert_eq!(
                *store.deletes.borrow(),
                ["liberty:migration:synthetic-unused"]
            );
            assert!(store.writes.borrow().is_empty());
        }
    }

    #[test]
    fn retry_merges_journal_entries_retained_by_an_earlier_backup() {
        let mut profile = TestProfile::legacy();
        let store = TestCredentialStore::new(&profile);
        store.secrets.borrow_mut().insert(
            "synthetic-earlier-staged".into(),
            "synthetic-earlier-material".into(),
        );
        save_credential_journal(
            &profile.connection,
            &CredentialMigrationJournal {
                published: false,
                entries: vec![CredentialJournalEntry {
                    reference: "synthetic-earlier-staged".into(),
                    previous: String::new(),
                }],
            },
        )
        .unwrap();
        let backup_path = profile
            .directory
            .0
            .join("synthetic #资料.sqlite3.pre-restore-v2-earlier.bak");
        profile
            .connection
            .backup(rusqlite::MAIN_DB, &backup_path, None::<fn(_)>)
            .unwrap();
        let backup = Connection::open(backup_path).unwrap();
        backup.execute_batch("UPDATE ai_model_configs SET api_key = '', api_key_ref = 'synthetic-earlier-staged'").unwrap();
        drop(backup);

        run_migrations(&profile.connection, &store, TEST_MIGRATIONS).unwrap();

        let journal = read_credential_journal(&profile.connection)
            .unwrap()
            .unwrap();
        assert!(journal.published);
        assert_eq!(journal.entries.len(), 3);
        assert!(journal
            .entries
            .iter()
            .any(|entry| entry.reference == "synthetic-earlier-staged"));
        assert!(store
            .secrets
            .borrow()
            .contains_key("synthetic-earlier-staged"));
        assert!(!store
            .deletes
            .borrow()
            .iter()
            .any(|reference| reference == "synthetic-earlier-staged"));
        profile.remove_backups();
        profile.reopen();
        run_migrations(&profile.connection, &store, TEST_MIGRATIONS).unwrap();

        assert!(read_credential_journal(&profile.connection)
            .unwrap()
            .is_none());
        assert!(!store
            .secrets
            .borrow()
            .contains_key("synthetic-earlier-staged"));
        assert_published_material(&profile.connection, &store);
    }

    #[test]
    fn invalid_journal_or_unreadable_backup_keeps_durable_recovery_state() {
        for text in [
            "not-json",
            r#"{"published":true,"entries":[{"reference":"","previous":""}]}"#,
            r#"{"published":false,"entries":[{"reference":"synthetic-unused","previous":""}]}"#,
        ] {
            let profile = TestProfile::legacy();
            profile
                .connection
                .execute_batch("UPDATE app_meta SET value = '10'")
                .unwrap();
            profile
                .connection
                .execute(
                    "INSERT INTO app_meta VALUES (?1, ?2)",
                    params![CREDENTIAL_JOURNAL_KEY, text],
                )
                .unwrap();
            fs::write(
                profile
                    .directory
                    .0
                    .join("synthetic #资料.sqlite3.pre-native-corrupt.bak"),
                b"not sqlite",
            )
            .unwrap();
            let store = TestCredentialStore::new(&profile);
            store.secrets.borrow_mut().insert(
                "synthetic-unused".into(),
                "synthetic-unused-material".into(),
            );

            assert!(run_migrations(&profile.connection, &store, TEST_MIGRATIONS).is_err());

            let stored = profile
                .connection
                .query_row(
                    "SELECT value FROM app_meta WHERE key = ?1",
                    params![CREDENTIAL_JOURNAL_KEY],
                    |row| row.get::<_, String>(0),
                )
                .unwrap();
            assert_eq!(stored, text);
            assert!(store.writes.borrow().is_empty());
            assert!(store.deletes.borrow().is_empty());
            assert!(store.secrets.borrow().contains_key("synthetic-unused"));
        }
    }

    #[test]
    fn journal_is_fully_synchronized_before_system_store_writes() {
        struct SyncCheckingStore<'connection> {
            connection: &'connection Connection,
            inner: TestCredentialStore,
        }

        impl CredentialStore for SyncCheckingStore<'_> {
            fn get_secret(&self, key: &str) -> CredentialResult<Option<String>> {
                self.inner.get_secret(key)
            }

            fn set_secret(&self, key: &str, value: &str) -> CredentialResult<()> {
                let synchronous = self
                    .connection
                    .pragma_query_value(None, "synchronous", |row| row.get::<_, i64>(0))
                    .unwrap();
                assert_eq!(synchronous, 2);
                self.inner.set_secret(key, value)
            }

            fn delete_secret(&self, key: &str) -> CredentialResult<()> {
                self.inner.delete_secret(key)
            }
        }

        let profile = TestProfile::legacy();
        profile
            .connection
            .execute_batch("PRAGMA journal_mode = WAL; PRAGMA synchronous = NORMAL;")
            .unwrap();
        let store = SyncCheckingStore {
            connection: &profile.connection,
            inner: TestCredentialStore::new(&profile),
        };

        run_migrations(&profile.connection, &store, TEST_MIGRATIONS).unwrap();

        assert_published_material(&profile.connection, &store.inner);
    }
}
