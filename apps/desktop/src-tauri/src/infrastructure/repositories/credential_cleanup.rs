use std::{fs, path::Path, time::Duration};

use rusqlite::{params, Connection, OpenFlags, Transaction};

use crate::{infrastructure::migrations, local_db::LocalResult};

const BACKUP_CHECK_FAILED: &str = "无法核实数据库备份凭据引用，凭据清理已暂缓。";

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CredentialCleanupIntent {
    pub id: String,
    pub model_id: String,
    pub credential_reference: String,
    pub operation: String,
    pub attempt: u32,
}

pub fn insert_tx(
    transaction: &Transaction<'_>,
    id: &str,
    model_id: &str,
    credential_reference: &str,
    operation: &str,
    now: &str,
) -> LocalResult<()> {
    if credential_reference.trim().is_empty() {
        return Ok(());
    }
    transaction
        .execute(
            "INSERT INTO credential_cleanup_intents (
               id, model_id, credential_reference, operation, attempt,
               last_error, created_at, updated_at
             ) VALUES (?1, ?2, ?3, ?4, 0, NULL, ?5, ?5)
             ON CONFLICT(credential_reference) DO UPDATE SET
               model_id = excluded.model_id,
               operation = excluded.operation,
               updated_at = excluded.updated_at",
            params![id, model_id, credential_reference, operation, now],
        )
        .map_err(|error| error.to_string())?;
    Ok(())
}

pub fn insert(
    connection: &mut Connection,
    id: &str,
    model_id: &str,
    credential_reference: &str,
    operation: &str,
    now: &str,
) -> LocalResult<()> {
    let transaction = connection
        .transaction()
        .map_err(|error| error.to_string())?;
    insert_tx(
        &transaction,
        id,
        model_id,
        credential_reference,
        operation,
        now,
    )?;
    transaction.commit().map_err(|error| error.to_string())
}

pub fn list(connection: &Connection) -> LocalResult<Vec<CredentialCleanupIntent>> {
    let mut statement = connection
        .prepare(
            "SELECT id, model_id, credential_reference, operation, attempt
             FROM credential_cleanup_intents
             ORDER BY created_at, id",
        )
        .map_err(|error| error.to_string())?;
    let intents = statement
        .query_map([], |row| {
            let attempt = row.get::<_, i64>(4)?;
            Ok(CredentialCleanupIntent {
                id: row.get(0)?,
                model_id: row.get(1)?,
                credential_reference: row.get(2)?,
                operation: row.get(3)?,
                attempt: u32::try_from(attempt).unwrap_or(u32::MAX),
            })
        })
        .map_err(|error| error.to_string())?
        .collect::<Result<Vec<_>, _>>()
        .map_err(|error| error.to_string())?;
    Ok(intents)
}

pub fn is_referenced(connection: &Connection, credential_reference: &str) -> LocalResult<bool> {
    let model_referenced = connection
        .query_row(
            "SELECT EXISTS (
               SELECT 1 FROM ai_model_configs WHERE api_key_ref = ?1
             )",
            params![credential_reference],
            |row| row.get::<_, bool>(0),
        )
        .map_err(|error| error.to_string())?;
    if model_referenced {
        return Ok(true);
    }
    // Native publication also uses these durable intents for remote tokens.
    // Older isolated model tests do not create app_settings; production v10
    // always has it. Never treat a query failure as an unreferenced secret.
    let has_settings = connection
        .query_row(
            "SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'app_settings')",
            [],
            |row| row.get::<_, bool>(0),
        )
        .map_err(|error| error.to_string())?;
    if !has_settings {
        return Ok(false);
    }
    connection
        .query_row(
            "SELECT EXISTS (SELECT 1 FROM app_settings WHERE api_token_ref = ?1)",
            params![credential_reference],
            |row| row.get(0),
        )
        .map_err(|error| error.to_string())
}

pub fn backup_is_referenced(
    connection: &Connection,
    credential_reference: &str,
) -> LocalResult<bool> {
    let Some(database) = connection.path().filter(|path| !path.is_empty()) else {
        return Ok(false);
    };
    let failed = || BACKUP_CHECK_FAILED.to_string();
    let database = Path::new(database);
    if credential_reference.is_empty()
        || !fs::symlink_metadata(database)
            .map_err(|_| failed())?
            .is_file()
    {
        return Err(failed());
    }
    let directory = database.parent().ok_or_else(failed)?;
    let basename = database.file_name().ok_or_else(failed)?.to_string_lossy();
    let prefix = format!("{basename}.pre-");
    for entry in fs::read_dir(directory).map_err(|_| failed())? {
        let entry = entry.map_err(|_| failed())?;
        let filename = entry.file_name();
        let filename = filename.to_string_lossy();
        if !filename.starts_with(&prefix)
            || !filename.ends_with(".bak")
            || filename.contains(['/', '\\', ':'])
        {
            continue;
        }
        let path = entry.path();
        let metadata = fs::symlink_metadata(&path).map_err(|_| failed())?;
        if !metadata.is_file() || metadata.len() == 0 {
            return Err(failed());
        }
        if backup_file_is_referenced(&path, credential_reference)? {
            return Ok(true);
        }
    }
    Ok(false)
}

fn backup_file_is_referenced(path: &Path, credential_reference: &str) -> LocalResult<bool> {
    let failed = || BACKUP_CHECK_FAILED.to_string();
    let mut connection = Connection::open_with_flags(
        path,
        OpenFlags::SQLITE_OPEN_READ_ONLY
            | OpenFlags::SQLITE_OPEN_NOFOLLOW
            | OpenFlags::SQLITE_OPEN_NO_MUTEX,
    )
    .map_err(|_| failed())?;
    connection
        .busy_timeout(Duration::from_secs(5))
        .map_err(|_| failed())?;
    connection
        .execute_batch("PRAGMA query_only = ON; PRAGMA trusted_schema = OFF;")
        .map_err(|_| failed())?;
    let transaction = connection.transaction().map_err(|_| failed())?;
    let integrity = transaction
        .query_row("PRAGMA integrity_check", [], |row| row.get::<_, String>(0))
        .map_err(|_| failed())?;
    if integrity != "ok" {
        return Err(failed());
    }
    let version = migrations::ensure_schema_version(&transaction).map_err(|_| failed())?;
    let mut referenced = false;
    for (columns_sql, column_name, reference_sql) in [
        (
            "PRAGMA table_info(ai_model_configs)",
            "api_key_ref",
            "SELECT EXISTS (SELECT 1 FROM ai_model_configs WHERE api_key_ref = ?1)",
        ),
        (
            "PRAGMA table_info(app_settings)",
            "api_token_ref",
            "SELECT EXISTS (SELECT 1 FROM app_settings WHERE api_token_ref = ?1)",
        ),
    ] {
        let mut columns = transaction.prepare(columns_sql).map_err(|_| failed())?;
        let column_names = columns
            .query_map([], |row| row.get::<_, String>(1))
            .map_err(|_| failed())?
            .collect::<Result<Vec<_>, _>>()
            .map_err(|_| failed())?;
        if !column_names.iter().any(|name| name == column_name) {
            if version >= 3 {
                return Err(failed());
            }
            continue;
        }
        referenced |= transaction
            .query_row(reference_sql, params![credential_reference], |row| {
                row.get::<_, bool>(0)
            })
            .map_err(|_| failed())?;
    }
    transaction.commit().map_err(|_| failed())?;
    Ok(referenced)
}

pub fn finish(connection: &Connection, id: &str) -> LocalResult<()> {
    connection
        .execute(
            "DELETE FROM credential_cleanup_intents WHERE id = ?1",
            params![id],
        )
        .map_err(|error| error.to_string())?;
    Ok(())
}

pub fn record_failure(
    connection: &Connection,
    id: &str,
    error: &str,
    now: &str,
) -> LocalResult<()> {
    connection
        .execute(
            "UPDATE credential_cleanup_intents
             SET attempt = attempt + 1, last_error = ?2, updated_at = ?3
             WHERE id = ?1",
            params![id, error, now],
        )
        .map_err(|error| error.to_string())?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn cleanup_protects_both_model_and_remote_token_references() {
        let connection = Connection::open_in_memory().unwrap();
        connection
            .execute_batch(
                "CREATE TABLE ai_model_configs (api_key_ref TEXT);
                 CREATE TABLE app_settings (api_token_ref TEXT);
                 INSERT INTO ai_model_configs VALUES ('synthetic-model-ref');
                 INSERT INTO app_settings VALUES ('synthetic-token-ref');",
            )
            .unwrap();
        assert!(is_referenced(&connection, "synthetic-model-ref").unwrap());
        assert!(is_referenced(&connection, "synthetic-token-ref").unwrap());
        assert!(!is_referenced(&connection, "synthetic-orphan-ref").unwrap());
    }
}
