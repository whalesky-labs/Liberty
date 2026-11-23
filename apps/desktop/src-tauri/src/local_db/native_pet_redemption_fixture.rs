use std::{fs::OpenOptions, io::Write, path::Path};

use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine as _};
use ring::{
    hmac,
    rand::{SecureRandom, SystemRandom},
    signature::{Ed25519KeyPair, KeyPair},
};
use rusqlite::{types::ValueRef, Connection};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};

use crate::infrastructure::{
    repositories::{pet, pet_redeem_key, pet_store},
    time::{freeze_test_clock, utc_now},
};

struct Signer {
    pair: Ed25519KeyPair,
    legacy: [u8; 32],
}

impl Signer {
    fn new() -> Self {
        let random = SystemRandom::new();
        let document = Ed25519KeyPair::generate_pkcs8(&random).unwrap();
        let pair = Ed25519KeyPair::from_pkcs8(document.as_ref()).unwrap();
        let mut legacy = [0; 32];
        random.fill(&mut legacy).unwrap();
        Self { pair, legacy }
    }

    fn signed(&self, payload: &str) -> String {
        let encoded = URL_SAFE_NO_PAD.encode(payload);
        let signature = self.pair.sign(encoded.as_bytes());
        format!("LIB1.{encoded}.{}", URL_SAFE_NO_PAD.encode(signature))
    }

    fn compact(&self, payload: &[u8]) -> String {
        let mut token = payload.to_vec();
        token.extend_from_slice(self.pair.sign(payload).as_ref());
        format!("LIB2.{}", URL_SAFE_NO_PAD.encode(token))
    }

    fn short(&self) -> String {
        let mut payload = vec![3, 1, 1, 2, 3, 4, 5, 6, 0xff, 0xff, 0, 40, 0, 7];
        let digest = Sha256::digest(b"wheat-seed");
        payload.extend_from_slice(&digest[..2]);
        payload.extend_from_slice(&[2, 0, 0, 0]);
        let key = hmac::Key::new(hmac::HMAC_SHA256, &self.legacy);
        let mut context = hmac::Context::with_key(&key);
        context.update(b"SYNTH.");
        context.update(&payload);
        payload.extend_from_slice(&context.sign().as_ref()[..12]);
        let alphabet = b"0123456789ABCDEFGHJKMNPQRSTVWXYZ";
        let mut encoded = String::from("SYNTH-");
        let mut buffer = 0_u32;
        let mut bits = 0;
        for byte in payload {
            buffer = (buffer << 8) | u32::from(byte);
            bits += 8;
            while bits >= 5 {
                bits -= 5;
                encoded.push(alphabet[((buffer >> bits) & 31) as usize] as char);
            }
        }
        if bits != 0 {
            encoded.push(alphabet[((buffer << (5 - bits)) & 31) as usize] as char);
        }
        encoded
    }
}

fn queries() -> Value {
    let mut queries = super::checkin_snapshot_queries();
    queries["events"] = json!("SELECT id,pet_id,event_type,event_source,event_value,event_time,metadata FROM pet_event_ledger ORDER BY rowid");
    queries["economy"] = json!("SELECT id,pet_id,entry_type,currency_key,amount,balance_after,source_type,source_key,metadata,created_at FROM pet_economy_ledger ORDER BY rowid");
    queries["redemptions"] = json!("SELECT id,pet_id,key_hash,code_prefix,campaign_id,reward_json,status,redeemed_at,metadata FROM pet_redeem_key_redemptions ORDER BY rowid");
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
                            ValueRef::Blob(_) => panic!("unexpected synthetic redemption blob"),
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
    let transaction = connection
        .transaction()
        .map_err(|error| error.to_string())?;
    pet::ensure_default_exists_tx(&transaction)?;
    if input["operation"] == "history" {
        let limit = input["limit"].as_u64().unwrap().clamp(1, 100) as usize;
        let history = pet_redeem_key::list_redemptions_tx(&transaction, limit)?;
        transaction.commit().map_err(|error| error.to_string())?;
        return Ok(json!(history));
    }
    pet_store::ensure_store_defaults_tx(&transaction, &utc_now())?;
    let profile = pet::load_profile_tx(&transaction)?;
    let (redemption, rewards, duplicate) = pet_redeem_key::redeem_key_tx(
        &transaction,
        profile,
        input["code"].as_str().unwrap(),
        &utc_now(),
    )?;
    transaction.commit().map_err(|error| error.to_string())?;
    pet::reconcile_profile_leveling(connection)?;
    let profile = pet::load_profile(connection)?;
    let state = pet_store::store_state(connection, profile)?;
    Ok(json!({"state":state,"redemption":redemption,"rewards":rewards,"duplicate":duplicate}))
}

fn view(connection: &mut Connection) -> Value {
    let transaction = connection.transaction().unwrap();
    pet::ensure_default_exists_tx(&transaction).unwrap();
    pet_store::ensure_store_defaults_tx(&transaction, &utc_now()).unwrap();
    transaction.commit().unwrap();
    pet::reconcile_profile_leveling(connection).unwrap();
    let profile = pet::load_profile(connection).unwrap();
    let state = pet_store::store_state(connection, profile).unwrap();
    let history = action(connection, &json!({"operation":"history","limit":100})).unwrap();
    json!({"state":state,"history":history,"events":pet::list_event_ledger(connection,100).unwrap(),"cosmetics":pet::list_cosmetic_unlocks(connection).unwrap()})
}

fn fault_sql(fault: &str) -> Option<String> {
    let target = match fault {
        "" => return None,
        "wallet" => "BEFORE UPDATE ON pet_wallets WHEN NEW.balance > OLD.balance",
        "economy" => "BEFORE INSERT ON pet_economy_ledger WHEN NEW.source_type='redeem_key'",
        "item-insert" => "BEFORE INSERT ON pet_inventory WHEN NEW.source='redeem_key'",
        "item-update" => "BEFORE UPDATE ON pet_inventory WHEN NEW.source='redeem_key'",
        "profile" => "BEFORE UPDATE ON pet_profile",
        "cosmetic" => "BEFORE INSERT ON pet_cosmetic_unlocks",
        "achievement" => "BEFORE INSERT ON pet_inventory WHEN NEW.source='achievement'",
        "event" => "BEFORE INSERT ON pet_event_ledger",
        "redemption" => "BEFORE INSERT ON pet_redeem_key_redemptions",
        _ => panic!("unknown synthetic redemption fault"),
    };
    Some(format!("CREATE TRIGGER synthetic_pet_redemption_failure {target} BEGIN SELECT liberty_checkin_fault_probe(); SELECT RAISE(ABORT,'synthetic-pet-redemption-write-failure'); END;"))
}

struct Oracle {
    connection: Connection,
    steps: Vec<Value>,
}

impl Oracle {
    fn run(&mut self, mut input: Value) {
        let clock = input["now"]
            .as_str()
            .unwrap_or("2026-11-18T12:00:00Z")
            .to_string();
        let _clock = freeze_test_clock(&clock).unwrap();
        input["now"] = json!(utc_now());
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
            "{}",
            input["name"]
        );
        let raw_error = result.as_ref().err().cloned();
        let output = if trigger.is_some() {
            assert_eq!(hits.load(std::sync::atomic::Ordering::SeqCst), 1);
            assert_eq!(
                raw_error.as_deref(),
                Some("synthetic-pet-redemption-write-failure")
            );
            json!({"fault":"synthetic-pet-redemption-write-failure"})
        } else {
            assert_eq!(hits.load(std::sync::atomic::Ordering::SeqCst), 0);
            result.unwrap_or_else(|error| json!({"error":error}))
        };
        let persisted = snapshot(&self.connection);
        if input["rejected"] == true {
            assert_eq!(before, persisted, "{} rollback", input["name"]);
        }
        self.reopen();
        assert_eq!(snapshot(&self.connection), persisted);
        if trigger.is_some() {
            self.connection
                .execute_batch("DROP TRIGGER synthetic_pet_redemption_failure")
                .unwrap();
        }
        let state = view(&mut self.connection);
        let after_read = snapshot(&self.connection);
        for _ in 0..2 {
            self.reopen();
            assert_eq!(snapshot(&self.connection), after_read);
            assert_eq!(view(&mut self.connection), state);
            assert_eq!(snapshot(&self.connection), after_read);
        }
        self.steps.push(json!({"input":input,"output":output,"rawError":raw_error,"trigger":trigger,"persisted":persisted,"reopenedState":state,"afterRead":after_read}));
    }

    fn reopen(&mut self) {
        let path = self.connection.path().unwrap().to_string();
        self.connection = Connection::open_in_memory().unwrap();
        self.connection = Connection::open(path).unwrap();
    }

    fn redeem(&mut self, name: &str, code: String, rejected: bool) {
        self.run(json!({"name":name,"operation":"redeem","code":code,"rejected":rejected}));
    }
}

fn payload(nonce: &str) -> Value {
    json!({"version":1,"campaignId":"synthetic-campaign","nonce":nonce,"issuedAt":"2026-11-18","expiresAt":null,"rewards":{"lp":40,"growthValue":7,"items":[{"itemKey":"wheat-seed","quantity":2}]}})
}

fn unicode_payload(name: &str, field: &str, raw: &str) -> String {
    let mut campaign = json!(format!("synthetic-unicode-{name}")).to_string();
    let mut nonce = json!(name).to_string();
    let mut extra = String::new();
    let mut rewards = String::from(r#"{"lp":1}"#);
    match field {
        "campaignId" => campaign = raw.into(),
        "nonce" => nonce = raw.into(),
        "issuedAt" | "expiresAt" => extra = format!(r#",{field:?}:{raw}"#),
        "itemKey" => rewards = format!(r#"{{"lp":1,"items":[{{"itemKey":{raw}}}]}}"#),
        "root" => extra = format!(",{raw}"),
        "rewards" => rewards = format!(r#"{{"lp":1,{raw}}}"#),
        "item" => rewards = format!(r#"{{"lp":1,"items":[{{"itemKey":"wheat-seed",{raw}}}]}}"#),
        _ => panic!("unknown synthetic unicode target"),
    }
    format!(r#"{{"version":1,"campaignId":{campaign},"nonce":{nonce},"rewards":{rewards}{extra}}}"#)
}

fn copy_database(source: &Path, destination: &Path) {
    let mut output = OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(destination)
        .unwrap();
    std::io::copy(&mut std::fs::File::open(source).unwrap(), &mut output).unwrap();
}

fn compact_payload() -> Vec<u8> {
    let mut bytes = vec![0; 28];
    bytes[0] = 2;
    bytes[2..6].copy_from_slice(&0x10203040_u32.to_be_bytes());
    bytes[6..14].copy_from_slice(&[1, 2, 3, 4, 5, 6, 7, 8]);
    bytes[14..16].copy_from_slice(&321_u16.to_be_bytes());
    bytes[16..18].copy_from_slice(&0xffff_u16.to_be_bytes());
    bytes[18..20].copy_from_slice(&40_u16.to_be_bytes());
    bytes[20..22].copy_from_slice(&7_u16.to_be_bytes());
    bytes
}

pub(super) fn export(directory: &Path) {
    let signer = Signer::new();
    let other = Signer::new();
    let public: [u8; 32] = signer.pair.public_key().as_ref().try_into().unwrap();
    let _keys = pet_redeem_key::use_test_redemption_keys(public, signer.legacy);
    let _clock = freeze_test_clock("2026-11-18T12:00:00Z").unwrap();
    let mut cases = Vec::new();
    let mut legacy_boundary = Value::Null;
    for name in [
        "signed", "parser", "unicode", "compact", "identity", "legacy", "faults", "history",
    ] {
        let baseline = directory.join(format!("pet-redemption-{name}-base.sqlite3"));
        OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&baseline)
            .unwrap();
        let mut connection = Connection::open(&baseline).unwrap();
        super::super::schema::apply_test_schema(&connection).unwrap();
        pet::ensure_default_exists(&connection).unwrap();
        let transaction = connection.transaction().unwrap();
        pet_store::ensure_store_defaults_tx(&transaction, &utc_now()).unwrap();
        transaction.commit().unwrap();
        if name == "legacy" {
            let fresh = directory.join("pet-redemption-legacy-unredeemed-base.sqlite3");
            let before = snapshot(&connection);
            connection.close().unwrap();
            copy_database(&baseline, &fresh);
            connection = Connection::open(&baseline).unwrap();
            let code = signer.short();
            let output =
                action(&mut connection, &json!({"operation":"redeem","code":code})).unwrap();
            assert_eq!(output["duplicate"], false);
            legacy_boundary = json!({"code":code,"before":before,"rustOutput":output,"rustPersisted":snapshot(&connection)});
        }
        let initial = snapshot(&connection);
        connection.close().unwrap();
        let database = directory.join(format!("pet-redemption-{name}-rust.sqlite3"));
        copy_database(&baseline, &database);
        let mut oracle = Oracle {
            connection: Connection::open(database).unwrap(),
            steps: Vec::new(),
        };
        match name {
            "signed" => {
                for (index, rewards) in [
                    json!({"lp":40}), json!({"growthValue":31}),
                    json!({"items":[{"itemKey":"wheat-seed"}]}),
                    json!({"lp":-7,"growthValue":-9,"items":[{"itemKey":"wheat-seed","quantity":255}]}),
                    json!({"lp":1,"growthValue":1,"items":[{"itemKey":"wheat-seed","quantity":99},{"itemKey":"sandwich-food","quantity":2}]}),
                    json!({"items":[{"itemKey":"wheat-seed","quantity":2},{"itemKey":"wheat-seed","quantity":3}]}),
                    json!({"items":[{"itemKey":"clover-bow","quantity":2}]}),
                    json!({"items":[{"itemKey":"clover-bow","quantity":1}]}),
                    json!({"lp":1,"items":[{"itemKey":" wheat-seed ","quantity":1}]}),
                ].into_iter().enumerate() {
                    let mut value = payload(&format!("signed-{index}"));
                    value["rewards"] = rewards;
                    value["version"] = json!(1 + index % 3);
                    let code = signer.signed(&value.to_string());
                    oracle.redeem(&format!("reward-{index}"), code.clone(), false);
                    oracle.redeem(&format!("duplicate-{index}"), format!(" \t{code}\n\u{3000}"), false);
                }
                for (index, now) in [
                    "2026-11-18T23:59:59Z",
                    "2026-11-19T00:00:00Z",
                    "2026-11-18T23:30:00-08:00",
                    "2026-11-19T00:30:00+08:00",
                ]
                .into_iter()
                .enumerate()
                {
                    let mut value = payload(&format!("expiry-{index}"));
                    value["expiresAt"] = json!("2026-11-18");
                    oracle.run(json!({"name":format!("utc-expiry-{index}"),"operation":"redeem","code":signer.signed(&value.to_string()),"now":now,"rejected":index==1 || index==2}));
                }
            }
            "parser" => {
                let base = payload("invalid");
                for (index, (field, replacement)) in [
                    ("version", json!(0)),
                    ("version", json!(4)),
                    ("campaignId", json!("  ")),
                    ("nonce", json!("")),
                    ("expiresAt", json!("2026-02-30")),
                    ("rewards", json!({})),
                    ("rewards", Value::Null),
                    ("rewards", json!({"lp":null,"growthValue":1})),
                    ("rewards", json!({"lp":1,"items":null})),
                    (
                        "rewards",
                        json!({"items":[{"itemKey":"wheat-seed","quantity":null}]}),
                    ),
                    (
                        "rewards",
                        json!({"items":[{"itemKey":"wheat-seed","quantity":0}]}),
                    ),
                    (
                        "rewards",
                        json!({"items":[{"itemKey":"wheat-seed","quantity":-1}]}),
                    ),
                    ("rewards", json!({"items":[{"itemKey":""}]})),
                    (
                        "rewards",
                        json!({"items":[{"itemKey":String::from("synthetic") + "-missing-item"}]}),
                    ),
                ]
                .into_iter()
                .enumerate()
                {
                    let mut value = base.clone();
                    value[field] = replacement;
                    oracle.redeem(
                        &format!("invalid-field-{index}"),
                        signer.signed(&value.to_string()),
                        true,
                    );
                }
                for (index, value) in [
                    r#"{"version":1,"version":1,"campaignId":"synthetic","nonce":"a","rewards":{"lp":1}}"#,
                    r#"{"Version":1,"campaignId":"synthetic","nonce":"a","rewards":{"lp":1}}"#,
                    r#"{"version":1,"campaignId":"synthetic","nonce":"a","rewards":{"lp":1,"lp":1}}"#,
                    r#"{"version":1,"campaignId":"synthetic","nonce":"a","rewards":{"items":[{"itemKey":"wheat-seed","quantity":1,"quantity":1}]}}"#,
                    r#"{"version":1,"campaignId":"synthetic","nonce":"a","rewards":{"lp":9223372036854775808}}"#,
                    r#"{"version":1,"campaignId":"synthetic","nonce":"a"}"#,
                    r#"{"version":1,"campaignId":"synthetic","nonce":"a","rewards":{"lp":1}} trailing"#,
                ].into_iter().enumerate() {
                    oracle.redeem(&format!("invalid-json-{index}"), signer.signed(value), true);
                }
                let mut unknown = payload("unknown-fields");
                unknown["syntheticUnknown"] = json!({"version":0});
                oracle.redeem(
                    "unknown-fields-retained-policy",
                    signer.signed(&unknown.to_string()),
                    false,
                );
                oracle.redeem(
                    "wrong-signer",
                    other.signed(&payload("wrong-signer").to_string()),
                    true,
                );
                let valid = signer.signed(&payload("tampered").to_string());
                let mut parts: Vec<_> = valid.split('.').map(str::to_string).collect();
                parts[1] = URL_SAFE_NO_PAD.encode(payload("different-content").to_string());
                oracle.redeem("tampered-content", parts.join("."), true);
                for (index, code) in [
                    "",
                    "LIB1.a.a",
                    "LIB1.!.!",
                    "LIB1.a.AA==",
                    "LIB2.a",
                    "LIB2.!",
                ]
                .into_iter()
                .enumerate()
                {
                    oracle.redeem(&format!("malformed-{index}"), code.into(), true);
                }
            }
            "unicode" => {
                for field in ["campaignId", "nonce", "issuedAt", "expiresAt", "itemKey"] {
                    for (index, raw) in [r#""\ud800""#, r#""\udc00""#].into_iter().enumerate() {
                        let name = format!("invalid-{field}-{index}");
                        oracle.redeem(
                            &name,
                            signer.signed(&unicode_payload(&name, field, raw)),
                            true,
                        );
                    }
                }
                for (index, raw) in [
                    r#""\ud800\u0041""#,
                    r#""\ud800\ud800""#,
                    r#""\ud800x""#,
                    r#""\udc00\ud800""#,
                    r#""\ud800\\udc00""#,
                    r#""\\ud800\udc00""#,
                    r#""\uD800""#,
                ]
                .into_iter()
                .enumerate()
                {
                    let name = format!("broken-pair-{index}");
                    oracle.redeem(
                        &name,
                        signer.signed(&unicode_payload(&name, "campaignId", raw)),
                        true,
                    );
                }
                for field in ["root", "rewards", "item"] {
                    for (index, raw) in [r#""\ud800":1"#, r#""\udc00":1"#].into_iter().enumerate() {
                        let name = format!("invalid-field-name-{field}-{index}");
                        oracle.redeem(
                            &name,
                            signer.signed(&unicode_payload(&name, field, raw)),
                            true,
                        );
                    }
                    for (index, raw) in [
                        r#""unknown":"\ud800""#,
                        r#""unknown":"\udc00""#,
                        r#""unknown":{"\ud800":"\udc00"}"#,
                        r#""unknown":[{"\udc00":"\ud800"},"\udc00"]"#,
                    ]
                    .into_iter()
                    .enumerate()
                    {
                        let name = format!("ignored-value-{field}-{index}");
                        oracle.redeem(
                            &name,
                            signer.signed(&unicode_payload(&name, field, raw)),
                            false,
                        );
                    }
                }
                for field in ["campaignId", "nonce", "issuedAt"] {
                    for (index, raw) in
                        [r#""\ud83d\ude00""#, r#""�""#, r#""\ufffd""#, r#""\\ud800""#]
                            .into_iter()
                            .enumerate()
                    {
                        let name = format!("valid-string-{field}-{index}");
                        oracle.redeem(
                            &name,
                            signer.signed(&unicode_payload(&name, field, raw)),
                            false,
                        );
                    }
                }
                for (index, raw) in [
                    r#""\ud83d\ude00":1"#,
                    r#""�":1"#,
                    r#""\\ud800":1"#,
                    r#""unknown":"\ud800","unknown":"\udc00""#,
                ]
                .into_iter()
                .enumerate()
                {
                    let name = format!("valid-field-name-{index}");
                    oracle.redeem(
                        &name,
                        signer.signed(&unicode_payload(&name, "root", raw)),
                        false,
                    );
                }
                for field in ["issuedAt", "expiresAt"] {
                    let name = format!("nullable-{field}");
                    oracle.redeem(
                        &name,
                        signer.signed(&unicode_payload(&name, field, "null")),
                        false,
                    );
                }
                for field in ["expiresAt", "itemKey"] {
                    for (index, raw) in [r#""\ud83d\ude00""#, r#""�""#, r#""\\ud800""#]
                        .into_iter()
                        .enumerate()
                    {
                        let name = format!("valid-string-business-rejection-{field}-{index}");
                        oracle.redeem(
                            &name,
                            signer.signed(&unicode_payload(&name, field, raw)),
                            true,
                        );
                    }
                }
            }
            "compact" => {
                for count in 0..=2 {
                    let mut bytes = compact_payload();
                    bytes[1] = count;
                    bytes[13] = count;
                    bytes[18..20].copy_from_slice(&u16::MAX.to_be_bytes());
                    bytes[20..22].copy_from_slice(&u16::MAX.to_be_bytes());
                    for (index, item) in ["wheat-seed", "sandwich-food"]
                        .into_iter()
                        .take(count as usize)
                        .enumerate()
                    {
                        let digest = Sha256::digest(item.as_bytes());
                        let offset = 22 + index * 3;
                        bytes[offset..offset + 2].copy_from_slice(&digest[..2]);
                        bytes[offset + 2] = 255;
                    }
                    oracle.redeem(
                        &format!("compact-items-{count}"),
                        signer.compact(&bytes),
                        false,
                    );
                }
                for (index, (offset, value)) in [(0, 3), (1, 3), (24, 0), (22, 0), (23, 0)]
                    .into_iter()
                    .enumerate()
                {
                    let mut bytes = compact_payload();
                    bytes[1] = 1;
                    let digest = Sha256::digest(b"wheat-seed");
                    bytes[22..24].copy_from_slice(&digest[..2]);
                    bytes[24] = 1;
                    bytes[offset] = value;
                    if offset == 22 || offset == 23 {
                        bytes[22] = 0;
                        bytes[23] = 0;
                    }
                    oracle.redeem(
                        &format!("compact-invalid-{index}"),
                        signer.compact(&bytes),
                        true,
                    );
                }
                for length in [0, 27, 29] {
                    oracle.redeem(
                        &format!("compact-length-{length}"),
                        signer.compact(&vec![0; length]),
                        true,
                    );
                }
                let mut bytes = compact_payload();
                bytes[13] = 20;
                bytes[16..18].copy_from_slice(&321_u16.to_be_bytes());
                oracle.redeem("compact-expiry-today", signer.compact(&bytes), false);
                bytes[13] = 21;
                bytes[16..18].copy_from_slice(&320_u16.to_be_bytes());
                oracle.redeem("compact-expired", signer.compact(&bytes), true);
                bytes[13] = 22;
                oracle.redeem("compact-wrong-signer", other.compact(&bytes), true);
            }
            "identity" => {
                let mut original = payload("same-nonce");
                original["expiresAt"] = json!("2026-11-18");
                let code = signer.signed(&original.to_string());
                oracle.redeem("first-identity", code.clone(), false);
                oracle.run(json!({"name":"expired-same-hash","operation":"redeem","code":code,"now":"2030-01-01T00:00:00Z"}));
                original["issuedAt"] = json!("2026-11-17");
                oracle.run(json!({"name":"expired-reissued-identity","operation":"redeem","code":signer.signed(&original.to_string()),"now":"2030-01-01T00:00:00Z"}));
                original["expiresAt"] = json!("synthetic-invalid-date");
                oracle.redeem(
                    "existing-identity-invalid-expiration",
                    signer.signed(&original.to_string()),
                    false,
                );
                original["rewards"] = json!({"lp":0});
                oracle.redeem(
                    "existing-identity-invalid-rewards",
                    signer.signed(&original.to_string()),
                    true,
                );
                original["rewards"] = json!({"lp":999});
                original["expiresAt"] = Value::Null;
                oracle.redeem(
                    "existing-identity-changed-rewards",
                    signer.signed(&original.to_string()),
                    false,
                );
                original["campaignId"] = json!("synthetic-other-campaign");
                oracle.redeem(
                    "same-nonce-different-campaign",
                    signer.signed(&original.to_string()),
                    false,
                );
                original["nonce"] = json!("different-nonce");
                oracle.redeem(
                    "same-campaign-different-nonce",
                    signer.signed(&original.to_string()),
                    false,
                );
                let bytes = compact_payload();
                oracle.redeem("cross-format-compact", signer.compact(&bytes), false);
                let value = json!({"version":2,"campaignId":"campaign-10203040","nonce":"0102030405060708","issuedAt":"2026-11-18","expiresAt":null,"rewards":{"lp":40,"growthValue":7}});
                oracle.redeem(
                    "cross-format-lib1",
                    signer.signed(&value.to_string()),
                    false,
                );
                oracle.redeem(
                    "duplicate-identity-wrong-signature",
                    other.signed(&value.to_string()),
                    true,
                );
            }
            "legacy" => {
                oracle.redeem(
                    "old-short-hash-duplicate",
                    legacy_boundary["code"].as_str().unwrap().into(),
                    false,
                );
                let mut value = json!({"version":3,"campaignId":"SYNTH","nonce":"010203040506","issuedAt":null,"expiresAt":null,"rewards":{"lp":40,"growthValue":7,"items":[{"itemKey":"wheat-seed","quantity":2}]}});
                oracle.redeem(
                    "signed-reissue-same-identity",
                    signer.signed(&value.to_string()),
                    false,
                );
                value["expiresAt"] = json!("2026-01-01");
                oracle.redeem(
                    "expired-reissue-same-identity",
                    signer.signed(&value.to_string()),
                    false,
                );
                value["nonce"] = json!("010203040507");
                value["expiresAt"] = Value::Null;
                oracle.redeem(
                    "signed-reissue-new-identity",
                    signer.signed(&value.to_string()),
                    false,
                );
            }
            "faults" => {
                for fault in [
                    "wallet",
                    "economy",
                    "item-insert",
                    "item-update",
                    "profile",
                    "cosmetic",
                    "achievement",
                    "event",
                    "redemption",
                ] {
                    let value = payload(&format!("fault-{fault}"));
                    let code = signer.signed(&value.to_string());
                    let setup = if fault == "item-insert" {
                        "DELETE FROM pet_inventory WHERE item_key='wheat-seed'".into()
                    } else if fault == "cosmetic" || fault == "achievement" {
                        let experience =
                            super::super::pet_leveling::total_required_exp_for_level(11) - 1;
                        format!("UPDATE pet_profile SET level=10,experience={experience},stage='first_meet'; DELETE FROM pet_cosmetic_unlocks; DELETE FROM pet_inventory WHERE source='achievement'; INSERT OR REPLACE INTO pet_milestone_counters VALUES ('default-pet','tasks_created',1,'synthetic-eligibility','2026-11-18T12:00:00+00:00')")
                    } else {
                        String::new()
                    };
                    oracle.run(json!({"name":format!("fault-{fault}"),"operation":"redeem","code":code,"fault":fault,"setupSql":setup,"rejected":true}));
                    oracle.redeem(&format!("retry-{fault}"), code, false);
                }
            }
            "history" => {
                for index in 0..27 {
                    let mut value = payload(&format!("history-{index:02}"));
                    value["rewards"] = json!({"lp":1});
                    oracle.redeem(
                        &format!("same-time-{index:02}"),
                        signer.signed(&value.to_string()),
                        false,
                    );
                }
                for limit in [0, 1, 20, 100, 101] {
                    oracle.run(json!({"name":format!("history-limit-{limit}"),"operation":"history","limit":limit}));
                }
            }
            _ => unreachable!(),
        }
        cases.push(json!({"name":name,"initial":initial,"steps":oracle.steps}));
    }
    let mut output = OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(directory.join("pet-redemption-expected.json"))
        .unwrap();
    output.write_all(serde_json::to_string_pretty(&json!({"clockMode":"thread-local-fixed","publicKey":URL_SAFE_NO_PAD.encode(public),"queries":queries(),"cases":cases,"legacyBoundary":legacy_boundary})).unwrap().as_bytes()).unwrap();
}

#[test]
#[ignore = "explicit synthetic Rust redemption oracle export"]
fn export_native_pet_redemption_fixture() {
    let directory = std::path::PathBuf::from(
        std::env::var_os("LIBERTY_NATIVE_FIXTURE_DIR")
            .expect("explicit isolated synthetic fixture directory"),
    );
    assert!(directory.is_absolute() && directory.is_dir());
    export(&directory);
}
