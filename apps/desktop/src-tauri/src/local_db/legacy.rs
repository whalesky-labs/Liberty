use crate::{
    infrastructure::repositories::ai_summary_runs,
    local_db::{jobs, jobs_root, LocalResult},
};
use rusqlite::{Connection, OptionalExtension};
use std::{fs, path::Path};
use tauri::AppHandle;

use super::{model::*, progress};

pub(crate) fn import_legacy_jobs(app: &AppHandle, conn: &mut Connection) -> LocalResult<()> {
    import_legacy_jobs_from_root(&jobs_root(app)?, conn)
}

fn import_legacy_jobs_from_root(root: &Path, conn: &mut Connection) -> LocalResult<()> {
    let imported = conn
        .query_row(
            "SELECT value FROM app_meta WHERE key = 'legacy_jobs_imported'",
            [],
            |row| row.get::<_, String>(0),
        )
        .optional()
        .map_err(|err| err.to_string())?;

    if imported.as_deref() == Some("1") {
        return Ok(());
    }

    let tx = conn.transaction().map_err(|err| err.to_string())?;

    for entry in fs::read_dir(root).map_err(|err| err.to_string())? {
        let entry = entry.map_err(|err| err.to_string())?;
        let path = entry.path();

        if !path.is_dir() {
            continue;
        }

        if let Ok(job) = read_legacy_job(&path) {
            let protected = tx
                .query_row(
                    "SELECT EXISTS(SELECT 1 FROM jobs WHERE id = ?1)
                        OR EXISTS(SELECT 1 FROM job_deletion_ops WHERE job_id = ?1)",
                    [&job.id],
                    |row| row.get::<_, bool>(0),
                )
                .map_err(|err| err.to_string())?;
            if protected {
                continue;
            }
            jobs::save_job_snapshot_tx(&tx, &job)?;

            if has_summary_content(&job.summary) {
                let imported_run = imported_summary_run(&job)?;
                let summary_exists = tx
                    .query_row(
                        "SELECT EXISTS(SELECT 1 FROM ai_summary_runs WHERE id = ?1)",
                        [&imported_run.id],
                        |row| row.get::<_, bool>(0),
                    )
                    .map_err(|err| err.to_string())?;
                if summary_exists {
                    return Err("旧任务摘要标识已存在，未导入任何任务。".into());
                }
                ai_summary_runs::save_summary_run_tx(&tx, &imported_run)?;
            }
        }
    }

    tx.execute(
        "INSERT INTO app_meta (key, value) VALUES ('legacy_jobs_imported', '1')
         ON CONFLICT(key) DO UPDATE SET value = excluded.value",
        [],
    )
    .map_err(|err| err.to_string())?;
    tx.commit().map_err(|err| err.to_string())
}

fn read_legacy_job(job_dir: &Path) -> LocalResult<MeetingJob> {
    let job_path = job_dir.join("job.json");
    let raw = fs::read(&job_path).map_err(|err| err.to_string())?;
    let mut job: MeetingJob = serde_json::from_slice(&raw).map_err(|err| err.to_string())?;

    if let Ok(progress) = progress::read_runner_progress(&job_dir.join("progress.json")) {
        progress::apply_progress_snapshot(&mut job, &progress);
    }

    if let Ok(result) =
        progress::read_legacy_json::<LegacyRunnerResult>(&job_dir.join("result.json"))
    {
        if job.transcript_segments.is_empty() {
            job.transcript_segments = result.transcript_segments.unwrap_or_default();
        }
        if job.speaker_segments.is_empty() {
            job.speaker_segments = result.speaker_segments.unwrap_or_default();
        }
        if job.duration_minutes == 0 {
            job.duration_minutes = result.duration_minutes.unwrap_or(0);
        }
        if job.failure_reason.is_none() {
            job.failure_reason = result.failure_reason;
        }
    }

    job.process_log = fs::read_to_string(job_dir.join("process.log"))
        .ok()
        .map(|content| content.trim().to_string())
        .filter(|content| !content.is_empty());
    job.summary_runs = Vec::new();
    job.active_summary_run_id = None;

    Ok(job)
}

fn has_summary_content(summary: &MeetingSummary) -> bool {
    !summary.overview.trim().is_empty()
        || !summary.topics.is_empty()
        || !summary.decisions.is_empty()
        || !summary.action_items.is_empty()
        || !summary.risks.is_empty()
        || !summary.follow_ups.is_empty()
}

fn imported_summary_run(job: &MeetingJob) -> LocalResult<AiSummaryRun> {
    let id = format!("imported-summary-{}", job.id);
    let result = AiSummaryResult {
        title: job.title.clone(),
        overview: job.summary.overview.clone(),
        topics: job.summary.topics.clone(),
        decisions: job.summary.decisions.clone(),
        action_items: job
            .summary
            .action_items
            .iter()
            .map(|item| AiSummaryActionItem {
                task: item.clone(),
                owner: String::new(),
                due_date: String::new(),
            })
            .collect(),
        risks: job.summary.risks.clone(),
        follow_ups: job.summary.follow_ups.clone(),
    };
    let minutes_payload = crate::local_export::source::reconstruct_verified_payload(
        job,
        &result,
        &[],
        "",
        Some(id.clone()),
    )?;

    Ok(AiSummaryRun {
        id,
        job_id: job.id.clone(),
        model_config_id: String::new(),
        template_id: String::new(),
        include_speaker: job.enable_speaker,
        include_timestamp: true,
        extra_instructions: String::new(),
        status: "completed".into(),
        error_message: None,
        prompt_preview: Some("Imported from legacy JSON task".into()),
        raw_response: None,
        result: Some(result),
        minutes_payload: Some(minutes_payload),
        created_at: job.created_at.clone(),
        updated_at: job.created_at.clone(),
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::domain::transcript::TranscriptSegment;

    struct LegacyTestDirectory(std::path::PathBuf);

    impl LegacyTestDirectory {
        fn new() -> Self {
            let mut random = [0_u8; 16];
            ring::rand::SecureRandom::fill(&ring::rand::SystemRandom::new(), &mut random).unwrap();
            let suffix = random
                .iter()
                .map(|value| format!("{value:02x}"))
                .collect::<String>();
            let path = std::env::temp_dir().join(format!("liberty-legacy-{suffix}"));
            fs::create_dir(&path).unwrap();
            Self(path)
        }

        fn path(&self) -> &Path {
            &self.0
        }
    }

    impl Drop for LegacyTestDirectory {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    fn write_legacy_snapshot(root: &Path, job: &MeetingJob) {
        let directory = root.join(&job.id);
        fs::create_dir_all(&directory).unwrap();
        fs::write(directory.join("job.json"), serde_json::to_vec(job).unwrap()).unwrap();
    }

    #[test]
    fn legacy_import_preserves_existing_sqlite_and_persisted_deletions_after_reopen() {
        let directory = LegacyTestDirectory::new();
        let root = directory.path().join("jobs");
        let database = directory.path().join("synthetic.sqlite3");
        fs::create_dir(&root).unwrap();
        let mut connection = Connection::open(&database).unwrap();
        crate::local_db::schema::apply_test_schema(&connection).unwrap();
        let mut existing = MeetingJob {
            id: "job-1700000000000-701".into(),
            title: "Authoritative SQLite result".into(),
            source: "local".into(),
            created_at: "2026-11-20T00:00:00Z".into(),
            lang: "zh".into(),
            overall_status: "completed".into(),
            asr_status: "completed".into(),
            summary_status: "completed".into(),
            process_log: Some("Authoritative process log".into()),
            source_files: vec![MeetingSourceFile {
                id: "synthetic-source-701".into(),
                name: "synthetic.wav".into(),
                kind: "audio".into(),
                ..MeetingSourceFile::default()
            }],
            transcript_segments: vec![TranscriptSegment {
                id: "synthetic-transcript-701".into(),
                start_ms: 0,
                end_ms: 1000,
                speaker: None,
                text: "Authoritative transcript".into(),
            }],
            summary: MeetingSummary {
                overview: "Authoritative summary".into(),
                ..MeetingSummary::default()
            },
            ..MeetingJob::default()
        };
        let summary = imported_summary_run(&existing).unwrap();
        existing.active_summary_run_id = Some(summary.id.clone());
        let transaction = connection.transaction().unwrap();
        jobs::save_job_snapshot_tx(&transaction, &existing).unwrap();
        ai_summary_runs::save_summary_run_tx(&transaction, &summary).unwrap();
        transaction
            .execute(
                "INSERT INTO job_runs(job_id,attempt_id,lease_token,status) VALUES(?1,3,4,'completed')",
                [&existing.id],
            )
            .unwrap();
        transaction
            .execute(
                "INSERT INTO job_events(id,job_id,event_type,message,created_at)
                 VALUES('synthetic-event-701',?1,'completed','Authoritative event','2026-11-20T00:00:00Z')",
                [&existing.id],
            )
            .unwrap();
        let deleted_id = "job-1700000000000-702";
        transaction
            .execute(
                "INSERT INTO job_deletion_ops(operation_id,job_id,trash_name,phase,created_at_ms,updated_at_ms)
                 VALUES('synthetic-delete-702',?1,'synthetic-trash-702','database_deleted',1,2)",
                [deleted_id],
            )
            .unwrap();
        transaction.commit().unwrap();
        let before =
            serde_json::to_value(jobs::load_job_snapshot(&connection, &existing.id).unwrap())
                .unwrap();
        let stale = MeetingJob {
            id: existing.id.clone(),
            title: "Stale JSON result".into(),
            source: "local".into(),
            created_at: existing.created_at.clone(),
            overall_status: "queued".into(),
            asr_status: "queued".into(),
            ..MeetingJob::default()
        };
        write_legacy_snapshot(&root, &stale);
        let deleted = MeetingJob {
            id: deleted_id.into(),
            ..stale.clone()
        };
        write_legacy_snapshot(&root, &deleted);
        let missing = MeetingJob {
            id: "job-1700000000000-703".into(),
            title: "Missing task imported".into(),
            ..stale
        };
        write_legacy_snapshot(&root, &missing);
        import_legacy_jobs_from_root(&root, &mut connection).unwrap();
        for _ in 0..3 {
            drop(connection);
            connection = Connection::open(&database).unwrap();
            let after =
                serde_json::to_value(jobs::load_job_snapshot(&connection, &existing.id).unwrap())
                    .unwrap();
            assert_eq!(after, before);
            let run = connection
                .query_row(
                    "SELECT attempt_id,lease_token,status FROM job_runs WHERE job_id=?1",
                    [&existing.id],
                    |row| {
                        Ok((
                            row.get::<_, i64>(0)?,
                            row.get::<_, i64>(1)?,
                            row.get::<_, String>(2)?,
                        ))
                    },
                )
                .unwrap();
            assert_eq!(run, (3, 4, "completed".into()));
            assert_eq!(
                connection
                    .query_row(
                        "SELECT message FROM job_events WHERE id='synthetic-event-701'",
                        [],
                        |row| row.get::<_, String>(0),
                    )
                    .unwrap(),
                "Authoritative event"
            );
            assert!(jobs::load_job_snapshot(&connection, deleted_id).is_err());
            assert_eq!(
                jobs::load_job_snapshot(&connection, &missing.id)
                    .unwrap()
                    .title,
                missing.title
            );
            import_legacy_jobs_from_root(&root, &mut connection).unwrap();
        }
    }

    #[test]
    fn legacy_import_marker_failure_rolls_back_new_tasks_and_retries() {
        let directory = LegacyTestDirectory::new();
        let root = directory.path().join("jobs");
        fs::create_dir(&root).unwrap();
        let database = directory.path().join("synthetic.sqlite3");
        let mut connection = Connection::open(&database).unwrap();
        crate::local_db::schema::apply_test_schema(&connection).unwrap();
        let job = MeetingJob {
            id: "job-1700000000000-711".into(),
            title: "Synthetic imported task".into(),
            created_at: "2026-11-20T00:00:00Z".into(),
            summary: MeetingSummary {
                overview: "Synthetic imported summary".into(),
                ..MeetingSummary::default()
            },
            ..MeetingJob::default()
        };
        write_legacy_snapshot(&root, &job);
        connection
            .execute_batch(
                "CREATE TRIGGER reject_import_marker BEFORE INSERT ON app_meta
                 WHEN NEW.key='legacy_jobs_imported'
                 BEGIN SELECT RAISE(ABORT,'synthetic import marker failure'); END;",
            )
            .unwrap();
        assert!(import_legacy_jobs_from_root(&root, &mut connection).is_err());
        drop(connection);
        connection = Connection::open(&database).unwrap();
        for table in [
            "jobs",
            "job_source_files",
            "transcript_segments",
            "ai_summary_runs",
        ] {
            let count = connection
                .query_row(&format!("SELECT COUNT(*) FROM {table}"), [], |row| {
                    row.get::<_, i64>(0)
                })
                .unwrap();
            assert_eq!(count, 0, "{table} changed after failed import");
        }
        assert_eq!(
            connection
                .query_row(
                    "SELECT COUNT(*) FROM app_meta WHERE key='legacy_jobs_imported'",
                    [],
                    |row| row.get::<_, i64>(0),
                )
                .unwrap(),
            0
        );
        connection
            .execute_batch("DROP TRIGGER reject_import_marker")
            .unwrap();
        import_legacy_jobs_from_root(&root, &mut connection).unwrap();
        drop(connection);
        connection = Connection::open(&database).unwrap();
        let imported = jobs::load_job_snapshot(&connection, &job.id).unwrap();
        assert_eq!(imported.title, job.title);
        assert_eq!(imported.summary_runs.len(), 1);
        import_legacy_jobs_from_root(&root, &mut connection).unwrap();
        assert_eq!(
            jobs::load_job_snapshot(&connection, &job.id)
                .unwrap()
                .summary_runs
                .len(),
            1
        );
    }

    #[test]
    fn legacy_import_summary_identity_collision_preserves_existing_owner() {
        let directory = LegacyTestDirectory::new();
        let root = directory.path().join("jobs");
        fs::create_dir(&root).unwrap();
        let database = directory.path().join("synthetic.sqlite3");
        let mut connection = Connection::open(&database).unwrap();
        crate::local_db::schema::apply_test_schema(&connection).unwrap();
        let existing = MeetingJob {
            id: "job-1700000000000-721".into(),
            title: "Authoritative SQLite task".into(),
            ..MeetingJob::default()
        };
        let imported = MeetingJob {
            id: "job-1700000000000-722".into(),
            title: "Synthetic old task".into(),
            summary: MeetingSummary {
                overview: "Synthetic imported summary".into(),
                ..MeetingSummary::default()
            },
            ..MeetingJob::default()
        };
        write_legacy_snapshot(&root, &imported);
        let mut conflicting = imported_summary_run(&imported).unwrap();
        conflicting.job_id.clone_from(&existing.id);
        conflicting.result.as_mut().unwrap().overview = "Authoritative existing summary".into();
        let transaction = connection.transaction().unwrap();
        jobs::save_job_snapshot_tx(&transaction, &existing).unwrap();
        ai_summary_runs::save_summary_run_tx(&transaction, &conflicting).unwrap();
        transaction.commit().unwrap();
        let before =
            serde_json::to_value(jobs::load_job_snapshot(&connection, &existing.id).unwrap())
                .unwrap();
        assert!(import_legacy_jobs_from_root(&root, &mut connection).is_err());
        drop(connection);
        connection = Connection::open(&database).unwrap();
        assert_eq!(
            serde_json::to_value(jobs::load_job_snapshot(&connection, &existing.id).unwrap())
                .unwrap(),
            before
        );
        assert!(jobs::load_job_snapshot(&connection, &imported.id).is_err());
        assert_eq!(
            connection
                .query_row(
                    "SELECT COUNT(*) FROM app_meta WHERE key='legacy_jobs_imported'",
                    [],
                    |row| row.get::<_, i64>(0),
                )
                .unwrap(),
            0
        );
        connection
            .execute("DELETE FROM ai_summary_runs WHERE id=?1", [&conflicting.id])
            .unwrap();
        import_legacy_jobs_from_root(&root, &mut connection).unwrap();
        assert_eq!(
            jobs::load_job_snapshot(&connection, &imported.id)
                .unwrap()
                .summary_runs
                .len(),
            1
        );
    }

    #[test]
    fn imported_completed_run_persists_verified_payload_with_snapshot_speakers() {
        let job = MeetingJob {
            id: "legacy-job".into(),
            title: "周会".into(),
            enable_speaker: true,
            transcript_segments: vec![
                TranscriptSegment {
                    speaker: Some("人员A".into()),
                    ..TranscriptSegment::default()
                },
                TranscriptSegment {
                    speaker: Some("人员B".into()),
                    ..TranscriptSegment::default()
                },
            ],
            summary: MeetingSummary {
                overview: "发言内容\n【部门】：人员A\n上周总结：\n1、A事项".into(),
                ..MeetingSummary::default()
            },
            ..MeetingJob::default()
        };

        let run = imported_summary_run(&job).unwrap();
        let payload = run.minutes_payload.expect("legacy payload");
        let speakers = payload
            .speaker_reports
            .iter()
            .map(|report| report.resolved_name.as_str())
            .collect::<Vec<_>>();

        assert_eq!(run.status, "completed");
        assert_eq!(
            payload.source_summary_run_id.as_deref(),
            Some("imported-summary-legacy-job")
        );
        assert!(speakers.contains(&"人员A"));
        assert!(speakers.contains(&"人员B"));
        assert_eq!(
            payload
                .speaker_reports
                .iter()
                .find(|report| report.resolved_name == "人员B")
                .map(|report| report.match_status.as_str()),
            Some("missing_from_ai")
        );
    }
}
