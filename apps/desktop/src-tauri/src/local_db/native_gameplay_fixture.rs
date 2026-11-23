use std::{fs::OpenOptions, io::Write, path::Path};

use jiff::{SignedDuration, Timestamp};
use rusqlite::{params, types::ValueRef, Connection};
use serde_json::{json, Value};

use crate::infrastructure::{
    repositories::{farm, pet_store, work_game},
    time::{add_seconds, freeze_test_clock, parse_iso},
};

const BASE_NOW: &str = "2026-07-01T23:58:00+00:00";

fn queries() -> Value {
    let mut queries = super::checkin_snapshot_queries();
    for (name, query) in super::gameplay_snapshot_queries().as_object().unwrap() {
        if matches!(name.as_str(), "plots" | "tasks" | "harvests" | "rewards") {
            queries[name] = query.clone();
        }
    }
    queries["events"] = json!(
        "SELECT id,pet_id,event_type,event_source,event_value,event_time,metadata FROM pet_event_ledger ORDER BY rowid"
    );
    queries["economy"] = json!(
        "SELECT id,pet_id,entry_type,currency_key,amount,balance_after,source_type,source_key,metadata,created_at FROM pet_economy_ledger ORDER BY rowid"
    );
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
                            ValueRef::Blob(_) => panic!("unexpected synthetic gameplay blob"),
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

fn action(connection: &mut Connection, input: &Value) -> Result<Value, String> {
    let transaction = connection
        .transaction()
        .map_err(|error| error.to_string())?;
    let now = input["now"].as_str().unwrap();
    let result = match input["operation"].as_str().unwrap() {
        "market" => json!(farm::work_market_state_tx(&transaction, now)?),
        "harvest-ledger" => json!(farm::list_harvest_ledger_tx(
            &transaction,
            input["limit"].as_u64().unwrap() as usize
        )?),
        _ => super::gameplay_action(&transaction, input)?,
    };
    transaction.commit().map_err(|error| error.to_string())?;
    Ok(result)
}

fn view(connection: &mut Connection, now: &str) -> Value {
    let transaction = connection.transaction().unwrap();
    let farm = farm::state_tx(&transaction, now).unwrap();
    let market = farm::work_market_state_tx(&transaction, now).unwrap();
    let work = ["mine", "factory", "convenience-store"]
        .map(|key| work_game::state_tx(&transaction, key, now).unwrap());
    transaction.commit().unwrap();
    json!({"farm":farm,"market":market,"work":work})
}

fn fault_sql(fault: &str, farm: bool) -> Option<String> {
    let state_table = if farm {
        "farm_plots"
    } else {
        "work_game_tasks"
    };
    let ledger = if farm {
        "farm_harvest_ledger"
    } else {
        "work_game_reward_ledger"
    };
    let target = match fault {
        "" => return None,
        "seed" => "BEFORE UPDATE ON pet_inventory WHEN NEW.quantity < OLD.quantity".into(),
        "seed-delete" => {
            "BEFORE DELETE ON pet_inventory WHEN OLD.item_type = 'seed' AND OLD.quantity <= 0"
                .into()
        }
        "state" => format!("BEFORE UPDATE ON {state_table}"),
        "clear" => format!(
            "BEFORE UPDATE ON {state_table} WHEN NEW.stage_index = 0 AND OLD.stage_index > 0"
        ),
        "event" => "BEFORE INSERT ON pet_economy_ledger WHEN NEW.entry_type = 'event'".into(),
        "inventory-insert" => {
            "BEFORE INSERT ON pet_inventory WHEN NEW.item_key != 'libby-default'".into()
        }
        "inventory-update" => {
            "BEFORE UPDATE ON pet_inventory WHEN NEW.quantity > OLD.quantity".into()
        }
        "wallet" => {
            "BEFORE UPDATE ON pet_wallets WHEN NEW.lifetime_earned > OLD.lifetime_earned".into()
        }
        "economy" => "BEFORE INSERT ON pet_economy_ledger WHEN NEW.entry_type = 'earn'".into(),
        "ledger" => format!("BEFORE INSERT ON {ledger}"),
        _ => panic!("unknown gameplay fault {fault}"),
    };
    Some(format!(
        "CREATE TRIGGER synthetic_gameplay_failure {target} BEGIN SELECT liberty_checkin_fault_probe(); SELECT RAISE(ABORT,'synthetic-gameplay-write-failure'); END;"
    ))
}

struct Oracle {
    connection: Connection,
    target: String,
    config: String,
    game: String,
    application: bool,
    steps: Vec<Value>,
}

impl Oracle {
    fn step(
        &mut self,
        name: &str,
        operation: &str,
        at: Timestamp,
        rejected: bool,
        fault: &str,
    ) -> Value {
        self.run(json!({"name":name,"operation":operation,"now":add_seconds(at,0).unwrap(),"target":self.target,"config":self.config,"gameKey":self.game,"rejected":rejected,"fault":fault,"limit":12,"application":self.application}))
    }

    fn run(&mut self, mut input: Value) -> Value {
        if self.application {
            input["application"] = json!(true);
        }
        let now = input["now"].as_str().unwrap();
        let _clock = freeze_test_clock(now).unwrap();
        if let Some(sql) = input["setupSql"].as_str() {
            self.connection.execute_batch(sql).unwrap();
        }
        let hits = super::checkin_install_fault_probe(&self.connection);
        let fault = input["fault"].as_str().unwrap();
        let trigger = fault_sql(fault, self.game.is_empty());
        if let Some(sql) = &trigger {
            self.connection.execute_batch(sql).unwrap();
        }
        let before = snapshot(&self.connection);
        let result = action(&mut self.connection, &input);
        assert_eq!(
            result.is_err(),
            input["rejected"].as_bool().unwrap(),
            "{input}: {result:?}"
        );
        let raw_error = result.as_ref().err().cloned();
        let output = match result {
            Ok(value) => value,
            Err(error) if !fault.is_empty() && !fault.starts_with("seed") => {
                assert_eq!(error, "synthetic-gameplay-write-failure", "{input}");
                json!({"fault":error})
            }
            Err(error) => json!({"error":error}),
        };
        assert_eq!(
            hits.load(std::sync::atomic::Ordering::SeqCst),
            usize::from(!fault.is_empty()),
            "{input}"
        );
        if trigger.is_some() {
            self.connection
                .execute_batch("DROP TRIGGER synthetic_gameplay_failure")
                .unwrap();
        }
        let persisted = snapshot(&self.connection);
        if input["rejected"] == true {
            assert_eq!(
                before, persisted,
                "failed action changed persistence: {input}"
            );
        }
        let database = self.connection.path().unwrap().to_string();
        let old = std::mem::replace(&mut self.connection, Connection::open_in_memory().unwrap());
        old.close().unwrap();
        self.connection = Connection::open(&database).unwrap();
        assert_eq!(snapshot(&self.connection), persisted, "raw reopen: {input}");
        let reopened_state = view(&mut self.connection, now);
        let after_read = snapshot(&self.connection);
        for _ in 0..2 {
            let old =
                std::mem::replace(&mut self.connection, Connection::open_in_memory().unwrap());
            old.close().unwrap();
            self.connection = Connection::open(&database).unwrap();
            assert_eq!(
                view(&mut self.connection, now),
                reopened_state,
                "view reopen: {input}"
            );
            assert_eq!(
                snapshot(&self.connection),
                after_read,
                "read idempotency: {input}"
            );
        }
        self.steps.push(json!({"input":input,"output":output,"rawError":raw_error,"trigger":trigger,"persisted":persisted,"reopenedState":reopened_state,"afterRead":after_read}));
        output
    }
}

fn shift(at: Timestamp, milliseconds: i64) -> Timestamp {
    at.checked_add(SignedDuration::from_millis(milliseconds))
        .unwrap()
}

fn select_start(
    oracle: &mut Oracle,
    base: Timestamp,
    minimum: i64,
    maximum: i64,
    bonus: bool,
) -> Timestamp {
    let desired = if bonus { maximum } else { minimum };
    for offset in 0..20_000 {
        let candidate = shift(base, offset);
        let now = add_seconds(candidate, 0).unwrap();
        let _clock = freeze_test_clock(&now).unwrap();
        let transaction = oracle.connection.transaction().unwrap();
        let result = if oracle.game.is_empty() {
            transaction.execute("UPDATE farm_plots SET crop_key=?1,status='mature',stage_index=3,planted_at=?2,next_care_at=NULL,mature_at=?2 WHERE id=?3",params![oracle.config,now,oracle.target]).unwrap();
            let (_, harvest) = farm::harvest_plot_tx(&transaction, &oracle.target, &now).unwrap();
            (harvest.lp_reward, harvest.rewards.len() > 1)
        } else {
            transaction.execute("UPDATE work_game_tasks SET job_key=?1,status='claimable',stage_index=3,started_at=?2,next_care_at=NULL,claimable_at=?2 WHERE id=?3",params![oracle.config,now,oracle.target]).unwrap();
            let reward = work_game::claim_task_tx(&transaction, &oracle.game, &oracle.target, &now)
                .unwrap()
                .reward;
            (reward.lp_reward, reward.rewards.len() > 1)
        };
        transaction.rollback().unwrap();
        if result == (desired, bonus) {
            return candidate;
        }
    }
    panic!(
        "real repository could not find LP/bonus boundary for {}",
        oracle.config
    );
}

fn cycle(
    oracle: &mut Oracle,
    start: Timestamp,
    duration: i64,
    care: i64,
    detailed: bool,
    faults: bool,
) -> Value {
    let farm = oracle.game.is_empty();
    let begin = if farm { "plant" } else { "start" };
    let tend = if farm { "water" } else { "care" };
    let claim = if farm { "harvest" } else { "claim" };
    let state = if farm { "farm_state" } else { "work_state" };
    let prefix = if detailed { "first" } else { "second" };
    if faults {
        for fault in if farm {
            vec!["seed", "state"]
        } else {
            vec!["state"]
        } {
            oracle.step(
                &format!("{prefix}-begin-{fault}-rollback"),
                begin,
                start,
                true,
                fault,
            );
        }
    }
    oracle.step(&format!("{prefix}-begin"), begin, start, false, "");
    oracle.step(&format!("{prefix}-duplicate-begin"), begin, start, true, "");
    oracle.step(
        &format!("{prefix}-clock-backwards"),
        state,
        shift(start, -1000),
        false,
        "",
    );
    let interval = (duration / (care + 1)).max(60) * 1000;
    let mut current = start;
    for stage in 1..=care {
        let due = shift(current, interval);
        if detailed {
            oracle.step(
                &format!("{prefix}-stage-{stage}-before"),
                state,
                shift(due, -1001),
                false,
                "",
            );
            oracle.step(
                &format!("{prefix}-stage-{stage}-early"),
                tend,
                shift(due, -1001),
                true,
                "",
            );
            oracle.step(
                &format!("{prefix}-stage-{stage}-subsecond"),
                state,
                shift(due, -1),
                false,
                "",
            );
        }
        current = if stage == 1 && detailed {
            shift(due, 2 * 86_400_000)
        } else {
            due
        };
        oracle.step(
            &format!("{prefix}-stage-{stage}-due"),
            state,
            current,
            false,
            "",
        );
        if faults && stage == 1 {
            oracle.step(
                &format!("{prefix}-care-rollback"),
                tend,
                current,
                true,
                "state",
            );
        }
        oracle.step(
            &format!("{prefix}-stage-{stage}-care"),
            tend,
            current,
            false,
            "",
        );
        oracle.step(
            &format!("{prefix}-stage-{stage}-repeat"),
            tend,
            current,
            true,
            "",
        );
    }
    let due = shift(current, interval);
    oracle.step(
        &format!("{prefix}-claim-early"),
        claim,
        shift(due, -1001),
        true,
        "",
    );
    oracle.step(
        &format!("{prefix}-claim-subsecond"),
        state,
        shift(due, -1),
        false,
        "",
    );
    if faults {
        let inventory = if detailed {
            "inventory-insert"
        } else {
            "inventory-update"
        };
        for fault in ["event", inventory, "wallet", "economy", "ledger", "clear"] {
            oracle.step(
                &format!("{prefix}-claim-{fault}-rollback"),
                claim,
                due,
                true,
                fault,
            );
        }
    }
    let result = oracle.step(&format!("{prefix}-claim"), claim, due, false, "");
    oracle.step(&format!("{prefix}-claim-repeat"), claim, due, true, "");
    oracle.step(&format!("{prefix}-market"), "market", due, false, "");
    result[if farm { "harvest" } else { "reward" }].clone()
}

pub(super) fn export(directory: &Path) {
    assert!(directory.is_absolute());
    let mut cases = Vec::new();
    let mut definitions = Vec::new();
    for crop in farm::crop_configs() {
        definitions.push(json!({"name":format!("farm-{}",crop.crop_key),"target":"farm-plot-1","config":crop.crop_key,"gameKey":"","duration":crop.duration_seconds,"care":crop.water_required,"minimum":crop.lp_min,"maximum":crop.lp_max,"bonus":crop.bonus_reward_item_key.is_some(),"seed":crop.seed_item_key}));
    }
    let mut probe = Connection::open_in_memory().unwrap();
    super::super::schema::apply_test_schema(&probe).unwrap();
    let transaction = probe.transaction().unwrap();
    for game in ["mine", "factory", "convenience-store"] {
        for job in work_game::state_tx(&transaction, game, BASE_NOW)
            .unwrap()
            .jobs
        {
            definitions.push(json!({"name":format!("work-{game}-{}",job.job_key),"target":format!("{game}-slot-{}",job.slot_index),"config":job.job_key,"gameKey":game,"duration":job.duration_seconds,"care":job.care_required,"minimum":job.lp_min,"maximum":job.lp_max,"bonus":job.bonus_reward_item_key.is_some(),"seed":""}));
        }
    }
    let mut application_case = definitions
        .iter()
        .find(|definition| definition["name"] == "work-factory-basic-assembly")
        .unwrap()
        .clone();
    application_case["name"] = json!("work-factory-basic-assembly-application");
    application_case["application"] = json!(true);
    definitions.push(application_case);
    transaction.rollback().unwrap();
    for definition in &definitions {
        let name = definition["name"].as_str().unwrap();
        let baseline = directory.join(format!("gameplay-full-{name}-base.sqlite3"));
        OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&baseline)
            .unwrap();
        let mut connection = Connection::open(&baseline).unwrap();
        super::super::schema::apply_test_schema(&connection).unwrap();
        let _clock = freeze_test_clock(BASE_NOW).unwrap();
        let transaction = connection.transaction().unwrap();
        transaction.execute("INSERT INTO pet_profile(id,name,level,experience,stage,current_mood,created_at,updated_at) VALUES('default-pet','Synthetic full gameplay',1,0,'first_meet','idle',?1,?1)",params![BASE_NOW]).unwrap();
        pet_store::ensure_store_defaults_tx(&transaction, BASE_NOW).unwrap();
        if !definition["seed"].as_str().unwrap().is_empty() {
            let item =
                pet_store::find_catalog_item_by_key(definition["seed"].as_str().unwrap()).unwrap();
            pet_store::grant_catalog_item_tx(&transaction, &item, 3, "synthetic_fixture", BASE_NOW)
                .unwrap();
        }
        farm::work_market_state_tx(&transaction, BASE_NOW).unwrap();
        transaction.commit().unwrap();
        let initial = snapshot(&connection);
        connection.close().unwrap();
        let database = directory.join(format!("gameplay-full-{name}-rust.sqlite3"));
        std::fs::copy(&baseline, &database).unwrap();
        let mut oracle = Oracle {
            connection: Connection::open(&database).unwrap(),
            target: definition["target"].as_str().unwrap().into(),
            config: definition["config"].as_str().unwrap().into(),
            game: definition["gameKey"].as_str().unwrap().into(),
            application: definition["application"].as_bool().unwrap_or(false),
            steps: Vec::new(),
        };
        let base = parse_iso(BASE_NOW).unwrap();
        let state = if oracle.game.is_empty() {
            "farm_state"
        } else {
            "work_state"
        };
        oracle.step("initial-state", state, base, false, "");
        let first = select_start(
            &mut oracle,
            base,
            definition["minimum"].as_i64().unwrap(),
            definition["maximum"].as_i64().unwrap(),
            false,
        );
        let second_base = shift(base, 7 * 86_400_000);
        let has_bonus = definition["bonus"].as_bool().unwrap();
        let second = select_start(
            &mut oracle,
            second_base,
            if has_bonus {
                definition["minimum"].as_i64().unwrap()
            } else {
                definition["maximum"].as_i64().unwrap()
            },
            definition["maximum"].as_i64().unwrap(),
            has_bonus,
        );
        let detailed_faults = name == "farm-wheat"
            || name == "work-factory-basic-assembly"
            || name == "work-factory-basic-assembly-application";
        let first_reward = cycle(
            &mut oracle,
            first,
            definition["duration"].as_i64().unwrap(),
            definition["care"].as_i64().unwrap(),
            true,
            detailed_faults,
        );
        let second_reward = cycle(
            &mut oracle,
            second,
            definition["duration"].as_i64().unwrap(),
            definition["care"].as_i64().unwrap(),
            false,
            detailed_faults,
        );
        assert_eq!(first_reward["lpReward"], definition["minimum"]);
        assert_eq!(second_reward["lpReward"], definition["maximum"]);
        assert_eq!(first_reward["rewards"].as_array().unwrap().len(), 1);
        assert_eq!(
            second_reward["rewards"].as_array().unwrap().len(),
            if has_bonus { 2 } else { 1 }
        );
        for (suffix, target, config, game) in [
            (
                "unknown-target",
                "synthetic-missing".to_string(),
                oracle.config.clone(),
                oracle.game.clone(),
            ),
            (
                "unknown-config",
                oracle.target.clone(),
                "synthetic-missing".into(),
                oracle.game.clone(),
            ),
        ] {
            let input = json!({"name":suffix,"operation":if oracle.game.is_empty() {"plant"}else{"start"},"now":add_seconds(second,100_000).unwrap(),"target":target,"config":config,"gameKey":game,"fault":"","rejected":true,"limit":12});
            oracle.run(input);
        }
        for operation in if oracle.game.is_empty() {
            vec!["water", "harvest"]
        } else {
            vec!["care", "claim"]
        } {
            oracle.run(json!({"name":format!("unknown-target-{operation}"),"operation":operation,"now":add_seconds(second,100_000).unwrap(),"target":"synthetic-missing","config":oracle.config,"gameKey":oracle.game,"fault":"","rejected":true,"limit":12}));
        }
        if oracle.game.is_empty() {
            oracle.step(
                "last-seed-delete-rollback",
                "plant",
                shift(second, 200_000_000),
                true,
                "seed-delete",
            );
            oracle.step(
                "missing-seed-first-plant",
                "plant",
                shift(second, 200_000_000),
                false,
                "",
            );
            oracle.run(json!({"name":"missing-seed","operation":"plant","now":add_seconds(second,200_001).unwrap(),"target":"farm-plot-2","config":oracle.config,"gameKey":"","fault":"","rejected":true,"limit":12}));
            for limit in [0, 1, 12, 101] {
                let setup = if limit == 101 {
                    "WITH RECURSIVE numbers(value) AS (SELECT 1 UNION ALL SELECT value+1 FROM numbers WHERE value<105) INSERT INTO farm_harvest_ledger(id,plot_id,crop_key,rewards_json,lp_reward,created_at) SELECT 'synthetic-history-'||value,plot_id,crop_key,rewards_json,lp_reward,strftime('%Y-%m-%dT%H:%M:%S+00:00','2026-06-01','+'||value||' seconds') FROM numbers CROSS JOIN (SELECT * FROM farm_harvest_ledger ORDER BY created_at LIMIT 1)"
                } else {
                    ""
                };
                oracle.run(json!({"name":format!("harvest-ledger-limit-{limit}"),"operation":"harvest-ledger","now":add_seconds(second,200_002).unwrap(),"target":"","config":"","gameKey":"","fault":"","rejected":false,"limit":limit,"setupSql":setup}));
            }
        } else {
            for operation in ["work_state", "start", "care", "claim"] {
                oracle.run(json!({"name":format!("unknown-map-{operation}"),"operation":operation,"now":add_seconds(second,100_001).unwrap(),"target":oracle.target,"config":oracle.config,"gameKey":"synthetic-missing","fault":"","rejected":true,"limit":12}));
            }
            let wrong_slot = if oracle.target.ends_with('1') { 2 } else { 1 };
            oracle.run(json!({"name":"wrong-slot","operation":"start","now":add_seconds(second,100_002).unwrap(),"target":format!("{}-slot-{wrong_slot}",oracle.game),"config":oracle.config,"gameKey":oracle.game,"fault":"","rejected":true,"limit":12}));
            for operation in ["start", "care", "claim"] {
                let other = if oracle.game == "factory" {
                    "mine-slot-1"
                } else {
                    "factory-slot-1"
                };
                oracle.run(json!({"name":format!("wrong-map-{operation}"),"operation":operation,"now":add_seconds(second,100_003).unwrap(),"target":other,"config":oracle.config,"gameKey":oracle.game,"fault":"","rejected":true,"limit":12}));
            }
            oracle.run(json!({"name":"history-window","operation":"work_state","now":add_seconds(second,100_004).unwrap(),"target":oracle.target,"config":oracle.config,"gameKey":oracle.game,"fault":"","rejected":false,"limit":12,"setupSql":"WITH RECURSIVE numbers(value) AS (SELECT 1 UNION ALL SELECT value+1 FROM numbers WHERE value<13) INSERT INTO work_game_reward_ledger(id,game_key,task_id,job_key,rewards_json,lp_reward,created_at) SELECT 'synthetic-history-'||value,game_key,task_id,job_key,rewards_json,lp_reward,strftime('%Y-%m-%dT%H:%M:%S+00:00','2026-06-01','+'||value||' seconds') FROM numbers CROSS JOIN (SELECT * FROM work_game_reward_ledger ORDER BY created_at LIMIT 1)"}));
        }
        cases.push(
            json!({"name":name,"definition":definition,"initial":initial,"steps":oracle.steps}),
        );
        oracle.connection.close().unwrap();
    }
    let fixture = json!({"clockMode":"thread-local-fixed","queries":queries(),"cases":cases});
    OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(directory.join("gameplay-full-expected.json"))
        .unwrap()
        .write_all(&serde_json::to_vec(&fixture).unwrap())
        .unwrap();
}

#[test]
#[ignore = "explicit real Rust farm/work oracle with an isolated LIBERTY_NATIVE_FIXTURE_DIR"]
fn export_native_gameplay_compatibility_fixture() {
    let directory = std::path::PathBuf::from(
        std::env::var_os("LIBERTY_NATIVE_FIXTURE_DIR")
            .expect("set LIBERTY_NATIVE_FIXTURE_DIR to an isolated synthetic directory"),
    );
    super::export_gameplay_compatibility_fixture(&directory);
    export(&directory);
}
