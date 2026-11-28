use std::{
    fs::{self, File, OpenOptions},
    io::Read,
    path::{Path, PathBuf},
};

use rusqlite::{params, Connection, Transaction, TransactionBehavior};
use serde::Deserialize;
use sha2::{Digest, Sha256};

use crate::{infrastructure::time::utc_now, local_db::LocalResult};

const RECOVERY_ERROR: &str = "无法恢复数据库外部凭据退休记录，已保留记录以便重试。";
const MAX_RECORD_SIZE: u64 = 64 << 20;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Journal {
    version: u32,
    database: String,
    references: Vec<String>,
}

#[derive(PartialEq, Eq)]
struct FileIdentity(u64, u64);

struct Record {
    path: PathBuf,
    identity: FileIdentity,
    data: Vec<u8>,
    journal: Journal,
}

pub(super) struct RestoreCredentials {
    database: PathBuf,
    identity: FileIdentity,
    records: Vec<Record>,
}

fn failed() -> String {
    RECOVERY_ERROR.to_string()
}

fn open_regular(path: &Path, writable: bool) -> LocalResult<(File, FileIdentity)> {
    let metadata = fs::symlink_metadata(path).map_err(|_| failed())?;
    if !metadata.is_file() {
        return Err(failed());
    }
    let mut options = OpenOptions::new();
    options.read(true).write(writable);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.custom_flags(libc::O_NOFOLLOW | libc::O_NONBLOCK);
    }
    #[cfg(windows)]
    {
        use std::os::windows::fs::{MetadataExt, OpenOptionsExt};
        if metadata.file_attributes() & 0x0000_0400 != 0 {
            return Err(failed());
        }
        options.custom_flags(0x0020_0000);
    }
    let file = options.open(path).map_err(|_| failed())?;
    if !file.metadata().map_err(|_| failed())?.is_file() {
        return Err(failed());
    }
    let identity = file_identity(&file)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        if identity != FileIdentity(metadata.dev(), metadata.ino()) {
            return Err(failed());
        }
    }
    Ok((file, identity))
}

#[cfg(unix)]
fn file_identity(file: &File) -> LocalResult<FileIdentity> {
    use std::os::unix::fs::MetadataExt;
    let metadata = file.metadata().map_err(|_| failed())?;
    if metadata.nlink() != 1 {
        return Err(failed());
    }
    Ok(FileIdentity(metadata.dev(), metadata.ino()))
}

#[cfg(windows)]
fn file_identity(file: &File) -> LocalResult<FileIdentity> {
    use std::{ffi::c_void, os::windows::io::AsRawHandle};

    #[repr(C)]
    #[derive(Default)]
    struct FileInformation {
        attributes: u32,
        creation_time: [u32; 2],
        last_access_time: [u32; 2],
        last_write_time: [u32; 2],
        volume_serial_number: u32,
        file_size_high: u32,
        file_size_low: u32,
        number_of_links: u32,
        file_index_high: u32,
        file_index_low: u32,
    }

    #[link(name = "kernel32")]
    unsafe extern "system" {
        fn GetFileInformationByHandle(handle: *mut c_void, info: *mut FileInformation) -> i32;
    }

    let mut info = FileInformation::default();
    if unsafe { GetFileInformationByHandle(file.as_raw_handle(), &mut info) } == 0
        || info.attributes & 0x0000_0400 != 0
        || info.number_of_links != 1
    {
        return Err(failed());
    }
    Ok(FileIdentity(
        u64::from(info.volume_serial_number),
        (u64::from(info.file_index_high) << 32) | u64::from(info.file_index_low),
    ))
}

#[cfg(not(any(unix, windows)))]
fn file_identity(_file: &File) -> LocalResult<FileIdentity> {
    Err(failed())
}

fn decode(data: &[u8], database: &str) -> LocalResult<Journal> {
    if data.trim_ascii_start().first() != Some(&b'{') {
        return Err(failed());
    }
    let journal: Journal = serde_json::from_slice(data).map_err(|_| failed())?;
    if journal.version != 1
        || journal.database != database
        || journal.references.iter().any(|reference| {
            reference.is_empty()
                || reference.len() >= 2048
                || reference.contains(['\0', '\r', '\n'])
        })
        || journal.references.windows(2).any(|pair| pair[0] >= pair[1])
    {
        return Err(failed());
    }
    Ok(journal)
}

fn read_record(path: PathBuf, database: &str) -> LocalResult<Record> {
    let (file, identity) = open_regular(&path, false)?;
    let length = file.metadata().map_err(|_| failed())?.len();
    if length == 0 || length > MAX_RECORD_SIZE {
        return Err(failed());
    }
    let mut data = Vec::new();
    file.take(MAX_RECORD_SIZE + 1)
        .read_to_end(&mut data)
        .map_err(|_| failed())?;
    if data.len() as u64 != length || open_regular(&path, false)?.1 != identity {
        return Err(failed());
    }
    let journal = decode(&data, database)?;
    Ok(Record {
        path,
        identity,
        data,
        journal,
    })
}

pub(super) fn read(conn: &Connection) -> LocalResult<Option<RestoreCredentials>> {
    let Some(database) = conn.path().filter(|path| !path.is_empty()) else {
        return Ok(None);
    };
    let database = PathBuf::from(database);
    if !database.is_absolute() {
        return Err(failed());
    }
    let basename = database
        .file_name()
        .and_then(|name| name.to_str())
        .filter(|name| !name.contains(['/', '\\', ':']))
        .ok_or_else(failed)?;
    let directory = database.parent().ok_or_else(failed)?;
    let (_, identity) = open_regular(&database, false)?;
    let prefix = format!("{basename}.restore-credentials-");
    let mut records = Vec::new();
    for entry in fs::read_dir(directory).map_err(|_| failed())? {
        let entry = entry.map_err(|_| failed())?;
        let name = entry.file_name();
        let name = name.to_string_lossy();
        let Some(suffix) = name.strip_prefix(&prefix) else {
            continue;
        };
        let identifier = suffix.strip_suffix(".json").ok_or_else(failed)?;
        if identifier.len() != 32
            || !identifier
                .bytes()
                .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
        {
            return Err(failed());
        }
        let record = read_record(entry.path(), basename)?;
        if record.identity == identity {
            return Err(failed());
        }
        records.push(record);
    }
    if records.is_empty() {
        return Ok(None);
    }
    records.sort_by(|left, right| left.path.cmp(&right.path));
    Ok(Some(RestoreCredentials {
        database,
        identity,
        records,
    }))
}

impl RestoreCredentials {
    pub(super) fn recover(self, conn: &Connection) -> LocalResult<()> {
        let actual: String = conn
            .query_row(
                "SELECT file FROM pragma_database_list WHERE name = 'main'",
                [],
                |row| row.get(0),
            )
            .map_err(|_| failed())?;
        if open_regular(Path::new(&actual), false)?.1 != self.identity {
            return Err(failed());
        }
        let synchronous: i64 = conn
            .pragma_query_value(None, "synchronous", |row| row.get(0))
            .map_err(|_| failed())?;
        conn.pragma_update(None, "synchronous", "FULL")
            .map_err(|_| failed())?;
        let outcome = self.import_and_checkpoint(conn);
        let reset = conn.pragma_update(None, "synchronous", synchronous);
        outcome?;
        reset.map_err(|_| failed())?;
        let (file, identity) = open_regular(&self.database, true)?;
        if identity != self.identity {
            return Err(failed());
        }
        file.sync_all().map_err(|_| failed())?;
        drop(file);
        for record in &self.records {
            let current = read_record(record.path.clone(), &record.journal.database)?;
            if current.identity != record.identity || current.data != record.data {
                return Err(failed());
            }
        }
        for record in self.records {
            fs::remove_file(record.path).map_err(|_| failed())?;
        }
        Ok(())
    }

    fn import_and_checkpoint(&self, conn: &Connection) -> LocalResult<()> {
        let transaction = Transaction::new_unchecked(conn, TransactionBehavior::Exclusive)
            .map_err(|_| failed())?;
        let now = utc_now();
        for record in &self.records {
            for reference in &record.journal.references {
                let id = format!("restore-{:x}", Sha256::digest(reference.as_bytes()));
                transaction
                    .execute(
                        "INSERT INTO credential_cleanup_intents (
                           id, model_id, credential_reference, operation, attempt,
                           last_error, created_at, updated_at
                         ) SELECT ?1, '', ?2, 'retire', 0, NULL, ?3, ?3
                         WHERE NOT EXISTS (
                           SELECT 1 FROM ai_model_configs WHERE api_key_ref = ?2
                           UNION ALL SELECT 1 FROM app_settings WHERE api_token_ref = ?2
                         ) ON CONFLICT(credential_reference) DO NOTHING",
                        params![id, reference, now],
                    )
                    .map_err(|_| failed())?;
            }
        }
        transaction.commit().map_err(|_| failed())?;
        let (busy, frames, checkpointed): (i64, i64, i64) = conn
            .query_row("PRAGMA main.wal_checkpoint(TRUNCATE)", [], |row| {
                Ok((row.get(0)?, row.get(1)?, row.get(2)?))
            })
            .map_err(|_| failed())?;
        if busy != 0 || frames != checkpointed {
            return Err(failed());
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use std::time::Duration;

    use super::*;
    use crate::infrastructure::{
        credentials::{CredentialResult, CredentialStore},
        ids,
        migrations::{ensure_schema_version, run_migrations, run_test_migration, Migration},
        repositories::credential_cleanup,
    };

    const BASENAME: &str = "synthetic #资料.sqlite3";
    const BASELINE: &str = "
        CREATE TABLE app_meta(key TEXT PRIMARY KEY, value TEXT NOT NULL);
        CREATE TABLE app_settings(id INTEGER PRIMARY KEY, api_token TEXT NOT NULL DEFAULT '', api_token_ref TEXT);
        CREATE TABLE ai_model_configs(id TEXT PRIMARY KEY, api_key TEXT NOT NULL DEFAULT '', api_key_ref TEXT);";
    const INTENTS: &str = "CREATE TABLE credential_cleanup_intents (
        id TEXT PRIMARY KEY, model_id TEXT NOT NULL, credential_reference TEXT NOT NULL UNIQUE,
        operation TEXT NOT NULL CHECK(operation IN ('retire', 'rollback')),
        attempt INTEGER NOT NULL DEFAULT 0, last_error TEXT,
        created_at TEXT NOT NULL, updated_at TEXT NOT NULL);";

    fn baseline(transaction: &Transaction<'_>) -> LocalResult<()> {
        transaction.execute_batch(BASELINE).map_err(|_| failed())
    }

    fn intents(transaction: &Transaction<'_>) -> LocalResult<()> {
        transaction.execute_batch(INTENTS).map_err(|_| failed())
    }

    fn noop(_transaction: &Transaction<'_>) -> LocalResult<()> {
        Ok(())
    }

    const MIGRATIONS: &[Migration] = &[
        Migration::new(1, "baseline", baseline),
        Migration::new(2, "v2", noop),
        Migration::credentials(3, "credentials"),
        Migration::new(4, "v4", noop),
        Migration::new(5, "v5", noop),
        Migration::new(6, "v6", noop),
        Migration::new(7, "v7", noop),
        Migration::new(8, "v8", noop),
        Migration::new(9, "v9", noop),
        Migration::new(10, "intents", intents),
        Migration::new(11, "v11", noop),
        Migration::new(12, "v12", noop),
        Migration::new(13, "v13", noop),
    ];

    struct NoSecrets;

    impl CredentialStore for NoSecrets {
        fn get_secret(&self, _key: &str) -> CredentialResult<Option<String>> {
            panic!("restore import must not read secrets");
        }

        fn set_secret(&self, _key: &str, _value: &str) -> CredentialResult<()> {
            panic!("restore import must not write secrets");
        }

        fn delete_secret(&self, _key: &str) -> CredentialResult<()> {
            panic!("restore import must not delete secrets");
        }
    }

    struct TestDirectory(PathBuf);

    impl Drop for TestDirectory {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    struct Profile {
        conn: Connection,
        directory: TestDirectory,
    }

    impl Profile {
        fn new(version: usize) -> Self {
            let directory =
                std::env::temp_dir().join(ids::timestamped_id("restore-credentials-test"));
            fs::create_dir(&directory).unwrap();
            let profile = Self {
                conn: Connection::open(directory.join(BASENAME)).unwrap(),
                directory: TestDirectory(directory),
            };
            for migration in &MIGRATIONS[..version] {
                run_test_migration(&profile.conn, &NoSecrets, migration).unwrap();
            }
            profile
                .conn
                .execute_batch("PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL;")
                .unwrap();
            profile
        }

        fn path(&self) -> PathBuf {
            self.directory.0.join(BASENAME)
        }

        fn sidecar_path(&self, identifier: &str) -> PathBuf {
            self.directory
                .0
                .join(format!("{BASENAME}.restore-credentials-{identifier}.json"))
        }

        fn record(&self, identifier: &str, references: &[&str]) -> (PathBuf, Vec<u8>) {
            let path = self.sidecar_path(identifier);
            let data = serde_json::to_vec(&serde_json::json!({
                "version": 1, "database": BASENAME, "references": references
            }))
            .unwrap();
            fs::write(&path, &data).unwrap();
            (path, data)
        }

        fn migrate(&self) -> LocalResult<i64> {
            run_migrations(&self.conn, &NoSecrets, MIGRATIONS)
        }

        fn reopen(&mut self) {
            let previous = std::mem::replace(&mut self.conn, Connection::open_in_memory().unwrap());
            previous.close().unwrap();
            self.conn = Connection::open(self.path()).unwrap();
        }

        fn count(&self) -> i64 {
            self.conn
                .query_row(
                    "SELECT COUNT(*) FROM credential_cleanup_intents",
                    [],
                    |row| row.get(0),
                )
                .unwrap()
        }

        fn metadata(&self) -> String {
            self.conn
                .query_row(
                    "SELECT id || '|' || model_id || '|' || operation || '|' || attempt || '|' ||
                 last_error || '|' || created_at || '|' || updated_at
                 FROM credential_cleanup_intents WHERE credential_reference = 'synthetic-retired'",
                    [],
                    |row| row.get(0),
                )
                .unwrap()
        }

        fn remember(&self) {
            self.conn.execute_batch(
                "INSERT INTO credential_cleanup_intents VALUES (
                 'kept', 'owner', 'synthetic-retired', 'rollback', 7, 'synthetic', 'before', 'after');"
            ).unwrap();
        }

        fn main_count(&self) -> i64 {
            let path = self.directory.0.join("main-only.sqlite3");
            fs::copy(self.path(), &path).unwrap();
            let connection = Connection::open(path).unwrap();
            connection
                .query_row(
                    "SELECT COUNT(*) FROM credential_cleanup_intents",
                    [],
                    |row| row.get(0),
                )
                .unwrap()
        }
    }

    #[test]
    fn v10_and_old_schema_import_after_upgrade_and_reopen_idempotently() {
        for version in [0, 2, 9, 10, 11, 12, 13] {
            let mut profile = Profile::new(version);
            let (path, data) =
                profile.record("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", &["synthetic-retired"]);
            assert_eq!(profile.migrate().unwrap(), 13);
            assert!(!path.exists());
            assert_eq!(profile.count(), 1);
            assert_eq!(profile.main_count(), 1);
            let intent = &credential_cleanup::list(&profile.conn).unwrap()[0];
            assert_eq!(
                intent.id,
                format!("restore-{:x}", Sha256::digest(b"synthetic-retired"))
            );
            assert_eq!(intent.model_id, "");
            assert_eq!(intent.operation, "retire");
            assert_eq!(intent.attempt, 0);
            let initialized: bool = profile.conn.query_row(
                "SELECT last_error IS NULL AND created_at = updated_at AND created_at <> '' FROM credential_cleanup_intents", [], |row| row.get(0)
            ).unwrap();
            assert!(initialized);
            profile
                .conn
                .execute_batch("DELETE FROM credential_cleanup_intents")
                .unwrap();
            profile.remember();
            let metadata = profile.metadata();
            fs::write(&path, &data).unwrap();
            profile.reopen();
            profile.migrate().unwrap();
            assert_eq!(profile.metadata(), metadata);
            assert!(!path.exists());
            profile.reopen();
            profile.migrate().unwrap();
            assert_eq!(profile.count(), 1);
            assert_eq!(profile.metadata(), metadata);
        }
    }

    #[test]
    fn production_schema_imports_without_changing_live_model_or_remote_references() {
        let profile = Profile::new(0);
        crate::local_db::schema::apply_test_schema(&profile.conn).unwrap();
        profile.conn.execute_batch(
            "INSERT INTO app_settings (
               id, theme_mode, liquid_glass_style, accent_color, locale, backend_url,
               api_token, api_token_ref, default_hotwords, summary_template, python_path, runner_script_path
             ) VALUES (1, 'auto', 'transparent', '#2f6dff', 'zh-CN', '', '', 'synthetic-token-live', '', '', '', '');
             INSERT INTO ai_model_configs(id, name, base_url, model, api_key, api_key_ref, created_at, updated_at)
             VALUES('model', 'Synthetic', 'https://synthetic.invalid', 'synthetic', '', 'synthetic-model-live', 'fixture', 'fixture');"
        ).unwrap();
        let (path, _) = profile.record(
            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            &[
                "synthetic-model-live",
                "synthetic-retired",
                "synthetic-token-live",
            ],
        );
        crate::local_db::schema::apply_test_schema(&profile.conn).unwrap();
        assert!(!path.exists());
        assert_eq!(profile.count(), 1);
        assert_eq!(
            credential_cleanup::list(&profile.conn).unwrap()[0].credential_reference,
            "synthetic-retired"
        );
        for reference in ["synthetic-model-live", "synthetic-token-live"] {
            assert!(credential_cleanup::is_referenced(&profile.conn, reference).unwrap());
        }
    }

    #[test]
    fn malformed_records_fail_before_upgrade_or_import_and_keep_all_evidence() {
        let mut invalid = vec![
            "".to_string(),
            "not-json".to_string(),
            r#"{"version":1"#.to_string(),
            r#"{"version":2,"database":"DATABASE","references":[]}"#.to_string(),
            r#"{"version":1,"database":"foreign.sqlite3","references":[]}"#.to_string(),
            r#"{"version":1,"database":"../DATABASE","references":[]}"#.to_string(),
            r#"{"version":1,"database":"DATABASE","references":[],"unknown":true}"#.to_string(),
            r#"{"version":1,"database":"foreign","database":"DATABASE","references":[]}"#
                .to_string(),
            r#"{"version":1,"version":1,"database":"DATABASE","references":[]}"#.to_string(),
            r#"{"version":1,"database":"DATABASE","references":[],"references":[]}"#.to_string(),
            r#"{"version":1,"database":"DATABASE","references":null}"#.to_string(),
            r#"{"version":1,"database":"DATABASE","references":[null]}"#.to_string(),
            r#"{"version":1,"database":"DATABASE"}"#.to_string(),
            r#"{"version":1,"database":"DATABASE","references":["same","same"]}"#.to_string(),
            r#"{"version":1,"database":"DATABASE","references":["second","first"]}"#.to_string(),
            r#"{"version":1,"database":"DATABASE","references":[""]}"#.to_string(),
            r#"{"version":1,"database":"DATABASE","references":["synthetic\u0000"]}"#.to_string(),
            r#"{"version":1,"database":"DATABASE","references":["synthetic\r"]}"#.to_string(),
            r#"{"version":1,"database":"DATABASE","references":["synthetic\n"]}"#.to_string(),
            r#"{"version":1,"database":"DATABASE","references":[]}{}"#.to_string(),
            r#"[1,"DATABASE",[]]"#.to_string(),
        ];
        invalid.push(format!(
            r#"{{"version":1,"database":"DATABASE","references":["{}"]}}"#,
            "a".repeat(2048)
        ));
        for version in [2, 10] {
            for contents in &invalid {
                let profile = Profile::new(version);
                let (valid_path, valid_data) =
                    profile.record("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", &["synthetic-valid"]);
                let path = profile.sidecar_path("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb");
                let data = contents.replace("DATABASE", BASENAME).into_bytes();
                fs::write(&path, &data).unwrap();
                assert_eq!(profile.migrate().unwrap_err(), RECOVERY_ERROR);
                assert_eq!(
                    ensure_schema_version(&profile.conn).unwrap(),
                    version as i64
                );
                if version == 10 {
                    assert_eq!(profile.count(), 0);
                }
                assert_eq!(fs::read(path).unwrap(), data);
                assert_eq!(fs::read(valid_path).unwrap(), valid_data);
            }
        }
        let profile = Profile::new(10);
        let (path, _) = profile.record("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", &[]);
        fs::write(
            &path,
            b"{\"version\":1,\"database\":\"synthetic #\xff.sqlite3\",\"references\":[]}",
        )
        .unwrap();
        assert!(profile.migrate().is_err());
        assert!(path.exists());
    }

    #[test]
    fn invalid_names_non_files_and_oversized_records_are_rejected() {
        for identifier in [
            "short",
            "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaag",
            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.json.extra",
        ] {
            let profile = Profile::new(10);
            let (path, data) = profile.record(identifier, &["synthetic-retired"]);
            assert!(profile.migrate().is_err());
            assert_eq!(profile.count(), 0);
            assert_eq!(fs::read(path).unwrap(), data);
        }
        for directory in [false, true] {
            let profile = Profile::new(10);
            let path = profile.sidecar_path("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa");
            if directory {
                fs::create_dir(&path).unwrap();
            } else {
                File::create(&path)
                    .unwrap()
                    .set_len(MAX_RECORD_SIZE + 1)
                    .unwrap();
            }
            assert!(profile.migrate().is_err());
            assert_eq!(profile.count(), 0);
            assert!(path.exists());
        }
    }

    #[test]
    fn foreign_names_nested_records_and_temporary_files_are_ignored() {
        let profile = Profile::new(10);
        let nested = profile.directory.0.join("nested");
        fs::create_dir(&nested).unwrap();
        let paths = [
            profile
                .directory
                .0
                .join("other.sqlite3.restore-credentials-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.json"),
            profile
                .directory
                .0
                .join(format!(".{BASENAME}.restore-credentials-synthetic.tmp")),
            nested.join(format!(
                "{BASENAME}.restore-credentials-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.json"
            )),
        ];
        for path in &paths {
            fs::write(path, "not-json").unwrap();
        }
        let (path, _) = profile.record("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", &[]);
        profile.migrate().unwrap();
        assert!(!path.exists());
        assert_eq!(profile.count(), 0);
        for path in paths {
            assert_eq!(fs::read_to_string(path).unwrap(), "not-json");
        }
    }

    #[test]
    fn import_failure_rolls_back_all_records_and_reopen_retries() {
        let mut profile = Profile::new(10);
        let records = [
            profile.record("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", &["synthetic-first"]),
            profile.record("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", &["synthetic-second"]),
        ];
        profile
            .conn
            .execute_batch(
                "CREATE TRIGGER reject_restore_intent BEFORE INSERT ON credential_cleanup_intents
             WHEN NEW.credential_reference = 'synthetic-second'
             BEGIN SELECT RAISE(ABORT, 'synthetic import failure'); END;",
            )
            .unwrap();
        assert_eq!(profile.migrate().unwrap_err(), RECOVERY_ERROR);
        assert_eq!(profile.count(), 0);
        let synchronous: i64 = profile
            .conn
            .pragma_query_value(None, "synchronous", |row| row.get(0))
            .unwrap();
        assert_eq!(synchronous, 1);
        for (path, data) in &records {
            assert_eq!(fs::read(path).unwrap(), *data);
        }
        profile
            .conn
            .execute_batch("DROP TRIGGER reject_restore_intent")
            .unwrap();
        profile.reopen();
        profile.migrate().unwrap();
        assert_eq!(profile.main_count(), 2);
        for (path, _) in records {
            assert!(!path.exists());
        }
    }

    #[test]
    fn checkpoint_block_keeps_new_and_existing_normal_wal_intents_replayable() {
        for existing in [false, true] {
            let mut profile = Profile::new(13);
            profile.conn.busy_timeout(Duration::ZERO).unwrap();
            profile
                .conn
                .execute_batch("PRAGMA wal_autocheckpoint=0; PRAGMA wal_checkpoint(TRUNCATE);")
                .unwrap();
            let reader = Connection::open(profile.path()).unwrap();
            reader.execute_batch("BEGIN").unwrap();
            let count: i64 = reader
                .query_row(
                    "SELECT COUNT(*) FROM credential_cleanup_intents",
                    [],
                    |row| row.get(0),
                )
                .unwrap();
            assert_eq!(count, 0);
            if existing {
                profile.remember();
            }
            assert_eq!(profile.main_count(), 0);
            let (path, data) =
                profile.record("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", &["synthetic-retired"]);
            let changes = profile.conn.total_changes();
            assert_eq!(profile.migrate().unwrap_err(), RECOVERY_ERROR);
            assert_eq!(fs::read(&path).unwrap(), data);
            assert_eq!(profile.count(), 1);
            assert_eq!(profile.main_count(), 0);
            let synchronous: i64 = profile
                .conn
                .pragma_query_value(None, "synchronous", |row| row.get(0))
                .unwrap();
            assert_eq!(synchronous, 1);
            if existing {
                assert_eq!(profile.conn.total_changes(), changes);
                assert_eq!(
                    profile.metadata(),
                    "kept|owner|rollback|7|synthetic|before|after"
                );
            }
            reader.execute_batch("ROLLBACK").unwrap();
            reader.close().unwrap();
            profile.reopen();
            profile.migrate().unwrap();
            assert!(!path.exists());
            assert_eq!(profile.main_count(), 1);
            if existing {
                assert_eq!(
                    profile.metadata(),
                    "kept|owner|rollback|7|synthetic|before|after"
                );
            }
        }
    }

    #[test]
    fn full_synchronous_is_required_during_import_and_restored_afterward() {
        let profile = Profile::new(10);
        profile
            .conn
            .execute_batch(
                "CREATE TRIGGER require_full_sync BEFORE INSERT ON credential_cleanup_intents
             WHEN (SELECT synchronous FROM pragma_synchronous) <> 2
             BEGIN SELECT RAISE(ABORT, 'synthetic FULL required'); END;",
            )
            .unwrap();
        let (path, _) = profile.record("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", &["synthetic-retired"]);
        profile.migrate().unwrap();
        assert!(!path.exists());
        assert_eq!(profile.main_count(), 1);
        let synchronous: i64 = profile
            .conn
            .pragma_query_value(None, "synchronous", |row| row.get(0))
            .unwrap();
        assert_eq!(synchronous, 1);
    }

    #[test]
    fn hard_link_records_and_wrong_open_database_are_rejected() {
        let profile = Profile::new(10);
        let (path, data) =
            profile.record("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", &["synthetic-retired"]);
        let other = Profile::new(10);
        let records = read(&profile.conn).unwrap().unwrap();
        assert!(records.recover(&other.conn).is_err());
        assert_eq!(profile.count(), 0);
        assert_eq!(other.count(), 0);
        assert_eq!(fs::read(&path).unwrap(), data);
        let target = profile.directory.0.join("hard-link-target");
        fs::hard_link(&path, &target).unwrap();
        assert!(profile.migrate().is_err());
        assert_eq!(profile.count(), 0);
        assert_eq!(fs::read(target).unwrap(), data);
    }

    #[cfg(unix)]
    #[test]
    fn symlink_records_existing_or_dangling_and_database_aliases_are_rejected() {
        for exists in [false, true] {
            let profile = Profile::new(10);
            let (path, data) =
                profile.record("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", &["synthetic-retired"]);
            let target = profile.directory.0.join("outside-record");
            fs::rename(&path, &target).unwrap();
            if !exists {
                fs::remove_file(&target).unwrap();
            }
            std::os::unix::fs::symlink(&target, &path).unwrap();
            assert!(profile.migrate().is_err());
            assert_eq!(profile.count(), 0);
            assert!(fs::symlink_metadata(path).unwrap().file_type().is_symlink());
            if exists {
                assert_eq!(fs::read(target).unwrap(), data);
            }
        }
        let profile = Profile::new(10);
        let path = profile.sidecar_path("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa");
        std::os::unix::fs::symlink(profile.path(), &path).unwrap();
        assert!(profile.migrate().is_err());
        assert_eq!(profile.count(), 0);
    }

    #[test]
    fn changed_record_is_retained_after_durable_import_and_retry_keeps_both_intents() {
        let profile = Profile::new(10);
        let (path, _) = profile.record("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", &["synthetic-before"]);
        let records = read(&profile.conn).unwrap().unwrap();
        let (_, changed) = profile.record("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", &["synthetic-after"]);
        assert!(records.recover(&profile.conn).is_err());
        assert_eq!(profile.main_count(), 1);
        assert_eq!(fs::read(&path).unwrap(), changed);
        profile.migrate().unwrap();
        assert!(!path.exists());
        assert_eq!(profile.main_count(), 2);
    }
}
