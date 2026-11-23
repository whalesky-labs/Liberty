use std::{fs::OpenOptions, io::Write, path::Path};

use rusqlite::{types::ValueRef, Connection};
use serde_json::{json, Value};

use crate::infrastructure::{
    repositories::{pet, pet_store},
    time::{freeze_test_clock, local_date_string},
};

fn queries() -> Value {
    let mut queries = super::checkin_snapshot_queries();
    queries["events"] = json!("SELECT id,pet_id,event_type,event_source,event_value,event_time,metadata FROM pet_event_ledger ORDER BY rowid");
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
                            ValueRef::Blob(_) => panic!("unexpected synthetic store blob"),
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
    let key = input["key"].as_str().unwrap();
    let quantity = input["quantity"].as_i64().unwrap();
    match input["operation"].as_str().unwrap() {
        "state" => {}
        "purchase" => pet_store::purchase_item_tx(&transaction, &profile, key, quantity, now)?,
        "use" => pet_store::use_item_tx(&transaction, key, quantity, now)?,
        "equip" => pet_store::equip_item_tx(&transaction, key, now)?,
        "unequip" => pet_store::unequip_slot_tx(&transaction, key, now)?,
        operation => panic!("unknown store operation {operation}"),
    }
    transaction.commit().map_err(|error| error.to_string())?;
    if input["operation"] == "state" {
        pet::reconcile_profile_leveling(connection)?;
    }
    pet_store::store_state(connection, pet::load_profile(connection)?).map(|state| json!(state))
}

fn inputs(funded: bool) -> Vec<Value> {
    let mut actions = Vec::new();
    let mut add = |name: &str,
                   operation: &str,
                   key: &str,
                   quantity: i64,
                   rejected: bool,
                   fault: &str| {
        let seconds = actions.len() as i64;
        let instant = "2026-07-01T12:00:00"
            .parse::<jiff::civil::DateTime>()
            .unwrap()
            .to_zoned(jiff::tz::TimeZone::system())
            .unwrap()
            .timestamp();
        let now = crate::infrastructure::time::add_seconds(instant, seconds).unwrap();
        actions.push(json!({"name":name,"operation":operation,"key":key,"quantity":quantity,"now":now,"rejected":rejected,"fault":fault,"databaseError":false}));
    };
    add("initial-state", "state", "", 0, false, "");
    add(
        "unknown-purchase",
        "purchase",
        "synthetic-missing",
        1,
        true,
        "",
    );
    add("badge-purchase", "purchase", "sun-badge", 1, true, "");
    add("level-gate", "purchase", "bell-accessory", 1, true, "");
    add("cosmetic-multiple", "purchase", "clover-bow", 2, true, "");
    add("unknown-use", "use", "synthetic-missing", 1, true, "");
    add("unknown-equip", "equip", "synthetic-missing", 1, true, "");
    add("pet-unequip", "unequip", "pet", 0, true, "");
    add("non-consumable-use", "use", "libby-default", 1, true, "");
    if funded {
        for (name, operation, key, quantity, rejected, fault) in [
            ("buy-accessory", "purchase", "clover-bow", 1, false, ""),
            ("rebuy-accessory", "purchase", "clover-bow", 1, true, ""),
            ("equip-accessory", "equip", "clover-bow", 0, false, ""),
            ("reequip-accessory", "equip", "clover-bow", 0, false, ""),
            ("unequip-accessory", "unequip", "accessory", 0, false, ""),
            ("unequip-empty-slot", "unequip", "accessory", 0, false, ""),
            (
                "unequip-unknown-slot",
                "unequip",
                "synthetic-slot",
                0,
                false,
                "",
            ),
            ("buy-scene", "purchase", "desk-lamp-scene", 1, false, ""),
            ("equip-scene", "equip", "desk-lamp-scene", 0, false, ""),
            ("equip-pet", "equip", "libby-default", 0, false, ""),
            ("equip-badge", "equip", "clover-badge", 0, false, ""),
            ("unequip-badge", "unequip", "badge", 0, false, ""),
            ("unequip-empty-badge", "unequip", "badge", 0, false, ""),
            (
                "buy-zero",
                "purchase",
                "chocolate-chip-cookie-food",
                0,
                false,
                "",
            ),
            (
                "buy-negative",
                "purchase",
                "chocolate-chip-cookie-food",
                i64::MIN,
                false,
                "",
            ),
            (
                "buy-two",
                "purchase",
                "chocolate-chip-cookie-food",
                2,
                false,
                "",
            ),
            (
                "equip-consumable",
                "equip",
                "chocolate-chip-cookie-food",
                0,
                true,
                "",
            ),
            (
                "use-zero",
                "use",
                "chocolate-chip-cookie-food",
                0,
                false,
                "",
            ),
            (
                "use-negative",
                "use",
                "chocolate-chip-cookie-food",
                i64::MIN,
                false,
                "",
            ),
            (
                "use-insufficient",
                "use",
                "chocolate-chip-cookie-food",
                3,
                true,
                "",
            ),
            (
                "use-exact",
                "use",
                "chocolate-chip-cookie-food",
                2,
                false,
                "",
            ),
            (
                "use-empty",
                "use",
                "chocolate-chip-cookie-food",
                1,
                true,
                "",
            ),
            (
                "buy-over-99",
                "purchase",
                "chocolate-chip-cookie-food",
                i64::MAX,
                false,
                "",
            ),
            (
                "purchase-wallet-rollback",
                "purchase",
                "chocolate-chip-cookie-food",
                1,
                true,
                "wallet",
            ),
            (
                "purchase-economy-rollback",
                "purchase",
                "chocolate-chip-cookie-food",
                1,
                true,
                "economy",
            ),
            (
                "purchase-inventory-rollback",
                "purchase",
                "chocolate-chip-cookie-food",
                1,
                true,
                "inventory",
            ),
            (
                "food-profile-rollback",
                "use",
                "chocolate-chip-cookie-food",
                1,
                true,
                "profile",
            ),
            (
                "food-event-rollback",
                "use",
                "chocolate-chip-cookie-food",
                99,
                true,
                "food-event",
            ),
            (
                "consume-delete-rollback",
                "use",
                "chocolate-chip-cookie-food",
                99,
                true,
                "delete",
            ),
            (
                "use-over-99",
                "use",
                "chocolate-chip-cookie-food",
                i64::MAX,
                false,
                "",
            ),
            (
                "buy-new-accessory",
                "purchase",
                "bell-accessory",
                1,
                false,
                "",
            ),
            (
                "equip-event-rollback",
                "equip",
                "bell-accessory",
                0,
                true,
                "equip-event",
            ),
            (
                "equip-new-accessory",
                "equip",
                "bell-accessory",
                0,
                false,
                "",
            ),
            ("stage-gate", "purchase", "strawberry-candy", 1, true, ""),
            ("buy-drink", "purchase", "bubble-tea-food", 2, false, ""),
            ("use-drink", "use", "bubble-tea-food", 2, false, ""),
            ("buy-meal", "purchase", "bento-box-food", 2, false, ""),
            ("use-meal", "use", "bento-box-food", 2, false, ""),
            ("buy-tool", "purchase", "energy-drink-tool", 1, false, ""),
            ("use-tool", "use", "energy-drink-tool", 1, false, ""),
            (
                "unequip-inventory-rollback",
                "unequip",
                "scene",
                0,
                true,
                "unequip-inventory",
            ),
            ("unequip-scene", "unequip", "scene", 0, false, ""),
        ] {
            add(name, operation, key, quantity, rejected, fault);
        }
    } else {
        add(
            "balance-insufficient",
            "purchase",
            "clover-bow",
            1,
            true,
            "",
        );
        add(
            "buy-exact-balance",
            "purchase",
            "chocolate-chip-cookie-food",
            1,
            false,
            "",
        );
        add(
            "balance-exhausted",
            "purchase",
            "chocolate-chip-cookie-food",
            1,
            true,
            "",
        );
        add(
            "use-last-food",
            "use",
            "chocolate-chip-cookie-food",
            1,
            false,
            "",
        );
    }
    add(
        "claim-over-daily-cap",
        "purchase",
        pet_store::GIFT_BOX_ITEM_KEY,
        i64::MAX,
        false,
        "",
    );
    add(
        "daily-exhausted",
        "purchase",
        pet_store::GIFT_BOX_ITEM_KEY,
        1,
        true,
        "",
    );
    add("next-day-state", "state", "", 0, false, "");
    add(
        "claim-zero",
        "purchase",
        pet_store::GIFT_BOX_ITEM_KEY,
        0,
        false,
        "",
    );
    add(
        "claim-inventory-rollback",
        "purchase",
        pet_store::GIFT_BOX_ITEM_KEY,
        1,
        true,
        "gift-inventory",
    );
    add(
        "claim-negative",
        "purchase",
        pet_store::GIFT_BOX_ITEM_KEY,
        -99,
        false,
        "",
    );
    add(
        "claim-over-remaining",
        "purchase",
        pet_store::GIFT_BOX_ITEM_KEY,
        99,
        true,
        "",
    );
    add(
        "claim-last",
        "purchase",
        pet_store::GIFT_BOX_ITEM_KEY,
        1,
        false,
        "",
    );
    add(
        "claim-last-replay",
        "purchase",
        pet_store::GIFT_BOX_ITEM_KEY,
        1,
        true,
        "",
    );
    let mut next_day = false;
    for input in &mut actions {
        if input["name"] == "next-day-state" {
            next_day = true;
        }
        if next_day {
            let now =
                crate::infrastructure::time::parse_iso(input["now"].as_str().unwrap()).unwrap();
            input["now"] = json!(crate::infrastructure::time::add_seconds(now, 86_400).unwrap());
        }
        let _clock = freeze_test_clock(input["now"].as_str().unwrap()).unwrap();
        input["date"] = json!(local_date_string());
    }
    if funded {
        let index = actions
            .iter()
            .position(|input| input["name"] == "buy-zero")
            .unwrap();
        let mut duplicate = actions[index].clone();
        duplicate["name"] = json!("identical-purchase-replay");
        duplicate["rejected"] = json!(true);
        duplicate["databaseError"] = json!(true);
        actions.insert(index + 1, duplicate);
        let index = actions
            .iter()
            .position(|input| input["name"] == "equip-accessory")
            .unwrap();
        actions[index + 1]["now"] = actions[index]["now"].clone();
    }
    actions
}

fn fault_sql(fault: &str) -> Option<String> {
    let target = match fault {
        "" => return None,
        "wallet" => "BEFORE UPDATE ON pet_wallets WHEN NEW.lifetime_spent > OLD.lifetime_spent",
        "economy" => "BEFORE INSERT ON pet_economy_ledger",
        "inventory" => "BEFORE UPDATE ON pet_inventory WHEN NEW.item_key = 'chocolate-chip-cookie-food' AND NEW.quantity > OLD.quantity",
        "profile" => "BEFORE UPDATE ON pet_profile WHEN NEW.experience > OLD.experience",
        "food-event" => "BEFORE INSERT ON pet_event_ledger WHEN NEW.event_type = 'store_food'",
        "equip-event" => "BEFORE INSERT ON pet_event_ledger WHEN NEW.event_type = 'store_equip'",
        "unequip-inventory" => "BEFORE UPDATE ON pet_inventory WHEN OLD.slot = 'scene' AND OLD.equipped = 1 AND NEW.equipped = 0",
        "delete" => "BEFORE DELETE ON pet_inventory WHEN OLD.item_key = 'chocolate-chip-cookie-food'",
        "gift-inventory" => "BEFORE UPDATE ON pet_inventory WHEN NEW.item_key = 'gift-box-tool' AND NEW.quantity > OLD.quantity",
        _ => panic!("unknown store fault {fault}"),
    };
    Some(format!("CREATE TRIGGER synthetic_pet_store_failure {target} BEGIN SELECT liberty_checkin_fault_probe(); SELECT RAISE(ABORT,'synthetic-pet-store-write-failure'); END;"))
}

pub(super) fn export(directory: &Path) {
    let mut cases = Vec::new();
    for funded in [false, true] {
        let name = if funded { "funded" } else { "exact-balance" };
        let baseline = directory.join(format!("pet-store-{name}-base.sqlite3"));
        OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&baseline)
            .unwrap();
        let baseline_now = "2026-06-30T12:00:00Z";
        let _clock = freeze_test_clock(baseline_now).unwrap();
        let mut connection = Connection::open(&baseline).unwrap();
        super::super::schema::apply_test_schema(&connection).unwrap();
        let transaction = connection.transaction().unwrap();
        pet::ensure_default_exists_tx(&transaction).unwrap();
        pet_store::ensure_store_defaults_tx(&transaction, baseline_now).unwrap();
        pet_store::grant_reward_tx(
            &transaction,
            "synthetic_fixture",
            "store-opening-balance",
            if funded { 20_000 } else { 25 },
            Some(r#"{"preciseInteger":9223372036854775806,"zero":0.0}"#),
            baseline_now,
        )
        .unwrap();
        if funded {
            assert!(pet_store::increment_counter_tx(
                &transaction,
                "tasks_created",
                "synthetic-pending-badge",
                baseline_now,
            )
            .unwrap());
            pet_store::grant_catalog_item_tx(
                &transaction,
                &pet_store::find_catalog_item_by_key("clover-badge").unwrap(),
                1,
                "synthetic_fixture",
                baseline_now,
            )
            .unwrap();
        }
        transaction.commit().unwrap();
        let initial = snapshot(&connection);
        connection.close().unwrap();
        let database = directory.join(format!("pet-store-{name}-rust.sqlite3"));
        let mut output = OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&database)
            .unwrap();
        std::io::copy(&mut std::fs::File::open(&baseline).unwrap(), &mut output).unwrap();
        drop(output);
        let mut steps = Vec::new();
        for input in inputs(funded) {
            let _clock = freeze_test_clock(input["now"].as_str().unwrap()).unwrap();
            assert_eq!(local_date_string(), input["date"].as_str().unwrap());
            let mut connection = Connection::open(&database).unwrap();
            let fault_hits = super::checkin_install_fault_probe(&connection);
            let trigger = fault_sql(input["fault"].as_str().unwrap());
            if let Some(trigger) = &trigger {
                connection.execute_batch(trigger).unwrap();
            }
            let before = snapshot(&connection);
            let result = action(&mut connection, &input);
            assert_eq!(
                result.is_err(),
                input["rejected"].as_bool().unwrap(),
                "{input}: {result:?}"
            );
            let raw_error = result.as_ref().err().cloned();
            let output = if trigger.is_some() {
                assert_eq!(fault_hits.load(std::sync::atomic::Ordering::SeqCst), 1);
                assert_eq!(
                    raw_error.as_deref(),
                    Some("synthetic-pet-store-write-failure")
                );
                json!({"fault":"synthetic-pet-store-write-failure"})
            } else if input["databaseError"] == true {
                assert_eq!(raw_error.as_deref(), Some("UNIQUE constraint failed: pet_economy_ledger.pet_id, pet_economy_ledger.currency_key, pet_economy_ledger.source_type, pet_economy_ledger.source_key"));
                json!({"databaseError":"duplicate-purchase-key"})
            } else {
                assert_eq!(fault_hits.load(std::sync::atomic::Ordering::SeqCst), 0);
                result.unwrap_or_else(|error| json!({"error":error}))
            };
            let persisted = snapshot(&connection);
            if input["rejected"] == true {
                assert_eq!(
                    persisted, before,
                    "rejected store action mutated tables: {input}"
                );
            }
            connection.close().unwrap();
            let mut reopened = Connection::open(&database).unwrap();
            assert_eq!(snapshot(&reopened), persisted);
            if trigger.is_some() {
                reopened
                    .execute_batch("DROP TRIGGER synthetic_pet_store_failure")
                    .unwrap();
            }
            let state_input = json!({"operation":"state","key":"","quantity":0,"now":input["now"]});
            let state = action(&mut reopened, &state_input).unwrap();
            let after_read = snapshot(&reopened);
            reopened.close().unwrap();
            let mut reopened = Connection::open(&database).unwrap();
            assert_eq!(snapshot(&reopened), after_read);
            assert_eq!(action(&mut reopened, &state_input).unwrap(), state);
            assert_eq!(snapshot(&reopened), after_read);
            reopened.close().unwrap();
            steps.push(json!({"input":input,"output":output,"rawError":raw_error,"trigger":trigger,"persisted":persisted,"reopenedState":state,"afterRead":after_read}));
        }
        cases.push(json!({"name":name,"initial":initial,"steps":steps}));
    }
    OpenOptions::new().write(true).create_new(true).open(directory.join("pet-store-expected.json")).unwrap()
        .write_all(serde_json::to_string_pretty(&json!({"clockMode":"thread-local-fixed","queries":queries(),"cases":cases,"excluded":["random gift-box opening","blind-box drawing"]})).unwrap().as_bytes()).unwrap();
}

#[test]
#[ignore = "explicit isolated pet store fixture export"]
fn export_native_pet_store_compatibility_fixture() {
    let directory = std::path::PathBuf::from(
        std::env::var_os("LIBERTY_NATIVE_FIXTURE_DIR")
            .expect("explicit synthetic fixture directory"),
    );
    assert!(directory.is_absolute() && directory.is_dir());
    export(&directory);
}
