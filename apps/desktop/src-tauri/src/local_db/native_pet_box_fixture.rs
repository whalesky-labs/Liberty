use std::{fs::OpenOptions, io::Write, path::Path};

use rusqlite::{types::ValueRef, Connection};
use serde_json::{json, Value};

use crate::infrastructure::{
    repositories::{pet, pet_blind_box, pet_store},
    time::{freeze_test_clock, local_date_string},
};

fn queries() -> Value {
    let mut queries = super::checkin_snapshot_queries();
    queries["events"] = json!("SELECT id,pet_id,event_type,event_source,event_value,event_time,metadata FROM pet_event_ledger ORDER BY rowid");
    queries["economy"] = json!("SELECT id,pet_id,entry_type,currency_key,amount,balance_after,source_type,source_key,metadata,created_at FROM pet_economy_ledger ORDER BY rowid");
    queries["draws"] = json!("SELECT id,pet_id,draw_date,item_key,item_type,quantity,duplicate_compensation_lp,created_at FROM pet_blind_box_draws ORDER BY rowid");
    queries
}

fn snapshot(connection: &Connection) -> Value {
    let mut snapshot = serde_json::Map::new();
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
                            ValueRef::Blob(_) => panic!("unexpected synthetic box blob"),
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

fn action(connection: &mut Connection, input: &Value) -> Result<Value, String> {
    let now = input["now"].as_str().unwrap();
    let transaction = connection
        .transaction()
        .map_err(|error| error.to_string())?;
    pet::ensure_default_exists_tx(&transaction)?;
    pet_store::ensure_store_defaults_tx(&transaction, now)?;
    let profile = pet::load_profile_tx(&transaction)?;
    let operation = input["operation"].as_str().unwrap();
    let result = match operation {
        "gift-state" | "blind-state" => Value::Null,
        "gift" => {
            let (prize, duplicate, compensation) = pet_store::open_gift_box_tx(&transaction, now)?;
            json!({"prize":prize,"duplicate":duplicate,"duplicateCompensationLp":compensation})
        }
        "use-gift" => {
            pet_store::use_item_tx(&transaction, pet_store::GIFT_BOX_ITEM_KEY, 1, now)?;
            Value::Null
        }
        "blind" => json!(pet_blind_box::draw_blind_box_tx(
            &transaction,
            &profile,
            now
        )?),
        _ => panic!("unknown box operation {operation}"),
    };
    transaction.commit().map_err(|error| error.to_string())?;
    if operation.ends_with("state") {
        pet::reconcile_profile_leveling(connection)?;
    }
    let profile = pet::load_profile(connection)?;
    match operation {
        "blind-state" => {
            pet_blind_box::blind_box_state(connection, profile).map(|state| json!(state))
        }
        "blind" => {
            pet_blind_box::draw_result(connection, profile, serde_json::from_value(result).unwrap())
                .map(|result| json!(result))
        }
        _ => {
            let state = pet_store::store_state(connection, profile)?;
            if operation == "gift" {
                let mut result = result;
                result["state"] = json!(state);
                Ok(result)
            } else {
                Ok(json!(state))
            }
        }
    }
}

fn gift_pool(connection: &Connection) -> Vec<(String, i64)> {
    let state = pet_store::store_state(connection, pet::load_profile(connection).unwrap()).unwrap();
    state
        .catalog
        .into_iter()
        .filter(|state| {
            let item = &state.item;
            item.enabled
                && !matches!(item.item_type.as_str(), "pet" | "badge" | "seed")
                && item.item_key != pet_store::GIFT_BOX_ITEM_KEY
                && state.locked_reason_zh.is_empty()
        })
        .map(|state| {
            let item = state.item;
            let type_weight = match item.item_type.as_str() {
                "food" => 42,
                "tool" => 34,
                "cosmetic" => 16,
                "theme" => 10,
                _ => 1,
            };
            let rarity_weight = match item.rarity.as_str() {
                "first_meet" => 18,
                "familiar" => 14,
                "grow_together" => 10,
                "deep_bond" => 6,
                "bond_forever" => 1,
                _ => 8,
            };
            (item.item_key, type_weight * rarity_weight)
        })
        .collect()
}

fn fault_sql(fault: &str) -> Option<String> {
    let target = match fault {
        "" => return None,
        "inventory" => "BEFORE UPDATE ON pet_inventory WHEN NEW.item_key != 'libby-default' AND NEW.quantity > OLD.quantity",
        "wallet" => "BEFORE UPDATE ON pet_wallets WHEN NEW.lifetime_earned > OLD.lifetime_earned",
        "economy" => "BEFORE INSERT ON pet_economy_ledger",
        "gift-consume" => "BEFORE UPDATE ON pet_inventory WHEN OLD.item_key = 'gift-box-tool' AND NEW.quantity < OLD.quantity",
        "gift-event" => "BEFORE INSERT ON pet_event_ledger WHEN NEW.event_type LIKE 'gift_box_%'",
        "blind-event" => "BEFORE INSERT ON pet_event_ledger WHEN NEW.event_type LIKE 'blind_box_%'",
        "profile" => "BEFORE UPDATE ON pet_profile WHEN NEW.current_mood != OLD.current_mood",
        "draw" => "BEFORE INSERT ON pet_blind_box_draws",
        _ => panic!("unknown box fault {fault}"),
    };
    Some(format!("CREATE TRIGGER synthetic_pet_box_failure {target} BEGIN SELECT liberty_checkin_fault_probe(); SELECT RAISE(ABORT,'synthetic-pet-box-write-failure'); END;"))
}

struct Oracle {
    connection: Connection,
    steps: Vec<Value>,
    blind: bool,
}

impl Oracle {
    #[allow(clippy::too_many_arguments)]
    fn run(
        &mut self,
        name: &str,
        operation: &str,
        now: String,
        prize: &str,
        fault: &str,
        rejected: bool,
    ) {
        let _clock = freeze_test_clock(&now).unwrap();
        let input = json!({"name":name,"operation":operation,"now":now,"date":local_date_string(),"expectedPrize":prize,"fault":fault,"rejected":rejected});
        let hits = super::checkin_install_fault_probe(&self.connection);
        let trigger = fault_sql(fault);
        if let Some(trigger) = &trigger {
            self.connection.execute_batch(trigger).unwrap();
        }
        let before = snapshot(&self.connection);
        let result = action(&mut self.connection, &input);
        assert_eq!(
            result.is_err(),
            rejected,
            "{input}: {:?}",
            result.as_ref().err()
        );
        let raw_error = result.as_ref().err().cloned();
        let output = if trigger.is_some() {
            assert_eq!(hits.load(std::sync::atomic::Ordering::SeqCst), 1, "{input}");
            assert_eq!(
                raw_error.as_deref(),
                Some("synthetic-pet-box-write-failure")
            );
            json!({"fault":"synthetic-pet-box-write-failure"})
        } else {
            assert_eq!(hits.load(std::sync::atomic::Ordering::SeqCst), 0);
            result.unwrap_or_else(|error| json!({"error":error}))
        };
        if !prize.is_empty() && !rejected {
            if operation == "use-gift" {
                let metadata: String = self
                    .connection
                    .query_row(
                        "SELECT metadata FROM pet_event_ledger ORDER BY rowid DESC LIMIT 1",
                        [],
                        |row| row.get(0),
                    )
                    .unwrap();
                assert_eq!(
                    serde_json::from_str::<Value>(&metadata).unwrap()["itemKey"],
                    prize
                );
            } else {
                assert_eq!(output["prize"]["itemKey"], prize, "{input}");
            }
        }
        let persisted = snapshot(&self.connection);
        if rejected {
            assert_eq!(before, persisted, "box rollback: {input}");
        }
        let path = self.connection.path().unwrap().to_string();
        self.connection = Connection::open_in_memory().unwrap();
        self.connection = Connection::open(&path).unwrap();
        assert_eq!(snapshot(&self.connection), persisted);
        if trigger.is_some() {
            self.connection
                .execute_batch("DROP TRIGGER synthetic_pet_box_failure")
                .unwrap();
        }
        let read = json!({"operation":if self.blind {"blind-state"} else {"gift-state"},"now":now});
        let state = action(&mut self.connection, &read).unwrap();
        let after_read = snapshot(&self.connection);
        self.connection = Connection::open_in_memory().unwrap();
        self.connection = Connection::open(&path).unwrap();
        assert_eq!(snapshot(&self.connection), after_read);
        assert_eq!(action(&mut self.connection, &read).unwrap(), state);
        assert_eq!(snapshot(&self.connection), after_read);
        self.steps.push(json!({"input":input,"output":output,"rawError":raw_error,"trigger":trigger,"persisted":persisted,"reopenedState":state,"afterRead":after_read}));
    }

    fn selected_clock(&self, pool: &[(String, i64)], index: usize, end: bool, day: i64) -> String {
        let anchor = "2026-07-01T12:00:00"
            .parse::<jiff::civil::DateTime>()
            .unwrap()
            .to_zoned(jiff::tz::TimeZone::system())
            .unwrap()
            .timestamp();
        let anchor =
            anchor.as_millisecond() + day * 86_400_000 + (self.steps.len() as i64 % 100) * 60_000;
        let profile = pet::load_profile(&self.connection).unwrap();
        let salt = if self.blind {
            let _clock = freeze_test_clock(
                &jiff::Timestamp::from_millisecond(anchor)
                    .unwrap()
                    .to_string(),
            )
            .unwrap();
            let used = pet_blind_box::blind_box_state(&self.connection, profile.clone())
                .unwrap()
                .used_today;
            profile.experience * 31 + used * 97
        } else {
            pool.len() as i64 * 137
        };
        let total: i64 = pool.iter().map(|(_, weight)| weight).sum();
        let mut cursor: i64 = pool[..index].iter().map(|(_, weight)| weight).sum();
        if end {
            cursor += pool[index].1 - 1;
        }
        let milliseconds = anchor + (cursor - (anchor + salt).rem_euclid(total)).rem_euclid(total);
        jiff::Timestamp::from_millisecond(milliseconds)
            .unwrap()
            .to_string()
    }
}

pub(super) fn export(directory: &Path) {
    let mut cases = Vec::new();
    for name in [
        "gift-default",
        "gift-unlocked",
        "gift-missing",
        "gift-zero",
        "blind",
    ] {
        let blind = name == "blind";
        let baseline = directory.join(format!("pet-box-{name}-base.sqlite3"));
        OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&baseline)
            .unwrap();
        let mut connection = Connection::open(&baseline).unwrap();
        super::super::schema::apply_test_schema(&connection).unwrap();
        let baseline_now = "2026-06-30T12:00:00Z";
        let _clock = freeze_test_clock(baseline_now).unwrap();
        let transaction = connection.transaction().unwrap();
        pet::ensure_default_exists_tx(&transaction).unwrap();
        pet_store::ensure_store_defaults_tx(&transaction, baseline_now).unwrap();
        if name == "gift-unlocked" {
            let mut profile = pet::load_profile_tx(&transaction).unwrap();
            profile.experience = super::super::pet_leveling::total_required_exp_for_level(255);
            profile.level_snapshot =
                super::super::pet_leveling::level_snapshot_from_experience(profile.experience);
            profile.level = profile.level_snapshot.level;
            profile.stage = profile.level_snapshot.current_stage.clone();
            pet::save_profile_tx(&transaction, &profile).unwrap();
        }
        if !blind && name != "gift-missing" {
            let item = pet_store::find_catalog_item_by_key(pet_store::GIFT_BOX_ITEM_KEY).unwrap();
            for _ in 0..4 {
                pet_store::grant_catalog_item_tx(
                    &transaction,
                    &item,
                    99,
                    "synthetic_fixture",
                    baseline_now,
                )
                .unwrap();
            }
            if name == "gift-zero" {
                transaction
                    .execute(
                        "UPDATE pet_inventory SET quantity=0 WHERE item_key='gift-box-tool'",
                        [],
                    )
                    .unwrap();
            }
        }
        if name == "blind" {
            let mut profile = pet::load_profile_tx(&transaction).unwrap();
            profile.experience = 13;
            profile.level_snapshot =
                super::super::pet_leveling::level_snapshot_from_experience(profile.experience);
            profile.level = profile.level_snapshot.level;
            profile.stage = profile.level_snapshot.current_stage.clone();
            pet::save_profile_tx(&transaction, &profile).unwrap();
        }
        transaction.commit().unwrap();
        let initial = snapshot(&connection);
        connection.close().unwrap();
        let database = directory.join(format!("pet-box-{name}-rust.sqlite3"));
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
            blind,
        };
        oracle.run(
            "initial-state",
            if blind { "blind-state" } else { "gift-state" },
            "2026-07-01T12:00:00Z".into(),
            "",
            "",
            false,
        );
        let mut pool = Vec::new();
        if name == "gift-missing" || name == "gift-zero" {
            oracle.run(
                "open-unavailable-gift",
                "gift",
                "2026-07-01T12:00:01Z".into(),
                "",
                "",
                true,
            );
            oracle.run(
                "use-unavailable-gift",
                "use-gift",
                "2026-07-01T12:00:02Z".into(),
                "",
                "",
                true,
            );
        } else {
            pool = if blind {
                pet_blind_box::blind_box_state(
                    &oracle.connection,
                    pet::load_profile(&oracle.connection).unwrap(),
                )
                .unwrap()
                .pool
                .into_iter()
                .map(|entry| (entry.item.item_key, entry.weight))
                .collect()
            } else {
                gift_pool(&oracle.connection)
            };
            let mut used = 0;
            let mut day = 0;
            for (index, (key, _)) in pool.iter().enumerate() {
                for end in [false, true] {
                    if blind && used == 10 {
                        let now = oracle.selected_clock(&pool, index, end, day);
                        oracle.run(
                            &format!("daily-exhausted-{day}"),
                            "blind",
                            now,
                            "",
                            "",
                            true,
                        );
                        day += 1;
                        used = 0;
                        let now = oracle.selected_clock(&pool, index, end, day);
                        oracle.run(
                            &format!("next-day-{day}"),
                            "blind-state",
                            now,
                            "",
                            "",
                            false,
                        );
                    }
                    let now = oracle.selected_clock(&pool, index, end, day);
                    oracle.run(
                        &format!("boundary-{key}-{}", if end { "end" } else { "start" }),
                        if blind { "blind" } else { "gift" },
                        now,
                        key,
                        "",
                        false,
                    );
                    used += 1;
                }
            }
            let index = pool
                .iter()
                .position(|(key, _)| key == "clover-bow")
                .unwrap();
            let now = oracle.selected_clock(&pool, index, false, day + 1);
            oracle.run(
                "same-clock-first",
                if blind { "blind" } else { "gift" },
                now.clone(),
                "clover-bow",
                "",
                false,
            );
            oracle.run(
                "same-clock-replay",
                if blind { "blind" } else { "gift" },
                now,
                if blind { "" } else { "clover-bow" },
                "",
                false,
            );
            let food_index = pool
                .iter()
                .position(|(key, _)| key == "chocolate-chip-cookie-food")
                .unwrap();
            for fault in if blind {
                vec![
                    "inventory",
                    "wallet",
                    "economy",
                    "blind-event",
                    "profile",
                    "draw",
                ]
            } else {
                vec![
                    "inventory",
                    "wallet",
                    "economy",
                    "gift-consume",
                    "gift-event",
                ]
            } {
                let index = if fault == "profile" {
                    pool.iter().position(|(key, _)| key == "nothing").unwrap()
                } else if fault == "inventory" {
                    food_index
                } else {
                    index
                };
                let now = oracle.selected_clock(&pool, index, false, day + 2);
                oracle.run(
                    &format!("rollback-{fault}"),
                    if blind { "blind" } else { "gift" },
                    now,
                    "",
                    fault,
                    true,
                );
            }
            if !blind {
                let now = oracle.selected_clock(&pool, food_index, false, 1);
                oracle.run(
                    "use-gift-production-entry",
                    "use-gift",
                    now,
                    "chocolate-chip-cookie-food",
                    "",
                    false,
                );
                oracle.run(
                    "before-epoch-clock",
                    "gift",
                    "1969-01-01T12:00:00Z".into(),
                    "",
                    "",
                    false,
                );
            } else {
                oracle.run(
                    "before-epoch-clock",
                    "blind",
                    "1969-01-01T12:00:00Z".into(),
                    "",
                    "",
                    false,
                );
            }
        }
        oracle.connection.close().unwrap();
        cases.push(
            json!({"name":name,"blind":blind,"initial":initial,"pool":pool,"steps":oracle.steps}),
        );
    }
    OpenOptions::new().write(true).create_new(true).open(directory.join("pet-box-expected.json")).unwrap().write_all(&serde_json::to_vec(&json!({"clockMode":"thread-local-fixed-milliseconds","queries":queries(),"cases":cases})).unwrap()).unwrap();
}

#[test]
#[ignore = "explicit isolated pet box fixture export"]
fn export_native_pet_box_compatibility_fixture() {
    let directory = std::path::PathBuf::from(
        std::env::var_os("LIBERTY_NATIVE_FIXTURE_DIR")
            .expect("explicit synthetic fixture directory"),
    );
    assert!(directory.is_absolute() && directory.is_dir());
    export(&directory);
}
