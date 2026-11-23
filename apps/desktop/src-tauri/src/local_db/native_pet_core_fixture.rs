use std::{fs::OpenOptions, io::Write, path::Path};

use rusqlite::{types::ValueRef, Connection};
use serde_json::{json, Value};

use crate::infrastructure::{
    repositories::{pet, pet_store},
    time::{freeze_test_clock, local_date_string, utc_now},
};

fn queries() -> Value {
    let mut queries = super::checkin_snapshot_queries();
    queries["events"] = json!("SELECT id,pet_id,event_type,event_source,event_value,event_time,metadata FROM pet_event_ledger ORDER BY rowid");
    queries["economy"] = json!("SELECT id,pet_id,entry_type,currency_key,amount,balance_after,source_type,source_key,metadata,created_at FROM pet_economy_ledger ORDER BY rowid");
    queries
}

fn snapshot(connection: &Connection) -> Value {
    let mut result = serde_json::Map::new();
    for (name, query) in queries().as_object().unwrap() {
        let mut statement = connection.prepare(query.as_str().unwrap()).unwrap();
        let columns = statement.column_count();
        let rows = statement
            .query_map([], |row| {
                (0..columns)
                    .map(|column| {
                        Ok(match row.get_ref(column)? {
                            ValueRef::Null => Value::Null,
                            ValueRef::Integer(value) => json!(value),
                            ValueRef::Real(value) => json!(value),
                            ValueRef::Text(value) => json!(std::str::from_utf8(value).unwrap()),
                            ValueRef::Blob(_) => panic!("unexpected synthetic pet core blob"),
                        })
                    })
                    .collect::<rusqlite::Result<Vec<_>>>()
            })
            .unwrap()
            .collect::<rusqlite::Result<Vec<_>>>()
            .unwrap();
        result.insert(name.clone(), json!(rows));
    }
    result.into()
}

fn profile(connection: &Connection) -> Result<Value, String> {
    pet::ensure_default_exists(connection)?;
    pet::reconcile_profile_leveling(connection)?;
    pet::load_profile(connection).map(|profile| json!(profile))
}

fn install_sqlite_clock(connection: &Connection) {
    use rusqlite::ffi;

    unsafe extern "C" fn date(
        context: *mut ffi::sqlite3_context,
        count: std::ffi::c_int,
        values: *mut *mut ffi::sqlite3_value,
    ) {
        let result = std::panic::catch_unwind(|| unsafe {
            let delegate = &*ffi::sqlite3_user_data(context).cast::<Connection>();
            let placeholders = vec!["?"; count.max(1) as usize].join(",");
            let sql = std::ffi::CString::new(format!("SELECT date({placeholders})")).unwrap();
            let mut statement = std::ptr::null_mut();
            let mut status = ffi::sqlite3_prepare_v2(
                delegate.handle(),
                sql.as_ptr(),
                -1,
                &mut statement,
                std::ptr::null_mut(),
            );
            if status == ffi::SQLITE_OK {
                let now = utc_now();
                for index in 0..count.max(1) {
                    let argument = if count == 0 {
                        std::ptr::null_mut()
                    } else {
                        *values.add(index as usize)
                    };
                    let replace = count == 0
                        || (index == 0
                            && ffi::sqlite3_value_type(argument) == ffi::SQLITE_TEXT
                            && ffi::sqlite3_value_bytes(argument) == 3
                            && std::slice::from_raw_parts(ffi::sqlite3_value_text(argument), 3)
                                .eq_ignore_ascii_case(b"now"));
                    status = if replace {
                        ffi::sqlite3_bind_text(
                            statement,
                            index + 1,
                            now.as_ptr().cast(),
                            now.len() as i32,
                            ffi::SQLITE_TRANSIENT(),
                        )
                    } else {
                        ffi::sqlite3_bind_value(statement, index + 1, argument)
                    };
                    if status != ffi::SQLITE_OK {
                        break;
                    }
                }
                if status == ffi::SQLITE_OK {
                    status = ffi::sqlite3_step(statement);
                }
                if status == ffi::SQLITE_ROW {
                    ffi::sqlite3_result_value(context, ffi::sqlite3_column_value(statement, 0));
                } else {
                    ffi::sqlite3_result_error_code(context, status);
                }
            } else {
                ffi::sqlite3_result_error_code(context, status);
            }
            ffi::sqlite3_finalize(statement);
        });
        if result.is_err() {
            unsafe {
                ffi::sqlite3_result_error(context, c"synthetic SQLite clock failure".as_ptr(), -1)
            };
        }
    }
    unsafe extern "C" fn destroy(data: *mut std::ffi::c_void) {
        unsafe { drop(Box::from_raw(data.cast::<Connection>())) };
    }
    let delegate = Box::new(Connection::open_in_memory().unwrap());
    let result = unsafe {
        ffi::sqlite3_create_function_v2(
            connection.handle(),
            c"date".as_ptr(),
            -1,
            ffi::SQLITE_UTF8,
            Box::into_raw(delegate).cast(),
            Some(date),
            None,
            None,
            Some(destroy),
        )
    };
    assert_eq!(result, ffi::SQLITE_OK);
}

fn action(connection: &mut Connection, input: &Value) -> Result<Value, String> {
    install_sqlite_clock(connection);
    let operation = input["operation"].as_str().unwrap();
    match operation {
        "profile" => profile(connection),
        "settings" => {
            pet::ensure_default_exists(connection)?;
            pet::load_settings(connection).map(|settings| json!(settings))
        }
        "save-settings" => {
            pet::ensure_default_exists(connection)?;
            let mut settings: super::super::PetSettings =
                serde_json::from_value(input["settings"].clone()).unwrap();
            settings.pet_id = "default-pet".into();
            settings.proactive_level = settings.proactive_level.clamp(0, 3);
            settings.updated_at = utc_now();
            pet::save_preferences(connection, &settings)?;
            pet::load_settings(connection).map(|settings| json!(settings))
        }
        "name" => {
            let name = input["source"].as_str().unwrap().trim();
            if name.is_empty() {
                return Err("宠物名字不能为空。".into());
            }
            profile(connection)?;
            let mut profile = pet::load_profile(connection)?;
            profile.name = name.into();
            profile.updated_at = utc_now();
            let transaction = connection
                .transaction()
                .map_err(|error| error.to_string())?;
            pet::save_profile_tx(&transaction, &profile)?;
            transaction.commit().map_err(|error| error.to_string())?;
            pet::load_profile(connection).map(|profile| json!(profile))
        }
        "events" => {
            pet::ensure_default_exists(connection)?;
            pet::list_event_ledger(
                connection,
                input["limit"].as_i64().unwrap().clamp(1, 100) as usize,
            )
            .map(|events| json!(events))
        }
        "cosmetics" => {
            pet::ensure_default_exists(connection)?;
            pet::list_cosmetic_unlocks(connection).map(|cosmetics| json!(cosmetics))
        }
        "interaction" | "workflow" => {
            let source = input["source"].as_str().unwrap().trim().to_lowercase();
            let metadata = input["metadata"]
                .as_str()
                .map(str::trim)
                .filter(|value| !value.is_empty());
            let (growth, mood) = if operation == "interaction" {
                (
                    1,
                    if source == "feed" {
                        "proud"
                    } else {
                        "cheerful"
                    },
                )
            } else {
                match source.as_str() {
                    "job_created" => (5, "cheerful"),
                    "transcription_started" => (3, "excited"),
                    "transcription_completed" => (12, "proud"),
                    "ai_summary_completed" => (10, "proud"),
                    "export_completed" => (6, "proud"),
                    "daily_open" => (2, "idle"),
                    "dark_theme_used" => (0, "idle"),
                    _ => (1, "idle"),
                }
            };
            super::super::pet_growth::apply_pet_growth_event(
                connection,
                operation,
                &source,
                growth,
                mood,
                if operation == "interaction" {
                    None
                } else {
                    metadata
                },
            )
            .map(|profile| json!(profile))
        }
        _ => panic!("unknown pet core operation {operation}"),
    }
}

fn view(connection: &Connection) -> Value {
    json!({
        "profile":profile(connection).unwrap(),
        "settings":pet::load_settings(connection).unwrap(),
        "events":pet::list_event_ledger(connection,100).unwrap(),
        "cosmetics":pet::list_cosmetic_unlocks(connection).unwrap(),
    })
}

fn fault_sql(fault: &str) -> Option<String> {
    let target = match fault {
        "" => return None,
        "claim" => "BEFORE INSERT ON pet_economy_ledger WHEN NEW.source_type='pet_growth'",
        "profile" => "BEFORE UPDATE ON pet_profile",
        "settings" => "BEFORE UPDATE ON pet_settings",
        "cosmetic" => "BEFORE INSERT ON pet_cosmetic_unlocks",
        "counter" => "BEFORE INSERT ON pet_milestone_counters",
        "wallet" => "BEFORE UPDATE ON pet_wallets WHEN NEW.balance > OLD.balance",
        "reward" => "BEFORE INSERT ON pet_economy_ledger WHEN NEW.entry_type='earn'",
        "inventory" => "BEFORE INSERT ON pet_inventory WHEN NEW.source='achievement'",
        "event" => "BEFORE INSERT ON pet_event_ledger",
        _ => panic!("unknown pet core fault {fault}"),
    };
    Some(format!("CREATE TRIGGER synthetic_pet_core_failure {target} BEGIN SELECT liberty_checkin_fault_probe(); SELECT RAISE(ABORT,'synthetic-pet-core-write-failure'); END;"))
}

struct Oracle {
    connection: Connection,
    steps: Vec<Value>,
}

impl Oracle {
    fn run(&mut self, mut input: Value) {
        let clock = input["now"]
            .as_str()
            .unwrap_or("2026-07-01T12:00:00Z")
            .to_string();
        let _clock = freeze_test_clock(&clock).unwrap();
        input["now"] = json!(utc_now());
        input["date"] = json!(local_date_string());
        if let Some(sql) = input["setupSql"].as_str() {
            self.connection.execute_batch(sql).unwrap();
        }
        let hits = super::checkin_install_fault_probe(&self.connection);
        let trigger = fault_sql(input["fault"].as_str().unwrap_or(""));
        if let Some(sql) = &trigger {
            self.connection.execute_batch(sql).unwrap();
        }
        let before = snapshot(&self.connection);
        let result = action(&mut self.connection, &input);
        assert_eq!(
            result.is_err(),
            input["rejected"] == true,
            "{input}: {result:?}"
        );
        let raw_error = result.as_ref().err().cloned();
        let output = if trigger.is_some() {
            assert_eq!(hits.load(std::sync::atomic::Ordering::SeqCst), 1, "{input}");
            assert_eq!(
                raw_error.as_deref(),
                Some("synthetic-pet-core-write-failure")
            );
            json!({"fault":"synthetic-pet-core-write-failure"})
        } else {
            assert_eq!(hits.load(std::sync::atomic::Ordering::SeqCst), 0);
            result.unwrap_or_else(|error| json!({"error":error}))
        };
        let persisted = snapshot(&self.connection);
        if input["rejected"] == true {
            assert_eq!(before, persisted, "pet core rollback: {input}");
        }
        let path = self.connection.path().unwrap().to_string();
        self.connection = Connection::open_in_memory().unwrap();
        self.connection = Connection::open(&path).unwrap();
        assert_eq!(snapshot(&self.connection), persisted);
        if trigger.is_some() {
            self.connection
                .execute_batch("DROP TRIGGER synthetic_pet_core_failure")
                .unwrap();
        }
        let state = view(&self.connection);
        let after_read = snapshot(&self.connection);
        for _ in 0..2 {
            self.connection = Connection::open_in_memory().unwrap();
            self.connection = Connection::open(&path).unwrap();
            assert_eq!(snapshot(&self.connection), after_read);
            assert_eq!(view(&self.connection), state);
            assert_eq!(snapshot(&self.connection), after_read);
        }
        self.steps.push(json!({"input":input,"output":output,"rawError":raw_error,"trigger":trigger,"persisted":persisted,"reopenedState":state,"afterRead":after_read}));
    }
}

pub(super) fn export(directory: &Path) {
    let mut cases = Vec::new();
    for name in [
        "defaults",
        "workflow",
        "interaction",
        "settings",
        "profile",
        "faults",
    ] {
        let baseline = directory.join(format!("pet-core-{name}-base.sqlite3"));
        OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&baseline)
            .unwrap();
        let mut connection = Connection::open(&baseline).unwrap();
        super::super::schema::apply_test_schema(&connection).unwrap();
        let _clock = freeze_test_clock("2026-06-30T12:00:00Z").unwrap();
        if name != "defaults" {
            pet::ensure_default_exists(&connection).unwrap();
            let transaction = connection.transaction().unwrap();
            pet_store::ensure_store_defaults_tx(&transaction, &utc_now()).unwrap();
            transaction.commit().unwrap();
        }
        if name == "settings" {
            pet::save_position(&connection, -800.5, 120.25, &utc_now()).unwrap();
        }
        if name == "profile" {
            for index in 0..105 {
                let timestamp =
                    crate::infrastructure::time::parse_iso("2026-06-29T12:00:00Z").unwrap();
                let now = crate::infrastructure::time::add_seconds(timestamp, index).unwrap();
                let _clock = freeze_test_clock(&now).unwrap();
                super::super::pet_growth::apply_pet_growth_event(
                    &mut connection,
                    "workflow",
                    "synthetic_history",
                    1,
                    "idle",
                    Some(&format!("synthetic-history-{index}")),
                )
                .unwrap();
            }
        }
        let initial = snapshot(&connection);
        connection.close().unwrap();
        let database = directory.join(format!("pet-core-{name}-rust.sqlite3"));
        let mut output = OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&database)
            .unwrap();
        std::io::copy(&mut std::fs::File::open(&baseline).unwrap(), &mut output).unwrap();
        drop(output);
        let mut oracle = Oracle {
            connection: Connection::open(&database).unwrap(),
            steps: Vec::new(),
        };
        match name {
            "defaults" => {
                for operation in ["profile", "settings", "events", "cosmetics", "profile"] {
                    oracle.run(json!({"name":format!("{operation}-{}",oracle.steps.len()),"operation":operation,"limit":20}));
                }
            }
            "workflow" => {
                for source in [
                    "daily_open",
                    "job_created",
                    "transcription_started",
                    "transcription_completed",
                    "ai_summary_completed",
                    "export_completed",
                    "dark_theme_used",
                    " Future_Event ",
                    "",
                ] {
                    for (index, metadata) in [
                        Some("  synthetic-a  "),
                        Some("synthetic-b"),
                        Some("synthetic-a"),
                        None,
                        Some("  "),
                    ]
                    .into_iter()
                    .enumerate()
                    {
                        oracle.run(json!({"name":format!("workflow-{source}-{index}"),"operation":"workflow","source":source,"metadata":metadata,"now":format!("2026-07-01T12:{:02}:00Z",oracle.steps.len())}));
                    }
                }
                for (index, now) in ["2026-07-01T23:59:59", "2026-07-02T00:00:00"]
                    .into_iter()
                    .enumerate()
                {
                    let local = now
                        .parse::<jiff::civil::DateTime>()
                        .unwrap()
                        .to_zoned(jiff::tz::TimeZone::system())
                        .unwrap()
                        .timestamp()
                        .to_string();
                    oracle.run(json!({"name":format!("daily-boundary-{index}"),"operation":"workflow","source":"daily_open","metadata":"ignored","now":local}));
                }
                for level in [30, 31, 60, 61, 100, 101, 140, 141, 180, 181, 220, 221, 255] {
                    let experience =
                        super::super::pet_leveling::total_required_exp_for_level(level);
                    oracle.run(json!({"name":format!("multiplier-level-{level}"),"operation":"workflow","source":"transcription_completed","metadata":format!("synthetic-level-{level}"),"setupSql":format!("UPDATE pet_profile SET level={level},experience={experience},stage='first_meet'"),"now":format!("2026-07-03T12:{:02}:00Z",oracle.steps.len()%60)}));
                }
                for level in [11, 31, 61, 101, 141, 181, 221] {
                    let experience =
                        super::super::pet_leveling::total_required_exp_for_level(level) - 1;
                    let stage = super::super::pet_leveling::stage_from_level(level - 1);
                    oracle.run(json!({"name":format!("stage-transition-{level}"),"operation":"workflow","source":"job_created","metadata":format!("synthetic-stage-{level}"),"setupSql":format!("UPDATE pet_profile SET level={},experience={experience},stage='{stage}'",level-1),"now":format!("2026-07-07T12:{:02}:00Z",oracle.steps.len()%60)}));
                }
            }
            "interaction" => {
                for (index, source) in [
                    "tap",
                    "pet",
                    "feed",
                    "encourage",
                    " TAP ",
                    "future",
                    "",
                    "job_created",
                    "daily_open",
                    "daily_open",
                ]
                .into_iter()
                .enumerate()
                {
                    oracle.run(json!({"name":format!("interaction-{index}"),"operation":"interaction","source":source,"metadata":source,"now":format!("2026-07-04T12:00:{index:02}Z")}));
                }
                let noon = "2026-07-08T12:00:00"
                    .parse::<jiff::civil::DateTime>()
                    .unwrap()
                    .to_zoned(jiff::tz::TimeZone::system())
                    .unwrap()
                    .timestamp()
                    .to_string();
                for source in ["tap", "pet", "feed", "encourage"] {
                    for index in 1..=20 {
                        oracle.run(json!({"name":format!("daily-cap-{source}-{index}"),"operation":"interaction","source":source,"metadata":source,"now":noon}));
                    }
                    oracle.run(json!({"name":format!("daily-cap-{source}-rejected"),"operation":"interaction","source":source,"metadata":source,"now":noon,"rejected":true}));
                    for (index, local) in ["2026-07-08T23:59:59", "2026-07-09T00:00:00"]
                        .into_iter()
                        .enumerate()
                    {
                        let now = local
                            .parse::<jiff::civil::DateTime>()
                            .unwrap()
                            .to_zoned(jiff::tz::TimeZone::system())
                            .unwrap()
                            .timestamp()
                            .to_string();
                        oracle.run(json!({"name":format!("daily-cap-{source}-boundary-{index}"),"operation":"interaction","source":source,"metadata":source,"now":now,"rejected":index==0}));
                    }
                }
            }
            "settings" => {
                for (index, level) in [i64::MIN, -1, 0, 1, 2, 3, 4, i64::MAX]
                    .into_iter()
                    .enumerate()
                {
                    oracle.run(json!({"name":format!("settings-{index}"),"operation":"save-settings","now":format!("2026-07-05T12:00:{index:02}Z"),"settings":{"petId":"synthetic-ignored-owner","desktopEnabled":index%2==0,"alwaysOnTop":index%3==0,"muted":index%2!=0,"focusModeEnabled":index%3!=0,"proactiveLevel":level,"lastWindowX":if index%2==0 {Some(-1234.5)} else {None},"lastWindowY":if index%3==0 {Some(987.25)} else {None},"updatedAt":"synthetic-ignored-time"}}));
                }
            }
            "profile" => {
                for (index, source) in ["  合成伙伴 🐾  ", "", " \t\n", "Synthetic\nName", "Libby"]
                    .into_iter()
                    .enumerate()
                {
                    oracle.run(json!({"name":format!("name-{index}"),"operation":"name","source":source,"rejected":source.trim().is_empty(),"now":format!("2026-07-06T12:00:{index:02}Z")}));
                }
                for (index, (level, experience, stage)) in [
                    (0, -1, "unknown"),
                    (3, 0, "baby"),
                    (1, 0, "mature"),
                    (999, 0, "growing"),
                    (1, 12345, "invalid"),
                    (1, i64::MAX, "first_meet"),
                ]
                .into_iter()
                .enumerate()
                {
                    oracle.run(json!({"name":format!("profile-normalize-{index}"),"operation":"profile","setupSql":format!("UPDATE pet_profile SET level={level},experience={experience},stage='{stage}'"),"now":format!("2026-07-06T13:00:{index:02}Z")}));
                }
                for limit in [0, 1, 20, 100, 101] {
                    oracle.run(json!({"name":format!("event-limit-{limit}"),"operation":"events","limit":limit}));
                }
            }
            "faults" => {
                for fault in [
                    "claim",
                    "profile",
                    "counter",
                    "wallet",
                    "reward",
                    "inventory",
                    "event",
                ] {
                    oracle.run(json!({"name":format!("fault-{fault}"),"operation":"workflow","source":"job_created","metadata":format!("synthetic-fault-{fault}"),"fault":fault,"rejected":true,"setupSql":if fault=="inventory" {"DELETE FROM pet_inventory WHERE source='achievement'"} else {""}}));
                    oracle.run(json!({"name":format!("retry-{fault}"),"operation":"workflow","source":"job_created","metadata":format!("synthetic-fault-{fault}")}));
                }
                let experience = super::super::pet_leveling::total_required_exp_for_level(11) - 1;
                oracle.run(json!({"name":"fault-cosmetic","operation":"workflow","source":"job_created","metadata":"synthetic-stage-job","fault":"cosmetic","rejected":true,"setupSql":format!("UPDATE pet_profile SET level=10,experience={experience},stage='first_meet'")}));
                oracle.run(json!({"name":"stage-retry","operation":"workflow","source":"job_created","metadata":"synthetic-stage-job"}));
                oracle.run(json!({"name":"fault-settings","operation":"save-settings","fault":"settings","rejected":true,"settings":{"petId":"default-pet","desktopEnabled":false,"alwaysOnTop":false,"muted":true,"focusModeEnabled":true,"proactiveLevel":3,"lastWindowX":null,"lastWindowY":null,"updatedAt":"ignored"}}));
                oracle.run(json!({"name":"fault-name","operation":"name","source":"synthetic-name","fault":"profile","rejected":true}));
            }
            _ => unreachable!(),
        }
        cases.push(json!({"name":name,"initial":initial,"steps":oracle.steps}));
    }
    let mut output = OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(directory.join("pet-core-expected.json"))
        .unwrap();
    output
        .write_all(
            serde_json::to_string_pretty(
                &json!({"clockMode":"thread-local-fixed","queries":queries(),"cases":cases}),
            )
            .unwrap()
            .as_bytes(),
        )
        .unwrap();
}

#[test]
#[ignore = "explicit synthetic Rust pet core oracle export"]
fn export_native_pet_core_fixture() {
    let directory = std::path::PathBuf::from(
        std::env::var_os("LIBERTY_NATIVE_FIXTURE_DIR")
            .expect("explicit synthetic fixture directory"),
    );
    assert!(directory.is_absolute() && directory.is_dir());
    export(&directory);
}

#[test]
fn sqlite_clock_preserves_builtin_date_semantics() {
    let connection = Connection::open_in_memory().unwrap();
    let delegate = Connection::open_in_memory().unwrap();
    install_sqlite_clock(&connection);
    let _clock = freeze_test_clock("2026-07-08T23:59:59.123+08:00").unwrap();
    let now = utc_now();
    for (query, argument) in [
        ("SELECT date(?1,'localtime')", Some("now")),
        ("SELECT date(?1,'localtime')", Some("NOW")),
        (
            "SELECT date(?1,'localtime')",
            Some("1969-12-31T23:59:59.123Z"),
        ),
        (
            "SELECT date(?1,'localtime')",
            Some("2026-07-08T23:59:59+08:00"),
        ),
        ("SELECT date(?1,'+1 day','start of month')", Some("now")),
        (
            "SELECT date(?1,'localtime')",
            Some("synthetic-invalid-date"),
        ),
        ("SELECT date(?1,'localtime')", None),
    ] {
        let expected_argument = match argument {
            Some(value) if value.eq_ignore_ascii_case("now") => Some(now.as_str()),
            value => value,
        };
        let expected: Option<String> = delegate
            .query_row(query, [expected_argument], |row| row.get(0))
            .unwrap();
        let actual: Option<String> = connection
            .query_row(query, [argument], |row| row.get(0))
            .unwrap();
        assert_eq!(actual, expected, "{query}: {argument:?}");
    }
    let expected: Option<String> = delegate
        .query_row("SELECT date(?1)", [&now], |row| row.get(0))
        .unwrap();
    let actual: Option<String> = connection
        .query_row("SELECT date()", [], |row| row.get(0))
        .unwrap();
    assert_eq!(actual, expected);
}
