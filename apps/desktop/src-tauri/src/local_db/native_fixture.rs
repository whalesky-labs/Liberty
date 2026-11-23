//! Cross-language fixture generation. Only compiled as a test; never reads a
//! user's profile and refuses to overwrite any output file.

use std::{fs::OpenOptions, io::Write, path::PathBuf};

use rusqlite::{params, Connection};
use serde_json::json;

use super::jobs::{
    attach_source_files, load_job_snapshot, query_job_summaries, save_job_snapshot_tx,
};
use super::{MeetingJob, MeetingSourceFile, TranscriptSegment};
use crate::domain::asr::{AsrBackend, DiarizationStatus};

#[path = "native_gameplay_fixture.rs"]
mod native_gameplay_fixture;
#[path = "native_pet_box_fixture.rs"]
mod native_pet_box_fixture;
#[path = "native_pet_core_fixture.rs"]
mod native_pet_core_fixture;
#[path = "native_pet_redemption_fixture.rs"]
mod native_pet_redemption_fixture;
#[path = "native_pet_store_fixture.rs"]
mod native_pet_store_fixture;

#[test]
#[ignore = "explicit release-mode comparison via pnpm native:bench:reads"]
fn benchmark_native_reads() {
    let directory = PathBuf::from(std::env::var_os("LIBERTY_NATIVE_FIXTURE_DIR").unwrap());
    let database = directory.join("reads.sqlite3");
    assert!(directory.is_absolute());
    OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&database)
        .unwrap();
    let mut connection = Connection::open(&database).unwrap();
    super::schema::apply_test_schema(&connection).unwrap();
    let transaction = connection.transaction().unwrap();
    for index in 0..1000 {
        let mut job = MeetingJob {
            id: format!("job-1700000000000-{index}"),
            source: "local".into(),
            title: format!("Synthetic meeting {index}"),
            overall_status: "completed".into(),
            source_files: vec![MeetingSourceFile {
                id: format!("source-{index}"),
                name: format!("synthetic-{index}.wav"),
                kind: "audio".into(),
                size_label: "1 MB".into(),
                path: None,
            }],
            created_at: "2026-11-01T00:00:00.000Z".into(),
            ..Default::default()
        };
        if index == 0 {
            job.transcript_segments = (0..10000)
                .map(|segment| TranscriptSegment {
                    id: format!("segment-{segment}"),
                    start_ms: segment * 1000,
                    end_ms: (segment + 1) * 1000,
                    speaker: None,
                    text: format!("合成会议记录 {segment}"),
                })
                .collect();
        }
        save_job_snapshot_tx(&transaction, &job).unwrap();
    }
    transaction.commit().unwrap();
    drop(connection);
    let connection =
        Connection::open_with_flags(database, rusqlite::OpenFlags::SQLITE_OPEN_READ_ONLY).unwrap();
    for operation in ["queue", "transcript"] {
        let mut samples = Vec::new();
        for iteration in 0..22 {
            let start = std::time::Instant::now();
            match operation {
                "queue" => {
                    let mut jobs = query_job_summaries(&connection).unwrap();
                    attach_source_files(&connection, &mut jobs).unwrap();
                    assert_eq!(jobs.len(), 1000);
                    assert_eq!(
                        jobs.iter().map(|job| job.source_files.len()).sum::<usize>(),
                        1000
                    );
                }
                _ => {
                    let job = load_job_snapshot(&connection, "job-1700000000000-0").unwrap();
                    assert_eq!(job.transcript_segments.len(), 10000);
                    assert_eq!(
                        job.transcript_segments.last().unwrap().text,
                        "合成会议记录 9999"
                    );
                }
            }
            if iteration >= 2 {
                samples.push(start.elapsed().as_micros());
            }
        }
        samples.sort_unstable();
        println!(
            "rust {operation}: p50={}us p95={}us n=20",
            samples[9], samples[18]
        );
    }
}

#[test]
#[ignore = "explicit cross-language fixture export via pnpm native:compat"]
fn export_native_compatibility_fixture() {
    let directory = PathBuf::from(
        std::env::var_os("LIBERTY_NATIVE_FIXTURE_DIR").expect("explicit fixture directory"),
    );
    assert!(directory.is_absolute() && directory.is_dir());
    let mut members_book = rust_xlsxwriter::Workbook::new();
    let sheet = members_book.add_worksheet();
    sheet.set_name("人员信息").unwrap();
    for (column, text) in ["姓名", "部门", "排序", "是否设置会议记录人"]
        .iter()
        .enumerate()
    {
        sheet.write_string(0, column as u16, *text).unwrap();
    }
    sheet.write_string(1, 0, "合成人员").unwrap();
    sheet.write_string(1, 1, "A&B <研发>").unwrap();
    sheet.write_number(1, 2, -7).unwrap();
    sheet.write_string(1, 3, "是").unwrap();
    sheet.write_string(2, 0, "Synthetic second").unwrap();
    sheet.write_string(2, 1, "").unwrap();
    sheet.write_number(2, 2, 9).unwrap();
    sheet.write_string(2, 3, "否").unwrap();
    members_book
        .save(directory.join("rust-members.xlsx"))
        .unwrap();
    let database = directory.join("liberty.sqlite3");
    OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&database)
        .expect("new fixture database");
    let mut connection = Connection::open(&database).expect("fixture connection");
    super::schema::apply_test_schema(&connection).expect("real migrations");
    let transaction = connection.transaction().expect("fixture transaction");
    for index in 0..15 {
        let id = format!("job-1700000000000-{index}");
        let status = match index % 4 {
            0 => "completed",
            1 => "queued",
            2 => "failed",
            _ => "speaker_processing",
        };
        let transcript = vec![TranscriptSegment {
            id: "segment-1".into(),
            start_ms: 0,
            end_ms: 90_000,
            speaker: None,
            text: "合成验收文本 / synthetic acceptance transcript".into(),
        }];
        let mut speakers = transcript.clone();
        speakers[0].speaker = Some("speaker-0".into());
        let job = MeetingJob {
            id: id.clone(),
            source: "local".into(),
            title: format!("验收会议 {index:02}"),
            source_files: vec![MeetingSourceFile {
                id: format!("source-{index:02}"),
                name: format!("synthetic-{index:02}.wav"),
                path: None,
                kind: "audio".into(),
                size_label: "1 MB".into(),
            }],
            duration_minutes: 2,
            created_at: format!("2026-11-{:02}T00:00:00.000Z", index + 1),
            lang: "zh".into(),
            enable_speaker: true,
            runner_protocol_version: Some(2),
            asr_backend: AsrBackend::Funasr,
            diarization_status: if index % 2 == 0 {
                DiarizationStatus::Completed
            } else {
                DiarizationStatus::LegacyUnverified
            },
            upload_status: "completed".into(),
            asr_status: status.into(),
            summary_status: "idle".into(),
            overall_status: status.into(),
            transcript_segments: transcript,
            speaker_segments: speakers,
            ..MeetingJob::default()
        };
        save_job_snapshot_tx(&transaction, &job).expect("real repository write");
    }
    transaction.execute_batch(
        "UPDATE jobs SET processing_duration_seconds = 120, last_exported_at = created_at WHERE id = 'job-1700000000000-0';
         UPDATE jobs SET warnings_json = '[{}]' WHERE id = 'job-1700000000000-1';
         INSERT INTO ai_summary_runs (id, job_id, include_speaker, include_timestamp, extra_instructions, status, result_json, created_at, updated_at)
         VALUES ('synthetic-summary', 'job-1700000000000-0', 1, 1, '', 'completed', '{}', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z');
         INSERT INTO pet_profile (id, name, experience, stage, current_mood, created_at, updated_at)
         VALUES ('default-pet', '验收伙伴', 1200, 'first_meet', 'happy', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z');"
    ).expect("synthetic dashboard details");
    transaction
        .execute(
            "INSERT INTO job_deletion_ops(operation_id, job_id, trash_name, phase, created_at_ms, updated_at_ms)
             VALUES ('delete-fixture', ?1, 'fixture-trash', 'prepared', 0, 0)",
            params!["job-1700000000000-14"],
        )
        .expect("pending deletion");
    transaction.commit().expect("fixture commit");
    connection.execute_batch(
        r#"UPDATE jobs SET duration_minutes=0, summary_status='queued' WHERE id='job-1700000000000-12';
        UPDATE jobs SET asr_backend='future-backend', diarization_status='future-status', hotwords_json='[null]', export_formats_json='null' WHERE id='job-1700000000000-2';
        UPDATE jobs SET process_log='start
warning: synthetic diagnostic
completed', active_summary_run_id='synthetic-failed' WHERE id='job-1700000000000-0';
        UPDATE ai_summary_runs SET result_json='{"overview":"Synthetic summary","actionItems":[{"task":"Follow up","owner":" Alice ","dueDate":"tomorrow"}]}',
         minutes_payload_json='{"meetingInfoSource":"user","meetingInfo":{"meetingName":"Synthetic minutes"},"speakerReports":[{}]}'
         WHERE id='synthetic-summary';
        INSERT INTO ai_summary_runs(id,job_id,status,created_at,updated_at,result_json) VALUES
         ('synthetic-failed','job-1700000000000-0','failed','2026-11-02T00:00:00Z','2026-11-02T00:00:00Z',NULL),
         ('synthetic-corrupt','job-1700000000000-0','completed','2026-11-03T00:00:00Z','2026-11-03T00:00:00Z','{"topics":[null]}'),
         ('synthetic-older','job-1700000000000-4','completed','2026-11-01T00:00:00Z','2026-11-01T00:00:00Z','{"overview":"Older selected result"}'),
         ('synthetic-newer','job-1700000000000-4','completed','2026-11-02T00:00:00Z','2026-11-02T00:00:00Z','{"overview":"Newer result"}');
        UPDATE jobs SET active_summary_run_id='synthetic-older' WHERE id='job-1700000000000-4';"#,
    ).expect("synthetic persisted detail edge cases");
    let preferences =
        crate::infrastructure::repositories::settings::load_ui_preferences(&connection)
            .expect("real UI preferences");
    let mut jobs = query_job_summaries(&connection).expect("real queue projection");
    attach_source_files(&connection, &mut jobs).expect("real source projection");
    let mut results = Vec::new();
    for job in &jobs {
        let detail = load_job_snapshot(&connection, &job.id).expect("real detail projection");
        results.push(json!({
            "job": detail,
            "transcriptSegments": detail.transcript_segments,
            "speakerSegments": detail.speaker_segments,
            "summary": detail.summary,
            "summaryRuns": detail.summary_runs,
            "processLog": detail.process_log,
        }));
    }
    let dashboard = crate::infrastructure::repositories::dashboard::get_overview(
        &connection,
        crate::infrastructure::repositories::dashboard::DashboardRange::All,
    )
    .expect("real dashboard projection");
    let mut levels = Vec::new();
    let mut experience = 0;
    for level in 1..=255 {
        for delta in [-1_i64, 0, 1] {
            let snapshot = super::pet_leveling::level_snapshot_from_experience(experience + delta);
            levels.push(json!({
                "experience": experience + delta,
                "level": snapshot.level,
                "current": snapshot.current_level_exp,
                "next": snapshot.next_level_required,
                "percent": (snapshot.progress_ratio * 100.0).round() as i64,
            }));
        }
        experience += super::pet_leveling::required_exp_for_level(level);
    }
    let timestamps = [
        -1_i64,
        0,
        1,
        1_000,
        1_000_000,
        100_000_000,
        1_793_000_000_123_456_789,
    ]
    .map(|nanos| {
        let instant = jiff::Timestamp::from_nanosecond(i128::from(nanos)).unwrap();
        json!({"nanos":nanos, "text":crate::infrastructure::time::add_seconds(instant, 0).unwrap()})
    });
    let expected = json!({"jobs": jobs, "results": results, "dashboard": dashboard, "levels": levels, "preferences":preferences, "timestamps": timestamps});
    OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(directory.join("expected.json"))
        .expect("new expected projection")
        .write_all(serde_json::to_string_pretty(&expected).unwrap().as_bytes())
        .expect("write expected projection");
    assert_eq!(jobs.len(), 14);
    connection.close().expect("close fixture");
    export_gameplay_compatibility_fixture(&directory);
    native_gameplay_fixture::export(&directory);
    export_checkin_compatibility_fixture(&directory);
    native_pet_store_fixture::export(&directory);
    native_pet_box_fixture::export(&directory);
    native_pet_core_fixture::export(&directory);
    native_pet_redemption_fixture::export(&directory);
}

#[test]
#[ignore = "explicit isolated check-in fixture export"]
fn export_native_checkin_compatibility_fixture() {
    let directory = PathBuf::from(
        std::env::var_os("LIBERTY_NATIVE_FIXTURE_DIR").expect("explicit fixture directory"),
    );
    assert!(directory.is_absolute() && directory.is_dir());
    export_checkin_compatibility_fixture(&directory);
}

fn checkin_snapshot_queries() -> serde_json::Value {
    json!({
        "profile": "SELECT id,name,level,experience,stage,current_mood,created_at,updated_at FROM pet_profile ORDER BY id",
        "settings": "SELECT pet_id,desktop_enabled,always_on_top,muted,focus_mode_enabled,proactive_level,last_window_x,last_window_y,updated_at FROM pet_settings ORDER BY pet_id",
        "checkins": "SELECT id,pet_id,check_in_date,streak_count,cycle_day,reward_lp,growth_value,reward_items_json,created_at FROM pet_daily_check_ins ORDER BY pet_id,check_in_date",
        "events": "SELECT id,pet_id,event_type,event_source,event_value,event_time,metadata FROM pet_event_ledger ORDER BY pet_id,event_time,event_type,event_source,id",
        "economy": "SELECT id,pet_id,entry_type,currency_key,amount,balance_after,source_type,source_key,metadata,created_at FROM pet_economy_ledger ORDER BY pet_id,currency_key,source_type,source_key",
        "wallets": "SELECT pet_id,currency_key,balance,lifetime_earned,lifetime_spent,updated_at FROM pet_wallets ORDER BY pet_id,currency_key",
        "inventory": "SELECT id,pet_id,item_key,item_type,slot,quantity,equipped,source,purchased_at,updated_at FROM pet_inventory ORDER BY pet_id,item_key",
        "cosmetics": "SELECT id,pet_id,cosmetic_type,cosmetic_key,unlocked_at,equipped FROM pet_cosmetic_unlocks ORDER BY pet_id,cosmetic_type,cosmetic_key,id",
        "counters": "SELECT pet_id,counter_key,counter_value,last_event_key,updated_at FROM pet_milestone_counters ORDER BY pet_id,counter_key",
        "limits": "SELECT pet_id,item_key,limit_date,free_claimed,updated_at FROM pet_store_daily_limits ORDER BY pet_id,item_key,limit_date"
    })
}

fn checkin_snapshot(connection: &Connection) -> serde_json::Value {
    use rusqlite::types::ValueRef;

    let mut snapshot = serde_json::Map::new();
    for (name, query) in checkin_snapshot_queries().as_object().unwrap() {
        let mut statement = connection.prepare(query.as_str().unwrap()).unwrap();
        let column_count = statement.column_count();
        let rows = statement
            .query_map([], |row| {
                (0..column_count)
                    .map(|column| {
                        Ok(match row.get_ref(column)? {
                            ValueRef::Null => serde_json::Value::Null,
                            ValueRef::Integer(value) => json!(value),
                            ValueRef::Real(value) => json!(value),
                            ValueRef::Text(value) => json!(std::str::from_utf8(value).unwrap()),
                            ValueRef::Blob(_) => panic!("unexpected check-in blob"),
                        })
                    })
                    .collect::<rusqlite::Result<Vec<_>>>()
            })
            .unwrap()
            .collect::<rusqlite::Result<Vec<_>>>()
            .unwrap();
        snapshot.insert(name.clone(), json!(rows));
    }
    snapshot.into()
}

fn checkin_action(
    connection: &mut Connection,
    input: &serde_json::Value,
) -> Result<serde_json::Value, String> {
    use crate::infrastructure::repositories::{pet, pet_check_in, pet_store};

    let transaction = connection.transaction().map_err(|err| err.to_string())?;
    pet::ensure_default_exists_tx(&transaction)?;
    pet_store::ensure_store_defaults_tx(&transaction, input["now"].as_str().unwrap())?;
    let profile = pet::load_profile_tx(&transaction)?;
    match input["operation"].as_str().unwrap() {
        "state" => {
            transaction.commit().map_err(|err| err.to_string())?;
            pet::reconcile_profile_leveling(connection)?;
            pet_check_in::daily_check_in_state(connection, pet::load_profile(connection)?)
                .map(|value| json!(value))
        }
        "claim" => {
            let (entry, duplicate) = pet_check_in::claim_daily_check_in_tx(
                &transaction,
                &profile,
                input["now"].as_str().unwrap(),
            )?;
            transaction.commit().map_err(|err| err.to_string())?;
            pet::reconcile_profile_leveling(connection)?;
            pet_check_in::claim_result(connection, pet::load_profile(connection)?, entry, duplicate)
                .map(|value| json!(value))
        }
        "makeup" => {
            let entry = pet_check_in::repair_daily_check_in_tx(
                &transaction,
                &profile,
                input["now"].as_str().unwrap(),
            )?;
            transaction.commit().map_err(|err| err.to_string())?;
            pet::reconcile_profile_leveling(connection)?;
            pet_check_in::makeup_result(connection, pet::load_profile(connection)?, entry)
                .map(|value| json!(value))
        }
        _ => panic!("unknown check-in operation"),
    }
}

fn checkin_input(
    name: &str,
    operation: &str,
    local: &str,
    rejected: bool,
    fail_write: bool,
    duplicate: bool,
) -> serde_json::Value {
    let local: jiff::civil::DateTime = local.parse().unwrap();
    let instant = local
        .to_zoned(jiff::tz::TimeZone::system())
        .unwrap()
        .timestamp();
    json!({
        "name":name,"operation":operation,"date":local.date().to_string(),
        "now":crate::infrastructure::time::add_seconds(instant,0).unwrap(),
        "rejected":rejected,"failWrite":fail_write,"duplicate":duplicate,
        "unchanged":duplicate && name != "replay-day-01-after-day-02"
    })
}

fn checkin_replay_actions() -> Vec<serde_json::Value> {
    let mut actions = vec![
        checkin_input(
            "initial-state",
            "state",
            "2026-07-01T23:59:56",
            false,
            false,
            false,
        ),
        checkin_input(
            "makeup-without-history",
            "makeup",
            "2026-07-01T23:59:57",
            true,
            false,
            false,
        ),
    ];
    for day in 1..=21 {
        let date = format!("2026-07-{day:02}");
        let before = if day == 1 { "23:59:58" } else { "00:00:00" };
        let claim = if day == 1 { "23:59:59" } else { "00:00:01" };
        actions.push(checkin_input(
            &format!("before-day-{day:02}"),
            "state",
            &format!("{date}T{before}"),
            false,
            false,
            false,
        ));
        if day == 14 {
            actions.push(checkin_input(
                "claim-write-failure",
                "claim",
                &format!("{date}T{claim}"),
                true,
                true,
                false,
            ));
        }
        actions.push(checkin_input(
            &format!("claim-day-{day:02}"),
            "claim",
            &format!("{date}T{claim}"),
            false,
            false,
            false,
        ));
        actions.push(checkin_input(
            &format!("duplicate-day-{day:02}"),
            "claim",
            &format!("{date}T{claim}"),
            false,
            false,
            true,
        ));
        if day == 1 {
            actions.push(checkin_input(
                "makeup-after-claim-rejected",
                "makeup",
                &format!("{date}T{claim}"),
                true,
                false,
                false,
            ));
        }
        if day == 2 {
            actions.push(checkin_input(
                "replay-day-01-after-day-02",
                "claim",
                "2026-07-01T23:59:59",
                false,
                false,
                true,
            ));
        }
    }
    for (name, operation, local, rejected, fail_write, duplicate) in [
        (
            "missed-one-day",
            "state",
            "2026-07-23T00:00:00",
            false,
            false,
            false,
        ),
        (
            "makeup-write-failure",
            "makeup",
            "2026-07-23T00:00:01",
            true,
            true,
            false,
        ),
        (
            "makeup-day-22",
            "makeup",
            "2026-07-23T00:00:01",
            false,
            false,
            false,
        ),
        (
            "claim-day-23",
            "claim",
            "2026-07-23T00:00:02",
            false,
            false,
            false,
        ),
        (
            "duplicate-day-23",
            "claim",
            "2026-07-23T00:00:02",
            false,
            false,
            true,
        ),
        (
            "missed-two-days",
            "state",
            "2026-07-26T00:00:00",
            false,
            false,
            false,
        ),
        (
            "makeup-day-24",
            "makeup",
            "2026-07-26T00:00:01",
            false,
            false,
            false,
        ),
        (
            "remaining-missed-day",
            "state",
            "2026-07-26T00:00:02",
            false,
            false,
            false,
        ),
        (
            "makeup-no-ticket-rejected",
            "makeup",
            "2026-07-26T00:00:03",
            true,
            false,
            false,
        ),
        (
            "claim-day-26-reset",
            "claim",
            "2026-07-26T00:00:04",
            false,
            false,
            false,
        ),
        (
            "duplicate-day-26",
            "claim",
            "2026-07-26T00:00:04",
            false,
            false,
            true,
        ),
        (
            "missed-seven-days",
            "state",
            "2026-08-03T00:00:00",
            false,
            false,
            false,
        ),
        (
            "makeup-seven-days-rejected",
            "makeup",
            "2026-08-03T00:00:01",
            true,
            false,
            false,
        ),
        (
            "claim-day-34-reset",
            "claim",
            "2026-08-03T00:00:02",
            false,
            false,
            false,
        ),
        (
            "duplicate-day-34",
            "claim",
            "2026-08-03T00:00:02",
            false,
            false,
            true,
        ),
    ] {
        actions.push(checkin_input(
            name, operation, local, rejected, fail_write, duplicate,
        ));
    }
    actions
}

fn checkin_install_fault_probe(
    connection: &Connection,
) -> std::sync::Arc<std::sync::atomic::AtomicUsize> {
    use std::sync::{
        atomic::{AtomicUsize, Ordering},
        Arc,
    };

    unsafe extern "C" fn record(
        context: *mut rusqlite::ffi::sqlite3_context,
        _argument_count: std::ffi::c_int,
        _values: *mut *mut rusqlite::ffi::sqlite3_value,
    ) {
        unsafe {
            let hits = &*rusqlite::ffi::sqlite3_user_data(context).cast::<AtomicUsize>();
            hits.fetch_add(1, Ordering::SeqCst);
            rusqlite::ffi::sqlite3_result_int(context, 1);
        }
    }
    unsafe extern "C" fn destroy(data: *mut std::ffi::c_void) {
        unsafe {
            drop(Arc::from_raw(data.cast::<AtomicUsize>()));
        }
    }
    let hits = Arc::new(AtomicUsize::new(0));
    let result = unsafe {
        rusqlite::ffi::sqlite3_create_function_v2(
            connection.handle(),
            c"liberty_checkin_fault_probe".as_ptr(),
            0,
            rusqlite::ffi::SQLITE_UTF8,
            Arc::into_raw(Arc::clone(&hits)).cast_mut().cast(),
            Some(record),
            None,
            None,
            Some(destroy),
        )
    };
    assert_eq!(result, rusqlite::ffi::SQLITE_OK);
    hits
}

fn export_checkin_compatibility_fixture(directory: &std::path::Path) {
    use crate::infrastructure::{
        repositories::{pet, pet_store},
        time::{freeze_test_clock, local_date_string},
    };

    let baseline_input = checkin_input(
        "baseline",
        "state",
        "2026-06-30T12:00:00",
        false,
        false,
        false,
    );
    let now = baseline_input["now"].as_str().unwrap();
    let baseline_clock = freeze_test_clock(now).unwrap();
    let baseline = directory.join("checkin-base.sqlite3");
    OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&baseline)
        .unwrap();
    let mut connection = Connection::open(&baseline).unwrap();
    super::schema::apply_test_schema(&connection).unwrap();
    let transaction = connection.transaction().unwrap();
    pet::ensure_default_exists_tx(&transaction).unwrap();
    pet_store::ensure_store_defaults_tx(&transaction, now).unwrap();
    let metadata = json!({"preciseInteger":9_223_372_036_854_775_806_i64,"zero":0.0}).to_string();
    pet_store::grant_reward_tx(
        &transaction,
        "synthetic_fixture",
        "existing-reward",
        1,
        Some(&metadata),
        now,
    )
    .unwrap();
    pet::insert_event_ledger_tx(
        &transaction,
        &super::PetEventLedgerEntry {
            id: "synthetic-existing-event".into(),
            pet_id: "default-pet".into(),
            event_type: "synthetic_fixture".into(),
            event_source: "checkin_baseline".into(),
            event_value: 0,
            event_time: now.into(),
            metadata: Some(metadata),
        },
    )
    .unwrap();
    pet_store::grant_catalog_item_tx(
        &transaction,
        &pet_store::find_catalog_item_by_key(pet_store::MAKEUP_TICKET_ITEM_KEY).unwrap(),
        2,
        "synthetic_fixture",
        now,
    )
    .unwrap();
    transaction.commit().unwrap();
    let initial = checkin_snapshot(&connection);
    let columns: serde_json::Map<_, _> = checkin_snapshot_queries()
        .as_object()
        .unwrap()
        .iter()
        .map(|(name, query)| {
            let statement = connection.prepare(query.as_str().unwrap()).unwrap();
            (
                name.clone(),
                json!(statement.column_names().collect::<Vec<_>>()),
            )
        })
        .collect();
    connection.close().unwrap();
    drop(baseline_clock);

    let database = directory.join("checkin-rust.sqlite3");
    let mut output = OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&database)
        .unwrap();
    std::io::copy(&mut std::fs::File::open(&baseline).unwrap(), &mut output).unwrap();
    drop(output);
    let mut steps = Vec::new();
    for input in checkin_replay_actions() {
        let _clock = freeze_test_clock(input["now"].as_str().unwrap()).unwrap();
        assert_eq!(local_date_string(), input["date"].as_str().unwrap());
        let rejected = input["rejected"].as_bool().unwrap();
        let fail_write = input["failWrite"].as_bool().unwrap();
        let mut connection = Connection::open(&database).unwrap();
        let fault_hits = checkin_install_fault_probe(&connection);
        if fail_write {
            connection.execute_batch("CREATE TRIGGER synthetic_checkin_failure BEFORE INSERT ON pet_daily_check_ins BEGIN SELECT liberty_checkin_fault_probe(); SELECT RAISE(ABORT,'synthetic-checkin-write-failure'); END;").unwrap();
        }
        let before = checkin_snapshot(&connection);
        let result = checkin_action(&mut connection, &input);
        assert_eq!(result.is_err(), rejected, "{input}: {result:?}");
        let trigger_error = if fail_write {
            let error = result.as_ref().unwrap_err();
            assert_eq!(error, "synthetic-checkin-write-failure");
            Some(error.clone())
        } else {
            None
        };
        let result = if fail_write {
            assert_eq!(fault_hits.load(std::sync::atomic::Ordering::SeqCst), 1);
            assert!(result
                .unwrap_err()
                .contains("synthetic-checkin-write-failure"));
            json!({"fault":"synthetic-checkin-write-failure"})
        } else {
            assert_eq!(fault_hits.load(std::sync::atomic::Ordering::SeqCst), 0);
            result.unwrap_or_else(|error| json!({"error":error}))
        };
        if !rejected && input["operation"] == "claim" {
            assert_eq!(result["duplicate"], input["duplicate"]);
        }
        match input["name"].as_str().unwrap() {
            "claim-day-14" => {
                assert_eq!(result["entry"]["cycleDay"], 14);
                assert_eq!(result["entry"]["rewardLp"], 60);
                assert_eq!(result["entry"]["rewardItems"][0]["itemKey"], "sprout-badge");
            }
            "claim-day-15" | "claim-day-21" => {
                let day = if input["name"] == "claim-day-15" {
                    15
                } else {
                    21
                };
                assert_eq!(result["entry"]["cycleDay"], day);
                assert_eq!(result["entry"]["streakCount"], day);
                assert_eq!(result["entry"]["rewardLp"], 20);
                assert_eq!(result["entry"]["rewardItems"], json!([]));
            }
            _ => {}
        }
        let persisted = checkin_snapshot(&connection);
        if rejected || input["unchanged"].as_bool().unwrap() {
            assert_eq!(
                persisted, before,
                "rejected/duplicate action mutated tables"
            );
        }
        connection.close().unwrap();
        let reopened = Connection::open(&database).unwrap();
        assert_eq!(checkin_snapshot(&reopened), persisted, "reopen lost writes");
        if fail_write {
            reopened
                .execute_batch("DROP TRIGGER synthetic_checkin_failure")
                .unwrap();
        }
        let mut reopened = reopened;
        let state_input = json!({"operation":"state","now":input["now"]});
        let reopened_state = checkin_action(&mut reopened, &state_input).unwrap();
        let after_read = checkin_snapshot(&reopened);
        reopened.close().unwrap();
        let mut reopened = Connection::open(&database).unwrap();
        assert_eq!(checkin_snapshot(&reopened), after_read);
        assert_eq!(
            checkin_action(&mut reopened, &state_input).unwrap(),
            reopened_state
        );
        assert_eq!(checkin_snapshot(&reopened), after_read);
        reopened.close().unwrap();
        steps.push(json!({"input":input,"output":result,"persisted":persisted,"reopenedState":reopened_state,"afterRead":after_read,"triggerError":trigger_error}));
    }
    let connection = Connection::open(&database).unwrap();
    let totals: (i64,i64,i64) = connection.query_row("SELECT (SELECT COUNT(*) FROM pet_daily_check_ins),(SELECT experience FROM pet_profile WHERE id='default-pet'),(SELECT balance FROM pet_wallets WHERE pet_id='default-pet' AND currency_key='lp')",[],|row|Ok((row.get(0)?,row.get(1)?,row.get(2)?))).unwrap();
    assert_eq!(totals, (26, 130, 631));
    connection.close().unwrap();
    OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(directory.join("checkin-expected.json"))
        .unwrap()
        .write_all(
            serde_json::to_string_pretty(&json!({
                "clockMode":"thread-local-fixed","startDate":"2026-07-01","continuousDays":21,
                "columns":columns,"queries":checkin_snapshot_queries(),"initial":initial,"steps":steps
            }))
            .unwrap()
            .as_bytes(),
        )
        .unwrap();
}

fn gameplay_snapshot_queries() -> serde_json::Value {
    json!({
        "plots": "SELECT id,plot_index,crop_key,status,stage_index,planted_at,last_watered_at,next_care_at,mature_at,updated_at FROM farm_plots ORDER BY id",
        "tasks": "SELECT id,game_key,slot_index,job_key,status,stage_index,started_at,last_cared_at,next_care_at,claimable_at,updated_at FROM work_game_tasks ORDER BY id",
        "inventory": "SELECT id,pet_id,item_key,item_type,slot,quantity,equipped,source,purchased_at,updated_at FROM pet_inventory ORDER BY id",
        "wallets": "SELECT pet_id,currency_key,balance,lifetime_earned,lifetime_spent,updated_at FROM pet_wallets ORDER BY pet_id,currency_key",
        "harvests": "SELECT id,plot_id,crop_key,rewards_json,lp_reward,created_at FROM farm_harvest_ledger ORDER BY id",
        "rewards": "SELECT id,game_key,task_id,job_key,rewards_json,lp_reward,created_at FROM work_game_reward_ledger ORDER BY id",
        "economy": "SELECT pet_id,entry_type,currency_key,amount,balance_after,source_type,source_key,metadata,created_at FROM pet_economy_ledger ORDER BY pet_id,currency_key,source_type,source_key"
    })
}

fn gameplay_snapshot(connection: &Connection) -> serde_json::Value {
    use rusqlite::types::ValueRef;

    let mut snapshot = serde_json::Map::new();
    for (name, query) in gameplay_snapshot_queries().as_object().unwrap() {
        let mut statement = connection.prepare(query.as_str().unwrap()).unwrap();
        let json_columns: Vec<_> = statement
            .column_names()
            .map(|name| name == "rewards_json")
            .collect();
        let rows = statement
            .query_map([], |row| {
                let mut values = Vec::new();
                for (column, is_json) in json_columns.iter().enumerate() {
                    values.push(match row.get_ref(column)? {
                        ValueRef::Null => serde_json::Value::Null,
                        ValueRef::Integer(value) => json!(value),
                        ValueRef::Real(value) => json!(value),
                        ValueRef::Text(value) => {
                            let text = std::str::from_utf8(value).unwrap();
                            if *is_json {
                                serde_json::from_str(text).unwrap()
                            } else {
                                json!(text)
                            }
                        }
                        ValueRef::Blob(_) => panic!("unexpected gameplay blob"),
                    });
                }
                Ok(values)
            })
            .unwrap()
            .collect::<Result<Vec<_>, _>>()
            .unwrap();
        snapshot.insert(name.clone(), json!(rows));
    }
    snapshot.into()
}

fn gameplay_action(
    transaction: &rusqlite::Transaction<'_>,
    input: &serde_json::Value,
) -> Result<serde_json::Value, String> {
    use crate::infrastructure::repositories::{farm, work_game};

    let now = input["now"].as_str().unwrap();
    let target = input["target"].as_str().unwrap();
    let config = input["config"].as_str().unwrap();
    let game = input["gameKey"].as_str().unwrap();
    match input["operation"].as_str().unwrap() {
        "farm_state" => farm::state_tx(transaction, now).map(|value| json!(value)),
        "plant" => farm::plant_crop_tx(transaction, target, config, now).map(|value| json!(value)),
        "water" => farm::water_plot_tx(transaction, target, now).map(|value| json!(value)),
        "harvest" => farm::harvest_plot_tx(transaction, target, now)
            .map(|(state, harvest)| json!({"state":state,"harvest":harvest})),
        "work_state" => work_game::state_tx(transaction, game, now).map(|value| json!(value)),
        "start" => work_game::start_task_tx(transaction, game, target, config, now)
            .map(|value| json!(value)),
        "care" => work_game::care_task_tx(transaction, game, target, now).map(|value| json!(value)),
        "claim" => {
            work_game::claim_task_tx(transaction, game, target, now).map(|value| json!(value))
        }
        _ => panic!("unknown gameplay operation"),
    }
}

fn gameplay_cycle_actions(farm: bool) -> Vec<serde_json::Value> {
    use crate::infrastructure::time::{add_seconds, parse_iso};

    let mut actions = Vec::new();
    let (state, start, care, claim, target, config, game, interval, care_count) = if farm {
        (
            "farm_state",
            "plant",
            "water",
            "harvest",
            "farm-plot-1",
            "carrot",
            "",
            300,
            2,
        )
    } else {
        (
            "work_state",
            "start",
            "care",
            "claim",
            "mine-slot-2",
            "deep-vein",
            "mine",
            500,
            2,
        )
    };
    for (cycle, timestamp) in [
        ("A", "2026-07-15T23:58:00+00:00"),
        ("B", "2026-07-18T23:58:00+00:00"),
        ("A-replay", "2026-07-15T23:58:00+00:00"),
    ] {
        let instant = parse_iso(timestamp).unwrap();
        let mut push = |label: &str, operation: &str, seconds: i64, rejected: bool| {
            actions.push(json!({
                "name": format!("{cycle}-{label}"), "operation": operation,
                "now": add_seconds(instant,seconds).unwrap(), "target":target,
                "config":config, "gameKey":game, "rejected":rejected
            }));
        };
        push("start", start, 0, false);
        push("duplicate-start", start, 0, true);
        push("early-care", care, interval - 1, true);
        push("offline-needs-care", state, 86400, false);
        push("unattended-claim", claim, 86400, true);
        for stage in 0..care_count {
            let at = 86400 + stage * interval;
            push(&format!("care-{}", stage + 1), care, at, false);
            if stage + 1 < care_count {
                push("early-next-care", care, at + interval - 1, true);
            }
        }
        let claim_at = 86400 + care_count * interval;
        push("early-claim", claim, claim_at - 1, true);
        push("claim", claim, claim_at, false);
        push("duplicate-claim", claim, claim_at, true);
        push("after-claim", state, claim_at, false);
    }
    actions
}

fn export_gameplay_compatibility_fixture(directory: &std::path::Path) {
    use crate::infrastructure::repositories::{farm, pet_store, work_game};

    const NOW: &str = "2026-07-15T23:58:00+00:00";
    let baseline = directory.join("gameplay-base.sqlite3");
    OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&baseline)
        .unwrap();
    let mut connection = Connection::open(&baseline).unwrap();
    super::schema::apply_test_schema(&connection).unwrap();
    let transaction = connection.transaction().unwrap();
    transaction.execute(
        "INSERT INTO pet_profile(id,name,level,experience,stage,current_mood,created_at,updated_at) VALUES('default-pet','Synthetic gameplay',1,0,'first_meet','idle',?1,?1)",
        params![NOW],
    ).unwrap();
    pet_store::ensure_store_defaults_tx(&transaction, NOW).unwrap();
    pet_store::grant_catalog_item_tx(
        &transaction,
        &pet_store::find_catalog_item_by_key("carrot-seed").unwrap(),
        3,
        "synthetic_fixture",
        NOW,
    )
    .unwrap();
    farm::state_tx(&transaction, NOW).unwrap();
    work_game::state_tx(&transaction, "mine", NOW).unwrap();
    transaction.commit().unwrap();
    let initial = gameplay_snapshot(&connection);
    connection.close().unwrap();

    let mut cases = Vec::new();
    for (name, farm) in [("farm", true), ("work", false)] {
        let database = directory.join(format!("gameplay-{name}-rust.sqlite3"));
        let mut output = OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&database)
            .unwrap();
        std::io::copy(&mut std::fs::File::open(&baseline).unwrap(), &mut output).unwrap();
        drop(output);
        let mut steps = Vec::new();
        for input in gameplay_cycle_actions(farm) {
            let mut connection = Connection::open(&database).unwrap();
            let before = gameplay_snapshot(&connection);
            let transaction = connection.transaction().unwrap();
            let result = gameplay_action(&transaction, &input);
            assert_eq!(
                result.is_err(),
                input["rejected"].as_bool().unwrap(),
                "{input}"
            );
            let result = match result {
                Ok(value) => {
                    transaction.commit().unwrap();
                    value
                }
                Err(error) => {
                    transaction.rollback().unwrap();
                    json!({"error":error})
                }
            };
            connection.close().unwrap();
            let reopened = Connection::open(&database).unwrap();
            let persisted = gameplay_snapshot(&reopened);
            if input["rejected"].as_bool().unwrap() {
                assert_eq!(
                    persisted, before,
                    "rejected action changed persistence: {input}"
                );
            }
            reopened.close().unwrap();
            steps.push(json!({"input":input,"output":result,"persisted":persisted}));
        }
        let connection = Connection::open(&database).unwrap();
        let ledger = if farm {
            "farm_harvest_ledger"
        } else {
            "work_game_reward_ledger"
        };
        let count: i64 = connection
            .query_row(&format!("SELECT COUNT(*) FROM {ledger}"), [], |row| {
                row.get(0)
            })
            .unwrap();
        assert_eq!(count, 2, "A/B/A must grant only two unique rewards");
        let earned: i64 = connection
            .query_row(&format!("SELECT SUM(lp_reward) FROM {ledger}"), [], |row| {
                row.get(0)
            })
            .unwrap();
        let balance: i64 = connection
            .query_row(
                "SELECT balance FROM pet_wallets WHERE pet_id='default-pet' AND currency_key='lp'",
                [],
                |row| row.get(0),
            )
            .unwrap();
        assert_eq!(balance, earned);
        connection.close().unwrap();
        cases.push(json!({"name":name,"steps":steps}));
    }
    OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(directory.join("gameplay-expected.json"))
        .unwrap()
        .write_all(
            serde_json::to_string_pretty(&json!({
                "queries":gameplay_snapshot_queries(),"initial":initial,"cases":cases
            }))
            .unwrap()
            .as_bytes(),
        )
        .unwrap();
}
