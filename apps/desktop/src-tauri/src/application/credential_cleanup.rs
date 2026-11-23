use rusqlite::Connection;

use crate::{
    infrastructure::{credentials::CredentialStore, ids, repositories::credential_cleanup},
    local_db::LocalResult,
};

pub fn recover_pending(
    connection: &Connection,
    credential_store: &dyn CredentialStore,
) -> LocalResult<()> {
    let intents = credential_cleanup::list(connection)?;
    let mut failures = Vec::new();
    for intent in intents {
        if credential_cleanup::is_referenced(connection, &intent.credential_reference)? {
            credential_cleanup::finish(connection, &intent.id)?;
            continue;
        }
        if credential_cleanup::backup_is_referenced(connection, &intent.credential_reference)? {
            continue;
        }
        match credential_store.delete_secret(&intent.credential_reference) {
            Ok(()) => credential_cleanup::finish(connection, &intent.id)?,
            Err(error) => {
                let error = String::from(error);
                credential_cleanup::record_failure(
                    connection,
                    &intent.id,
                    &error,
                    &crate::infrastructure::time::utc_now(),
                )?;
                failures.push(format!("{}: {error}", intent.id));
            }
        }
    }
    if failures.is_empty() {
        Ok(())
    } else {
        Err(format!("AI 凭据清理仍有待重试项：{}", failures.join("；")))
    }
}

pub fn remember_rollback(
    connection: &mut Connection,
    model_id: &str,
    credential_reference: &str,
) -> LocalResult<()> {
    let now = crate::infrastructure::time::utc_now();
    credential_cleanup::insert(
        connection,
        &ids::timestamped_id("credential-cleanup"),
        model_id,
        credential_reference,
        "rollback",
        &now,
    )
}

#[cfg(test)]
mod tests {
    use std::{
        cell::{Cell, RefCell},
        collections::HashSet,
        fs,
        path::PathBuf,
    };

    use super::*;
    use crate::{
        domain::error::AppError,
        infrastructure::{credentials::CredentialResult, migrations::CURRENT_SCHEMA_VERSION},
    };

    const DATABASE_BASENAME: &str = "synthetic #资料.sqlite3";
    const SCHEMA: &str = "
        CREATE TABLE app_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
        CREATE TABLE ai_model_configs (api_key TEXT NOT NULL DEFAULT '', api_key_ref TEXT);
        CREATE TABLE app_settings (api_token TEXT NOT NULL DEFAULT '', api_token_ref TEXT);
        CREATE TABLE credential_cleanup_intents (
            id TEXT PRIMARY KEY,
            model_id TEXT NOT NULL,
            credential_reference TEXT NOT NULL UNIQUE,
            operation TEXT NOT NULL,
            attempt INTEGER NOT NULL DEFAULT 0,
            last_error TEXT,
            created_at TEXT NOT NULL,
            updated_at TEXT NOT NULL
        );";

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
        fn new() -> Self {
            let directory = std::env::temp_dir().join(format!(
                "liberty-cleanup-{}-{}",
                std::process::id(),
                ids::timestamped_id("test")
            ));
            fs::create_dir(&directory).unwrap();
            let directory = TestDirectory(directory);
            let connection = Connection::open(directory.0.join(DATABASE_BASENAME)).unwrap();
            connection.execute_batch(SCHEMA).unwrap();
            connection
                .execute(
                    "INSERT INTO app_meta VALUES ('schema_version', ?1)",
                    [CURRENT_SCHEMA_VERSION.to_string()],
                )
                .unwrap();
            Self {
                connection,
                directory,
            }
        }

        fn backup_path(&self, purpose: &str) -> PathBuf {
            self.directory
                .0
                .join(format!("{DATABASE_BASENAME}.{purpose}.bak"))
        }

        fn backup(&self, purpose: &str) -> PathBuf {
            let path = self.backup_path(purpose);
            self.connection
                .backup(rusqlite::MAIN_DB, &path, None::<fn(_)>)
                .unwrap();
            path
        }

        fn remember(&mut self, reference: &str) {
            credential_cleanup::insert(
                &mut self.connection,
                reference,
                "synthetic-model",
                reference,
                "retire",
                "2026-11-19T00:00:00Z",
            )
            .unwrap();
        }
    }

    struct TestCredentialStore {
        references: RefCell<HashSet<String>>,
        deletes: RefCell<Vec<String>>,
        fail_delete: Cell<bool>,
    }

    impl TestCredentialStore {
        fn new(references: &[&str]) -> Self {
            Self {
                references: RefCell::new(
                    references.iter().map(|value| value.to_string()).collect(),
                ),
                deletes: RefCell::new(Vec::new()),
                fail_delete: Cell::new(false),
            }
        }
    }

    impl CredentialStore for TestCredentialStore {
        fn get_secret(&self, _key: &str) -> CredentialResult<Option<String>> {
            panic!("cleanup must not read secret values");
        }

        fn set_secret(&self, _key: &str, _value: &str) -> CredentialResult<()> {
            panic!("cleanup must not copy secret values");
        }

        fn delete_secret(&self, key: &str) -> CredentialResult<()> {
            self.deletes.borrow_mut().push(key.to_string());
            if self.fail_delete.get() {
                return Err(AppError::Infrastructure(
                    "synthetic-vault-unavailable".into(),
                ));
            }
            self.references.borrow_mut().remove(key);
            Ok(())
        }
    }

    fn assert_unchanged_intent(connection: &Connection, reference: &str) {
        let state = connection
            .query_row(
                "SELECT credential_reference, attempt, last_error, updated_at
                 FROM credential_cleanup_intents WHERE id = ?1",
                [reference],
                |row| {
                    Ok((
                        row.get::<_, String>(0)?,
                        row.get::<_, u32>(1)?,
                        row.get::<_, Option<String>>(2)?,
                        row.get::<_, String>(3)?,
                    ))
                },
            )
            .unwrap();
        assert_eq!(
            state,
            (
                reference.to_string(),
                0,
                None,
                "2026-11-19T00:00:00Z".to_string()
            )
        );
    }

    fn assert_backup_check_refused(profile: &TestProfile, store: &TestCredentialStore) {
        assert_eq!(
            recover_pending(&profile.connection, store).unwrap_err(),
            "无法核实数据库备份凭据引用，凭据清理已暂缓。"
        );
        assert!(store.deletes.borrow().is_empty());
        assert!(store.references.borrow().contains("synthetic-retired-ref"));
        assert_unchanged_intent(&profile.connection, "synthetic-retired-ref");
    }

    #[test]
    fn recover_retains_model_and_remote_credentials_until_last_backup_is_removed() {
        let mut profile = TestProfile::new();
        profile
            .connection
            .execute_batch(
                "INSERT INTO ai_model_configs(api_key_ref) VALUES ('synthetic-model-old');
                 INSERT INTO app_settings(api_token_ref) VALUES ('synthetic-token-old');",
            )
            .unwrap();
        let backups = [
            profile.backup("pre-migration-v3-snapshot"),
            profile.backup("pre-native-v10-snapshot"),
            profile.backup("pre-restore-v10-snapshot"),
        ];
        let snapshot_bytes = fs::read(&backups[2]).unwrap();
        profile
            .connection
            .execute_batch(
                "UPDATE ai_model_configs SET api_key_ref = 'synthetic-model-new';
                 UPDATE app_settings SET api_token_ref = 'synthetic-token-new';",
            )
            .unwrap();
        profile.remember("synthetic-model-old");
        profile.remember("synthetic-token-old");
        let store = TestCredentialStore::new(&[
            "synthetic-model-old",
            "synthetic-token-old",
            "synthetic-model-new",
            "synthetic-token-new",
        ]);
        store.fail_delete.set(true);

        recover_pending(&profile.connection, &store).unwrap();
        assert_unchanged_intent(&profile.connection, "synthetic-model-old");
        assert_unchanged_intent(&profile.connection, "synthetic-token-old");
        let reopened = Connection::open(profile.directory.0.join(DATABASE_BASENAME)).unwrap();
        recover_pending(&reopened, &store).unwrap();
        assert_eq!(credential_cleanup::list(&reopened).unwrap().len(), 2);
        drop(reopened);
        for path in &backups[..2] {
            fs::remove_file(path).unwrap();
            recover_pending(&profile.connection, &store).unwrap();
            assert_eq!(
                credential_cleanup::list(&profile.connection).unwrap().len(),
                2
            );
        }
        assert_eq!(fs::read(&backups[2]).unwrap(), snapshot_bytes);
        assert!(store.deletes.borrow().is_empty());

        fs::remove_file(&backups[2]).unwrap();
        store.fail_delete.set(false);
        recover_pending(&profile.connection, &store).unwrap();
        assert!(credential_cleanup::list(&profile.connection)
            .unwrap()
            .is_empty());
        let mut deleted = store.deletes.borrow().clone();
        deleted.sort();
        assert_eq!(deleted, ["synthetic-model-old", "synthetic-token-old"]);
        assert_eq!(
            *store.references.borrow(),
            HashSet::from([
                "synthetic-model-new".to_string(),
                "synthetic-token-new".to_string()
            ])
        );
    }

    #[test]
    fn recover_finishes_live_references_before_checking_backups() {
        let mut profile = TestProfile::new();
        profile
            .connection
            .execute_batch(
                "INSERT INTO ai_model_configs(api_key_ref) VALUES ('synthetic-model-live');
                 INSERT INTO app_settings(api_token_ref) VALUES ('synthetic-token-live');",
            )
            .unwrap();
        fs::write(profile.backup_path("pre-native-corrupt"), b"not sqlite").unwrap();
        profile.remember("synthetic-model-live");
        profile.remember("synthetic-token-live");
        let store = TestCredentialStore::new(&["synthetic-model-live", "synthetic-token-live"]);
        store.fail_delete.set(true);

        recover_pending(&profile.connection, &store).unwrap();

        assert!(credential_cleanup::list(&profile.connection)
            .unwrap()
            .is_empty());
        assert!(store.deletes.borrow().is_empty());
        assert_eq!(store.references.borrow().len(), 2);
    }

    #[test]
    fn recover_checks_available_reference_columns_in_pre_v3_backups() {
        for version in [0, 1, 2] {
            for (seed_sql, drop_sql) in [
                (
                    "INSERT INTO ai_model_configs(api_key_ref) VALUES ('synthetic-retired-ref')",
                    "DROP TABLE app_settings",
                ),
                (
                    "INSERT INTO app_settings(api_token_ref) VALUES ('synthetic-retired-ref')",
                    "DROP TABLE ai_model_configs",
                ),
            ] {
                let mut profile = TestProfile::new();
                let path = profile.backup("pre-migration-early");
                let backup = Connection::open(&path).unwrap();
                backup.execute_batch(seed_sql).unwrap();
                backup.execute_batch(drop_sql).unwrap();
                backup
                    .execute("UPDATE app_meta SET value = ?1", [version.to_string()])
                    .unwrap();
                drop(backup);
                profile.remember("synthetic-retired-ref");
                let store = TestCredentialStore::new(&["synthetic-retired-ref"]);
                store.fail_delete.set(true);

                recover_pending(&profile.connection, &store).unwrap();

                assert!(store.deletes.borrow().is_empty());
                assert_unchanged_intent(&profile.connection, "synthetic-retired-ref");
            }
        }
    }

    #[test]
    fn recover_allows_pre_v3_backups_without_reference_columns() {
        for version in [1, 2] {
            let mut profile = TestProfile::new();
            let path = profile.backup("pre-migration-plaintext-schema");
            let backup = Connection::open(path).unwrap();
            backup
                .execute_batch(
                    "DROP TABLE ai_model_configs;
                     DROP TABLE app_settings;
                     CREATE TABLE ai_model_configs(api_key TEXT);
                     CREATE TABLE app_settings(api_token TEXT);",
                )
                .unwrap();
            backup
                .execute("UPDATE app_meta SET value = ?1", [version.to_string()])
                .unwrap();
            drop(backup);
            profile.remember("synthetic-retired-ref");
            let store = TestCredentialStore::new(&["synthetic-retired-ref"]);

            recover_pending(&profile.connection, &store).unwrap();

            assert_eq!(*store.deletes.borrow(), ["synthetic-retired-ref"]);
            assert!(credential_cleanup::list(&profile.connection)
                .unwrap()
                .is_empty());
        }
    }

    #[test]
    fn recover_refuses_corrupt_empty_and_non_file_backup_candidates() {
        for variant in ["corrupt", "empty", "directory"] {
            let mut profile = TestProfile::new();
            let path = profile.backup_path("pre-native-invalid");
            match variant {
                "corrupt" => fs::write(path, b"not sqlite").unwrap(),
                "empty" => fs::write(path, []).unwrap(),
                "directory" => fs::create_dir(path).unwrap(),
                _ => unreachable!(),
            }
            profile.remember("synthetic-retired-ref");
            let store = TestCredentialStore::new(&["synthetic-retired-ref"]);

            assert_backup_check_refused(&profile, &store);
        }
    }

    #[test]
    fn recover_refuses_invalid_versions_and_missing_required_reference_columns() {
        for mutation in [
            "UPDATE app_meta SET value = '-1'",
            "UPDATE app_meta SET value = 'invalid'",
            "UPDATE app_meta SET value = '999'",
            "UPDATE app_meta SET value = '3'; ALTER TABLE app_settings DROP COLUMN api_token_ref",
            "ALTER TABLE ai_model_configs DROP COLUMN api_key_ref",
            "DROP TABLE app_settings",
        ] {
            let mut profile = TestProfile::new();
            let path = profile.backup("pre-restore-invalid-schema");
            let backup = Connection::open(path).unwrap();
            backup.execute_batch(mutation).unwrap();
            drop(backup);
            profile.remember("synthetic-retired-ref");
            let store = TestCredentialStore::new(&["synthetic-retired-ref"]);

            assert_backup_check_refused(&profile, &store);
        }
    }

    #[test]
    fn recover_refuses_sqlite_integrity_failures() {
        let mut profile = TestProfile::new();
        let path = profile.backup("pre-native-integrity-failure");
        let backup = Connection::open(path).unwrap();
        backup
            .execute_batch(
                "CREATE TABLE synthetic_integrity (value INTEGER);
                 CREATE INDEX synthetic_integrity_index ON synthetic_integrity(value);
                 INSERT INTO synthetic_integrity VALUES (1);
                 PRAGMA writable_schema = ON;
                 DELETE FROM sqlite_master WHERE name = 'synthetic_integrity_index';",
            )
            .unwrap();
        drop(backup);
        let backup = Connection::open(profile.backup_path("pre-native-integrity-failure")).unwrap();
        assert_ne!(
            backup
                .query_row("PRAGMA integrity_check", [], |row| row.get::<_, String>(0))
                .unwrap(),
            "ok"
        );
        drop(backup);
        profile.remember("synthetic-retired-ref");
        let store = TestCredentialStore::new(&["synthetic-retired-ref"]);

        assert_backup_check_refused(&profile, &store);
    }

    #[test]
    fn recover_reads_committed_wal_references_without_modifying_backup() {
        let mut profile = TestProfile::new();
        let path = profile.backup("pre-restore-wal");
        let backup = Connection::open(&path).unwrap();
        backup
            .execute_batch("PRAGMA journal_mode = WAL; PRAGMA wal_autocheckpoint = 0;")
            .unwrap();
        let before = fs::read(&path).unwrap();
        backup
            .execute_batch(
                "INSERT INTO ai_model_configs(api_key_ref) VALUES ('synthetic-model-old');
                 INSERT INTO app_settings(api_token_ref) VALUES ('synthetic-token-old');",
            )
            .unwrap();
        assert_eq!(fs::read(&path).unwrap(), before);
        profile.remember("synthetic-model-old");
        profile.remember("synthetic-token-old");
        let store = TestCredentialStore::new(&["synthetic-model-old", "synthetic-token-old"]);

        recover_pending(&profile.connection, &store).unwrap();

        assert!(store.deletes.borrow().is_empty());
        assert_unchanged_intent(&profile.connection, "synthetic-model-old");
        assert_unchanged_intent(&profile.connection, "synthetic-token-old");
        assert_eq!(fs::read(&path).unwrap(), before);
        drop(backup);
    }

    #[test]
    fn recover_ignores_other_databases_nested_files_and_non_backup_names() {
        let mut profile = TestProfile::new();
        for name in [
            "other.sqlite3.pre-native-corrupt.bak".to_string(),
            format!("{DATABASE_BASENAME}.pre-native-corrupt.bak.partial"),
            format!("{DATABASE_BASENAME}.snapshot.bak"),
        ] {
            fs::write(profile.directory.0.join(name), b"not sqlite").unwrap();
        }
        let nested = profile.directory.0.join("nested");
        fs::create_dir(&nested).unwrap();
        fs::write(
            nested.join(format!("{DATABASE_BASENAME}.pre-native-corrupt.bak")),
            b"not sqlite",
        )
        .unwrap();
        profile.remember("synthetic-retired-ref");
        let store = TestCredentialStore::new(&["synthetic-retired-ref"]);

        recover_pending(&profile.connection, &store).unwrap();

        assert_eq!(*store.deletes.borrow(), ["synthetic-retired-ref"]);
        assert!(credential_cleanup::list(&profile.connection)
            .unwrap()
            .is_empty());
    }

    #[cfg(unix)]
    #[test]
    fn recover_refuses_existing_and_dangling_backup_symlinks() {
        for exists in [true, false] {
            let mut profile = TestProfile::new();
            let target = profile.directory.0.join("synthetic-link-target");
            if exists {
                profile
                    .connection
                    .backup(rusqlite::MAIN_DB, &target, None::<fn(_)>)
                    .unwrap();
            }
            std::os::unix::fs::symlink(&target, profile.backup_path("pre-native-link")).unwrap();
            profile.remember("synthetic-retired-ref");
            let store = TestCredentialStore::new(&["synthetic-retired-ref"]);

            assert_backup_check_refused(&profile, &store);
        }
    }

    #[test]
    fn recover_refuses_locked_backup_then_retries_after_unlock() {
        let mut profile = TestProfile::new();
        let path = profile.backup("pre-native-locked");
        let backup = Connection::open(path).unwrap();
        backup.execute_batch("BEGIN EXCLUSIVE").unwrap();
        profile.remember("synthetic-retired-ref");
        let store = TestCredentialStore::new(&["synthetic-retired-ref"]);

        assert_backup_check_refused(&profile, &store);
        backup.execute_batch("ROLLBACK").unwrap();
        recover_pending(&profile.connection, &store).unwrap();

        assert_eq!(*store.deletes.borrow(), ["synthetic-retired-ref"]);
        assert!(credential_cleanup::list(&profile.connection)
            .unwrap()
            .is_empty());
    }

    #[test]
    fn recover_records_and_retries_actual_vault_failures() {
        let mut profile = TestProfile::new();
        profile.remember("synthetic-retired-ref");
        let store = TestCredentialStore::new(&["synthetic-retired-ref"]);
        store.fail_delete.set(true);

        let error = recover_pending(&profile.connection, &store).unwrap_err();

        assert!(error.contains("synthetic-vault-unavailable"));
        assert_eq!(
            credential_cleanup::list(&profile.connection).unwrap()[0].attempt,
            1
        );
        let path = profile.backup("pre-restore-retained-after-failure");
        let backup = Connection::open(&path).unwrap();
        backup
            .execute(
                "INSERT INTO ai_model_configs(api_key_ref) VALUES (?1)",
                ["synthetic-retired-ref"],
            )
            .unwrap();
        drop(backup);
        recover_pending(&profile.connection, &store).unwrap();
        assert_eq!(
            credential_cleanup::list(&profile.connection).unwrap()[0].attempt,
            1
        );
        assert_eq!(store.deletes.borrow().len(), 1);
        fs::remove_file(path).unwrap();
        store.fail_delete.set(false);
        recover_pending(&profile.connection, &store).unwrap();
        assert!(credential_cleanup::list(&profile.connection)
            .unwrap()
            .is_empty());
        assert!(store.references.borrow().is_empty());
    }

    #[test]
    fn recover_supports_in_memory_model_only_fixtures() {
        let mut connection = Connection::open_in_memory().unwrap();
        connection.execute_batch(SCHEMA).unwrap();
        connection
            .execute_batch(
                "DROP TABLE app_settings;
                 DROP TABLE app_meta;
                 INSERT INTO ai_model_configs(api_key_ref) VALUES ('synthetic-live-ref');",
            )
            .unwrap();
        for reference in ["synthetic-live-ref", "synthetic-retired-ref"] {
            credential_cleanup::insert(
                &mut connection,
                reference,
                "synthetic-model",
                reference,
                "retire",
                "2026-11-19T00:00:00Z",
            )
            .unwrap();
        }
        let store = TestCredentialStore::new(&["synthetic-live-ref", "synthetic-retired-ref"]);

        recover_pending(&connection, &store).unwrap();

        assert!(credential_cleanup::list(&connection).unwrap().is_empty());
        assert_eq!(*store.deletes.borrow(), ["synthetic-retired-ref"]);
        assert!(store.references.borrow().contains("synthetic-live-ref"));
    }
}
