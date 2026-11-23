use base64::{engine::general_purpose, Engine as _};
use jiff::{civil::Date, tz::TimeZone, ToSpan};
use ring::{
    hmac,
    signature::{UnparsedPublicKey, ED25519},
};
use rusqlite::{params, OptionalExtension, Transaction};
use serde::Deserialize;
use serde_json::json;
use sha2::{Digest, Sha256};

use crate::{
    infrastructure::{
        ids,
        repositories::{pet, pet_store},
    },
    local_db::{
        pet_leveling, LocalResult, PetEventLedgerEntry, PetProfile, PetRedeemKeyRedemption,
        PetRedeemKeyRewardItem, PetRedeemKeyRewards,
    },
};

const PET_ID: &str = "default-pet";
const REDEEM_KEY_LEGACY_PREFIX: &str = "LIB1";
const REDEEM_KEY_COMPACT_PREFIX: &str = "LIB2";
const REDEEM_KEY_PUBLIC_KEY_B64: &str =
    include_str!("../../../../../../packages/desktop-resources/redemption/public-key.b64");
const SHORT_KEY_SECRET_B64: &str = "B+6+O1eix4RItBiakvkqdGDRWlVK8I1m0ABDHgmJCXE=";
const SHORT_PAYLOAD_BYTES: usize = 20;
const SHORT_MAC_BYTES: usize = 12;
const SHORT_TOKEN_BYTES: usize = SHORT_PAYLOAD_BYTES + SHORT_MAC_BYTES;
const COMPACT_PAYLOAD_BYTES: usize = 28;
const COMPACT_SIGNATURE_BYTES: usize = 64;
const COMPACT_TOKEN_BYTES: usize = COMPACT_PAYLOAD_BYTES + COMPACT_SIGNATURE_BYTES;
const COMPACT_NO_EXPIRATION: u16 = 0xffff;
const COMPACT_BASE_DATE: &str = "2026-01-01";

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct RedeemKeyPayload {
    version: u32,
    campaign_id: String,
    #[serde(default)]
    nonce: String,
    #[serde(default)]
    issued_at: Option<String>,
    #[serde(default)]
    expires_at: Option<String>,
    rewards: PetRedeemKeyRewards,
}

pub fn redeem_key_tx(
    tx: &Transaction<'_>,
    profile: PetProfile,
    raw_key: &str,
    now: &str,
) -> LocalResult<(PetRedeemKeyRedemption, PetRedeemKeyRewards, bool)> {
    let normalized_key = normalize_redeem_key(raw_key);
    if normalized_key.is_empty() {
        return Err("请输入兑换 Key。".into());
    }

    let key_hash = redeem_key_hash(&normalized_key);
    if let Some(existing) = load_redemption_by_hash_tx(tx, &key_hash)? {
        let rewards =
            serde_json::from_str::<PetRedeemKeyRewards>(&existing.reward_json).unwrap_or_default();
        return Ok((existing, rewards, true));
    }

    let payload = verify_redeem_key(&normalized_key)?;
    validate_payload(&payload)?;
    validate_rewards(&payload.rewards)?;
    if let Some(existing) =
        load_redemption_by_identity_tx(tx, &payload.campaign_id, &payload.nonce)?
    {
        let rewards =
            serde_json::from_str::<PetRedeemKeyRewards>(&existing.reward_json).unwrap_or_default();
        return Ok((existing, rewards, true));
    }
    validate_expiration(&payload)?;

    let reward_json = serde_json::to_string(&payload.rewards).map_err(|err| err.to_string())?;
    let metadata = redeem_metadata(&payload, &key_hash);
    let mut profile = profile;
    grant_redeem_rewards_tx(
        tx,
        &mut profile,
        &payload.rewards,
        &key_hash,
        &metadata,
        now,
    )?;

    let redemption = PetRedeemKeyRedemption {
        id: ids::timestamped_id("pet-redeem"),
        pet_id: PET_ID.into(),
        key_hash,
        code_prefix: code_prefix(&normalized_key),
        campaign_id: payload.campaign_id,
        reward_json,
        status: "redeemed".into(),
        redeemed_at: now.into(),
        metadata: Some(metadata),
    };
    insert_redemption_tx(tx, &redemption)?;
    Ok((redemption, payload.rewards, false))
}

pub fn list_redemptions_tx(
    tx: &Transaction<'_>,
    limit: usize,
) -> LocalResult<Vec<PetRedeemKeyRedemption>> {
    let mut stmt = tx
        .prepare(
            "SELECT id, pet_id, key_hash, code_prefix, campaign_id, reward_json, status, redeemed_at, metadata
             FROM pet_redeem_key_redemptions
             WHERE pet_id = ?1
             ORDER BY datetime(redeemed_at) DESC, redeemed_at DESC
             LIMIT ?2",
        )
        .map_err(|err| err.to_string())?;
    let redemptions = stmt
        .query_map(params![PET_ID, limit.min(100) as i64], map_redemption)
        .map_err(|err| err.to_string())?
        .collect::<Result<Vec<_>, _>>()
        .map_err(|err| err.to_string())?;
    Ok(redemptions)
}

fn grant_redeem_rewards_tx(
    tx: &Transaction<'_>,
    profile: &mut PetProfile,
    rewards: &PetRedeemKeyRewards,
    key_hash: &str,
    metadata: &str,
    now: &str,
) -> LocalResult<()> {
    pet_store::grant_reward_tx(
        tx,
        "redeem_key",
        &format!("{key_hash}:lp"),
        rewards.lp.max(0),
        Some(metadata),
        now,
    )?;

    for reward_item in &rewards.items {
        let item = pet_store::find_catalog_item_by_key(reward_item.item_key.trim())
            .ok_or_else(|| format!("兑换奖励中包含未知道具：{}。", reward_item.item_key))?;
        pet_store::grant_catalog_item_tx(
            tx,
            &item,
            reward_item.quantity.clamp(1, 99),
            "redeem_key",
            now,
        )?;
    }

    let growth_value = rewards.growth_value.max(0);
    if growth_value > 0 {
        let previous_stage = profile.stage.clone();
        let next_experience = pet_store::checked_pet_experience(profile.experience, growth_value)?;
        let level_snapshot = pet_leveling::level_snapshot_from_experience(next_experience);
        profile.experience = next_experience;
        profile.level = level_snapshot.level;
        profile.stage = level_snapshot.current_stage.clone();
        profile.level_snapshot = level_snapshot;
        profile.current_mood = "proud".into();
        profile.updated_at = now.into();
        pet::save_profile_tx(tx, profile)?;
        pet::ensure_stage_cosmetic_unlocks_tx(tx, profile, &previous_stage, now)?;
        pet_store::auto_unlock_eligible_items_tx(tx, profile, now)?;
    }

    pet::insert_event_ledger_tx(
        tx,
        &PetEventLedgerEntry {
            id: ids::timestamped_id("pet-event"),
            pet_id: profile.id.clone(),
            event_type: "redeem_key".into(),
            event_source: "redeem_key".into(),
            event_value: growth_value,
            event_time: now.into(),
            metadata: Some(metadata.into()),
        },
    )
}

fn verify_redeem_key(key: &str) -> LocalResult<RedeemKeyPayload> {
    let parts = key.split('.').collect::<Vec<_>>();
    match parts.as_slice() {
        [REDEEM_KEY_LEGACY_PREFIX, _, _] => verify_legacy_redeem_key(&parts),
        [REDEEM_KEY_COMPACT_PREFIX, _] => verify_compact_redeem_key(&parts),
        _ => verify_short_redeem_key(key),
    }
}

fn verify_legacy_redeem_key(parts: &[&str]) -> LocalResult<RedeemKeyPayload> {
    let payload_part = parts[1];
    let signature_bytes = general_purpose::URL_SAFE_NO_PAD
        .decode(parts[2])
        .map_err(|_| "兑换 Key 签名格式不正确。".to_string())?;
    verify_signature(payload_part.as_bytes(), &signature_bytes)?;

    let payload_json = general_purpose::URL_SAFE_NO_PAD
        .decode(payload_part)
        .map_err(|_| "兑换 Key 内容格式不正确。".to_string())?;
    serde_json::from_slice::<RedeemKeyPayload>(&payload_json)
        .map_err(|_| "兑换 Key 内容无法解析。".to_string())
}

fn verify_compact_redeem_key(parts: &[&str]) -> LocalResult<RedeemKeyPayload> {
    let token = general_purpose::URL_SAFE_NO_PAD
        .decode(parts[1])
        .map_err(|_| "兑换 Key 内容格式不正确。".to_string())?;
    if token.len() != COMPACT_TOKEN_BYTES {
        return Err("兑换 Key 长度不正确。".into());
    }

    let (payload_bytes, signature_bytes) = token.split_at(COMPACT_PAYLOAD_BYTES);
    verify_signature(payload_bytes, signature_bytes)?;
    decode_compact_payload(payload_bytes)
}

fn verify_short_redeem_key(key: &str) -> LocalResult<RedeemKeyPayload> {
    let mut parts = key.split('-');
    let display_prefix = parts
        .next()
        .map(|value| value.trim().to_uppercase())
        .filter(|value| is_short_display_prefix(value))
        .ok_or_else(|| "兑换 Key 格式不正确。".to_string())?;
    let token_text = parts.collect::<Vec<_>>().join("");
    let token = decode_crockford_base32(&token_text)?;
    if token.len() != SHORT_TOKEN_BYTES {
        return Err("兑换 Key 长度不正确。".into());
    }

    let (payload_bytes, mac_bytes) = token.split_at(SHORT_PAYLOAD_BYTES);
    verify_short_mac(&display_prefix, payload_bytes, mac_bytes)?;
    decode_short_payload(&display_prefix, payload_bytes)
}

fn verify_short_mac(display_prefix: &str, payload: &[u8], mac_bytes: &[u8]) -> LocalResult<()> {
    let secret = legacy_mac_key()?;
    let key = hmac::Key::new(hmac::HMAC_SHA256, &secret);
    let mut context = hmac::Context::with_key(&key);
    context.update(display_prefix.as_bytes());
    context.update(b".");
    context.update(payload);
    let tag = context.sign();
    if &tag.as_ref()[..SHORT_MAC_BYTES] != mac_bytes {
        return Err("兑换 Key 校验失败。".into());
    }
    Ok(())
}

fn verify_signature(payload: &[u8], signature_bytes: &[u8]) -> LocalResult<()> {
    let public_key_bytes = redemption_public_key()?;
    if public_key_bytes.len() != 32 {
        return Err("兑换 Key 公钥长度不正确。".into());
    }
    let verifying_key = UnparsedPublicKey::new(&ED25519, public_key_bytes);
    verifying_key
        .verify(payload, signature_bytes)
        .map_err(|_| "兑换 Key 签名校验失败。".to_string())
}

fn redemption_public_key() -> LocalResult<Vec<u8>> {
    #[cfg(test)]
    if let Some((public, _)) = TEST_REDEMPTION_KEYS.with(std::cell::Cell::get) {
        return Ok(public.to_vec());
    }
    general_purpose::STANDARD
        .decode(REDEEM_KEY_PUBLIC_KEY_B64.trim())
        .map_err(|_| "兑换 Key 公钥配置不正确。".to_string())
}

fn legacy_mac_key() -> LocalResult<Vec<u8>> {
    #[cfg(test)]
    if let Some((_, legacy)) = TEST_REDEMPTION_KEYS.with(std::cell::Cell::get) {
        return Ok(legacy.to_vec());
    }
    general_purpose::STANDARD
        .decode(SHORT_KEY_SECRET_B64)
        .map_err(|_| "兑换 Key 密钥配置不正确。".to_string())
}

#[cfg(test)]
thread_local! {
    static TEST_REDEMPTION_KEYS: std::cell::Cell<Option<([u8; 32], [u8; 32])>> = const { std::cell::Cell::new(None) };
}

#[cfg(test)]
pub(crate) struct TestRedemptionKeysGuard {
    previous: Option<([u8; 32], [u8; 32])>,
    thread_bound: std::marker::PhantomData<std::rc::Rc<()>>,
}

#[cfg(test)]
pub(crate) fn use_test_redemption_keys(
    public: [u8; 32],
    legacy: [u8; 32],
) -> TestRedemptionKeysGuard {
    TestRedemptionKeysGuard {
        previous: TEST_REDEMPTION_KEYS.with(|keys| keys.replace(Some((public, legacy)))),
        thread_bound: std::marker::PhantomData,
    }
}

#[cfg(test)]
impl Drop for TestRedemptionKeysGuard {
    fn drop(&mut self) {
        TEST_REDEMPTION_KEYS.with(|keys| keys.set(self.previous));
    }
}

fn decode_short_payload(display_prefix: &str, bytes: &[u8]) -> LocalResult<RedeemKeyPayload> {
    if bytes.len() != SHORT_PAYLOAD_BYTES || bytes[0] != 3 {
        return Err("当前版本暂不支持该兑换 Key。".into());
    }

    let item_count = bytes[1] as usize;
    if item_count > 2 {
        return Err("兑换 Key 道具数量不正确。".into());
    }
    let nonce = bytes[2..8]
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    let expires_at_value = read_u16(bytes, 8);
    let expires_at = if expires_at_value == COMPACT_NO_EXPIRATION {
        None
    } else {
        Some(compact_date(expires_at_value)?)
    };
    let lp = read_u16(bytes, 10) as i64;
    let growth_value = read_u16(bytes, 12) as i64;
    let mut items = Vec::new();
    for index in 0..item_count {
        let offset = if index == 0 { 14 } else { 17 };
        let item_code = read_u16(bytes, offset);
        let quantity = bytes[offset + 2] as i64;
        if item_code == 0 || quantity <= 0 {
            return Err("兑换 Key 道具内容不正确。".into());
        }
        let item = find_compact_catalog_item(item_code)
            .ok_or_else(|| format!("兑换奖励中包含未知道具编码：{item_code}。"))?;
        items.push(PetRedeemKeyRewardItem {
            item_key: item.item_key,
            quantity,
        });
    }

    Ok(RedeemKeyPayload {
        version: 3,
        campaign_id: display_prefix.into(),
        nonce,
        issued_at: None,
        expires_at,
        rewards: PetRedeemKeyRewards {
            lp,
            growth_value,
            items,
        },
    })
}

fn decode_compact_payload(bytes: &[u8]) -> LocalResult<RedeemKeyPayload> {
    if bytes.len() != COMPACT_PAYLOAD_BYTES || bytes[0] != 2 {
        return Err("当前版本暂不支持该兑换 Key。".into());
    }

    let item_count = bytes[1] as usize;
    if item_count > 2 {
        return Err("兑换 Key 道具数量不正确。".into());
    }
    let campaign_hash = read_u32(bytes, 2);
    let nonce = bytes[6..14]
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    let issued_at = compact_date(read_u16(bytes, 14))?;
    let expires_at_value = read_u16(bytes, 16);
    let expires_at = if expires_at_value == COMPACT_NO_EXPIRATION {
        None
    } else {
        Some(compact_date(expires_at_value)?)
    };
    let lp = read_u16(bytes, 18) as i64;
    let growth_value = read_u16(bytes, 20) as i64;
    let mut items = Vec::new();
    for index in 0..item_count {
        let offset = if index == 0 { 22 } else { 25 };
        let item_code = read_u16(bytes, offset);
        let quantity = bytes[offset + 2] as i64;
        if item_code == 0 || quantity <= 0 {
            return Err("兑换 Key 道具内容不正确。".into());
        }
        let item = find_compact_catalog_item(item_code)
            .ok_or_else(|| format!("兑换奖励中包含未知道具编码：{item_code}。"))?;
        items.push(PetRedeemKeyRewardItem {
            item_key: item.item_key,
            quantity,
        });
    }

    Ok(RedeemKeyPayload {
        version: 2,
        campaign_id: format!("campaign-{campaign_hash:08x}"),
        nonce,
        issued_at: Some(issued_at),
        expires_at,
        rewards: PetRedeemKeyRewards {
            lp,
            growth_value,
            items,
        },
    })
}

fn compact_date(days_since_base: u16) -> LocalResult<String> {
    let base = Date::strptime("%Y-%m-%d", COMPACT_BASE_DATE)
        .map_err(|_| "兑换 Key 日期配置不正确。".to_string())?;
    base.checked_add(i64::from(days_since_base).days())
        .map(|date| date.to_string())
        .map_err(|_| "兑换 Key 日期不正确。".to_string())
}

fn find_compact_catalog_item(item_code: u16) -> Option<crate::local_db::PetStoreCatalogItem> {
    pet_store::catalog_items()
        .into_iter()
        .find(|item| compact_catalog_item_code(&item.item_key) == item_code)
}

fn compact_catalog_item_code(item_key: &str) -> u16 {
    let digest = Sha256::digest(item_key.as_bytes());
    u16::from_be_bytes([digest[0], digest[1]])
}

fn read_u16(bytes: &[u8], offset: usize) -> u16 {
    u16::from_be_bytes([bytes[offset], bytes[offset + 1]])
}

fn read_u32(bytes: &[u8], offset: usize) -> u32 {
    u32::from_be_bytes([
        bytes[offset],
        bytes[offset + 1],
        bytes[offset + 2],
        bytes[offset + 3],
    ])
}

fn decode_crockford_base32(value: &str) -> LocalResult<Vec<u8>> {
    let mut bytes = Vec::new();
    let mut buffer: u32 = 0;
    let mut bits: u8 = 0;

    for ch in value.chars() {
        let Some(next) = crockford_value(ch) else {
            return Err("兑换 Key 内容格式不正确。".into());
        };
        buffer = (buffer << 5) | next as u32;
        bits += 5;
        while bits >= 8 {
            bits -= 8;
            bytes.push(((buffer >> bits) & 0xff) as u8);
        }
    }

    Ok(bytes)
}

fn crockford_value(ch: char) -> Option<u8> {
    match ch.to_ascii_uppercase() {
        '0' | 'O' => Some(0),
        '1' | 'I' | 'L' => Some(1),
        '2' => Some(2),
        '3' => Some(3),
        '4' => Some(4),
        '5' => Some(5),
        '6' => Some(6),
        '7' => Some(7),
        '8' => Some(8),
        '9' => Some(9),
        'A' => Some(10),
        'B' => Some(11),
        'C' => Some(12),
        'D' => Some(13),
        'E' => Some(14),
        'F' => Some(15),
        'G' => Some(16),
        'H' => Some(17),
        'J' => Some(18),
        'K' => Some(19),
        'M' => Some(20),
        'N' => Some(21),
        'P' => Some(22),
        'Q' => Some(23),
        'R' => Some(24),
        'S' => Some(25),
        'T' => Some(26),
        'V' => Some(27),
        'W' => Some(28),
        'X' => Some(29),
        'Y' => Some(30),
        'Z' => Some(31),
        _ => None,
    }
}

fn is_short_display_prefix(value: &str) -> bool {
    !value.is_empty() && value.len() <= 8 && value.chars().all(|ch| ch.is_ascii_alphanumeric())
}

fn validate_payload(payload: &RedeemKeyPayload) -> LocalResult<()> {
    if !matches!(payload.version, 1..=3) {
        return Err("当前版本暂不支持该兑换 Key。".into());
    }
    if payload.campaign_id.trim().is_empty() {
        return Err("兑换 Key 缺少活动标识。".into());
    }
    if payload.nonce.trim().is_empty() {
        return Err("兑换 Key 缺少唯一标识。".into());
    }
    Ok(())
}

fn validate_expiration(payload: &RedeemKeyPayload) -> LocalResult<()> {
    let Some(expires_at) = payload.expires_at.as_deref() else {
        return Ok(());
    };
    let expires_at = Date::strptime("%Y-%m-%d", expires_at)
        .map_err(|_| "兑换 Key 过期时间格式不正确。".to_string())?;
    let now = crate::infrastructure::time::parse_iso(&crate::infrastructure::time::utc_now())?;
    if expires_at < now.to_zoned(TimeZone::UTC).date() {
        return Err("兑换 Key 已过期。".into());
    }
    Ok(())
}

fn validate_rewards(rewards: &PetRedeemKeyRewards) -> LocalResult<()> {
    if rewards.lp <= 0 && rewards.growth_value <= 0 && rewards.items.is_empty() {
        return Err("兑换 Key 没有可发放的奖励。".into());
    }
    for item in &rewards.items {
        let item_key = item.item_key.trim();
        if item_key.is_empty() {
            return Err("兑换奖励中存在空道具。".into());
        }
        if item.quantity <= 0 {
            return Err(format!("兑换奖励「{item_key}」数量必须大于 0。"));
        }
        if pet_store::find_catalog_item_by_key(item_key).is_none() {
            return Err(format!("兑换奖励中包含未知道具：{item_key}。"));
        }
    }
    Ok(())
}

fn insert_redemption_tx(
    tx: &Transaction<'_>,
    redemption: &PetRedeemKeyRedemption,
) -> LocalResult<()> {
    tx.execute(
        "INSERT INTO pet_redeem_key_redemptions (
            id, pet_id, key_hash, code_prefix, campaign_id, reward_json, status, redeemed_at, metadata
         ) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9)",
        params![
            redemption.id,
            redemption.pet_id,
            redemption.key_hash,
            redemption.code_prefix,
            redemption.campaign_id,
            redemption.reward_json,
            redemption.status,
            redemption.redeemed_at,
            redemption.metadata,
        ],
    )
    .map_err(|err| err.to_string())?;
    Ok(())
}

fn load_redemption_by_hash_tx(
    tx: &Transaction<'_>,
    key_hash: &str,
) -> LocalResult<Option<PetRedeemKeyRedemption>> {
    tx.query_row(
        "SELECT id, pet_id, key_hash, code_prefix, campaign_id, reward_json, status, redeemed_at, metadata
         FROM pet_redeem_key_redemptions
         WHERE pet_id = ?1 AND key_hash = ?2",
        params![PET_ID, key_hash],
        map_redemption,
    )
    .optional()
    .map_err(|err| err.to_string())
}

fn load_redemption_by_identity_tx(
    tx: &Transaction<'_>,
    campaign_id: &str,
    nonce: &str,
) -> LocalResult<Option<PetRedeemKeyRedemption>> {
    let mut statement = tx
        .prepare(
            "SELECT id, pet_id, key_hash, code_prefix, campaign_id, reward_json, status, redeemed_at, metadata
         FROM pet_redeem_key_redemptions
         WHERE pet_id = ?1 AND campaign_id = ?2 AND metadata LIKE ?3
         ORDER BY datetime(redeemed_at) DESC, redeemed_at DESC",
        )
        .map_err(|err| err.to_string())?;
    let mut rows = statement
        .query(params![PET_ID, campaign_id, "%\"nonce\":\"%"])
        .map_err(|err| err.to_string())?;
    while let Some(row) = rows.next().map_err(|err| err.to_string())? {
        let existing = map_redemption(row).map_err(|err| err.to_string())?;
        let matches_nonce = existing
            .metadata
            .as_deref()
            .and_then(|value| serde_json::from_str::<serde_json::Value>(value).ok())
            .and_then(|value| {
                value
                    .get("nonce")
                    .and_then(serde_json::Value::as_str)
                    .map(str::to_owned)
            })
            .is_some_and(|value| value == nonce);
        if matches_nonce {
            return Ok(Some(existing));
        }
    }
    Ok(None)
}

fn map_redemption(row: &rusqlite::Row<'_>) -> rusqlite::Result<PetRedeemKeyRedemption> {
    Ok(PetRedeemKeyRedemption {
        id: row.get(0)?,
        pet_id: row.get(1)?,
        key_hash: row.get(2)?,
        code_prefix: row.get(3)?,
        campaign_id: row.get(4)?,
        reward_json: row.get(5)?,
        status: row.get(6)?,
        redeemed_at: row.get(7)?,
        metadata: row.get(8)?,
    })
}

fn normalize_redeem_key(raw_key: &str) -> String {
    raw_key
        .chars()
        .filter(|value| !value.is_whitespace())
        .collect()
}

fn redeem_key_hash(key: &str) -> String {
    let digest = Sha256::digest(key.as_bytes());
    digest.iter().map(|byte| format!("{byte:02x}")).collect()
}

fn code_prefix(key: &str) -> String {
    let hash = redeem_key_hash(key);
    let prefix = key
        .split('.')
        .next()
        .unwrap_or(REDEEM_KEY_LEGACY_PREFIX)
        .split('-')
        .next()
        .unwrap_or(REDEEM_KEY_LEGACY_PREFIX);
    format!("{prefix}-{}", &hash[..8])
}

fn redeem_metadata(payload: &RedeemKeyPayload, key_hash: &str) -> String {
    json!({
        "version": payload.version,
        "campaignId": payload.campaign_id,
        "nonce": payload.nonce,
        "issuedAt": payload.issued_at,
        "expiresAt": payload.expires_at,
        "keyHash": key_hash,
    })
    .to_string()
}

#[cfg(test)]
mod tests {
    use super::*;
    use rusqlite::Connection;

    #[test]
    fn synthetic_signing_keys_are_scoped_nested_and_thread_local() {
        use ring::signature::KeyPair;
        let random = ring::rand::SystemRandom::new();
        let document = ring::signature::Ed25519KeyPair::generate_pkcs8(&random).unwrap();
        let pair = ring::signature::Ed25519KeyPair::from_pkcs8(document.as_ref()).unwrap();
        let public: [u8; 32] = pair.public_key().as_ref().try_into().unwrap();
        let signature = pair.sign(b"synthetic-redemption-message");
        assert!(verify_signature(b"synthetic-redemption-message", signature.as_ref()).is_err());
        {
            let _guard = use_test_redemption_keys(public, [7; 32]);
            assert!(verify_signature(b"synthetic-redemption-message", signature.as_ref()).is_ok());
            std::thread::scope(|scope| {
                scope
                    .spawn(|| {
                        assert!(verify_signature(
                            b"synthetic-redemption-message",
                            signature.as_ref()
                        )
                        .is_err())
                    })
                    .join()
                    .unwrap();
            });
            let restored = std::panic::catch_unwind(|| {
                let _nested = use_test_redemption_keys([0; 32], [8; 32]);
                assert!(
                    verify_signature(b"synthetic-redemption-message", signature.as_ref()).is_err()
                );
                panic!("synthetic guard unwind");
            });
            assert!(restored.is_err());
            assert!(verify_signature(b"synthetic-redemption-message", signature.as_ref()).is_ok());
        }
        assert!(verify_signature(b"synthetic-redemption-message", signature.as_ref()).is_err());
    }

    #[test]
    fn signed_unicode_parsing_distinguishes_known_strings_from_ignored_values() {
        use ring::signature::KeyPair;
        let random = ring::rand::SystemRandom::new();
        let document = ring::signature::Ed25519KeyPair::generate_pkcs8(&random).unwrap();
        let signer = ring::signature::Ed25519KeyPair::from_pkcs8(document.as_ref()).unwrap();
        let public: [u8; 32] = signer.public_key().as_ref().try_into().unwrap();
        let _keys = use_test_redemption_keys(public, [0; 32]);
        for payload in [
            r#"{"version":1,"campaignId":"\ud800","nonce":"a","rewards":{"lp":1}}"#,
            r#"{"version":1,"campaignId":"synthetic","nonce":"\udc00","rewards":{"lp":1}}"#,
            r#"{"version":1,"campaignId":"synthetic","nonce":"a","issuedAt":"\ud800","rewards":{"lp":1}}"#,
            r#"{"version":1,"campaignId":"synthetic","nonce":"a","expiresAt":"\udc00","rewards":{"lp":1}}"#,
            r#"{"version":1,"campaignId":"synthetic","nonce":"a","rewards":{"items":[{"itemKey":"\ud800"}]}}"#,
            r#"{"version":1,"campaignId":"synthetic","nonce":"a","rewards":{"lp":1},"\ud800":1}"#,
            r#"{"version":1,"campaignId":"synthetic","nonce":"a","rewards":{"lp":1,"\udc00":1}}"#,
            r#"{"version":1,"campaignId":"synthetic","nonce":"a","rewards":{"items":[{"itemKey":"wheat-seed","\ud800":1}]}}"#,
        ] {
            let encoded = general_purpose::URL_SAFE_NO_PAD.encode(payload);
            let code = format!(
                "LIB1.{encoded}.{}",
                general_purpose::URL_SAFE_NO_PAD.encode(signer.sign(encoded.as_bytes()))
            );
            assert_eq!(
                verify_redeem_key(&code).unwrap_err(),
                "兑换 Key 内容无法解析。"
            );
        }
        for (extra, expected) in [
            (r#""campaignId":"\ud83d\ude00""#, "😀"),
            (r#""campaignId":"�""#, "�"),
            (r#""campaignId":"\\ud800""#, r"\ud800"),
            (
                r#""campaignId":"synthetic","unknown":{"\ud800":["\udc00"]}"#,
                "synthetic",
            ),
        ] {
            let payload = format!(r#"{{"version":1,{extra},"nonce":"a","rewards":{{"lp":1}}}}"#);
            let encoded = general_purpose::URL_SAFE_NO_PAD.encode(payload);
            let code = format!(
                "LIB1.{encoded}.{}",
                general_purpose::URL_SAFE_NO_PAD.encode(signer.sign(encoded.as_bytes()))
            );
            let payload = verify_redeem_key(&code).unwrap();
            validate_payload(&payload).unwrap();
            validate_rewards(&payload.rewards).unwrap();
            assert_eq!(payload.campaign_id, expected);
        }
    }

    #[test]
    fn expiration_uses_scoped_utc_clock_and_includes_expiration_day() {
        let payload = RedeemKeyPayload {
            version: 1,
            campaign_id: "synthetic".into(),
            nonce: "synthetic".into(),
            issued_at: None,
            expires_at: Some("2026-11-18".into()),
            rewards: PetRedeemKeyRewards::default(),
        };
        for (now, expired) in [
            ("2026-11-18T23:59:59Z", false),
            ("2026-11-19T00:00:00Z", true),
            ("2026-11-18T23:30:00-08:00", true),
            ("2026-11-19T00:30:00+08:00", false),
        ] {
            let _clock = crate::infrastructure::time::freeze_test_clock(now).unwrap();
            let result = validate_expiration(&payload);
            assert_eq!(result.is_err(), expired);
            if expired {
                assert_eq!(result.unwrap_err(), "兑换 Key 已过期。");
            }
        }
    }

    #[test]
    fn normalize_redeem_key_removes_whitespace_only() {
        assert_eq!(normalize_redeem_key(" LIB1.abc\n.def "), "LIB1.abc.def");
    }

    #[test]
    fn redeem_key_hash_is_stable() {
        assert_eq!(redeem_key_hash("LIB1.test.sig").len(), 64);
        assert_eq!(
            redeem_key_hash("LIB1.test.sig"),
            redeem_key_hash("LIB1.test.sig")
        );
    }

    #[test]
    fn reissued_key_identity_matches_original_short_key_record() {
        let mut connection = Connection::open_in_memory().unwrap();
        connection
            .execute_batch(
                "CREATE TABLE pet_redeem_key_redemptions (
                    id TEXT, pet_id TEXT, key_hash TEXT, code_prefix TEXT,
                    campaign_id TEXT, reward_json TEXT, status TEXT,
                    redeemed_at TEXT, metadata TEXT
                );",
            )
            .unwrap();
        connection
            .execute(
                "INSERT INTO pet_redeem_key_redemptions VALUES
                 ('id', ?1, 'hash', 'campaign-hash', ?2, '{}', 'redeemed', '2026-11-19T00:00:00Z', ?3)",
                params![PET_ID, "campaign", r#"{"version":3,"campaignId":"campaign","nonce":"a1b2c3d4e5f6"}"#],
            )
            .unwrap();
        let tx = connection.transaction().unwrap();
        let found = load_redemption_by_identity_tx(&tx, "campaign", "a1b2c3d4e5f6")
            .unwrap()
            .expect("reissued signed key should match legacy redemption identity");
        assert_eq!(found.id, "id");
        assert!(load_redemption_by_identity_tx(&tx, "campaign", "different")
            .unwrap()
            .is_none());
    }

    #[test]
    fn experience_overflow_rolls_back_all_redemption_rewards() {
        use ring::signature::KeyPair;

        let random = ring::rand::SystemRandom::new();
        let document = ring::signature::Ed25519KeyPair::generate_pkcs8(&random).unwrap();
        let signer = ring::signature::Ed25519KeyPair::from_pkcs8(document.as_ref()).unwrap();
        let public: [u8; 32] = signer.public_key().as_ref().try_into().unwrap();
        let _keys = use_test_redemption_keys(public, [7; 32]);

        let payload = serde_json::json!({
            "version": 1,
            "campaignId": "synthetic-overflow",
            "nonce": "synthetic-overflow-nonce",
            "rewards": { "lp": 25, "growthValue": 2 }
        })
        .to_string();
        let encoded = general_purpose::URL_SAFE_NO_PAD.encode(payload);
        let key = format!(
            "LIB1.{encoded}.{}",
            general_purpose::URL_SAFE_NO_PAD.encode(signer.sign(encoded.as_bytes()))
        );

        let mut connection = Connection::open_in_memory().unwrap();
        crate::local_db::schema::apply_test_schema(&connection).unwrap();
        let transaction = connection.transaction().unwrap();
        pet::ensure_default_exists_tx(&transaction).unwrap();
        pet_store::ensure_store_defaults_tx(&transaction, "2026-11-20T12:00:00Z").unwrap();
        let mut profile = pet::load_profile_tx(&transaction).unwrap();
        profile.experience = i64::MAX - 1;
        pet::save_profile_tx(&transaction, &profile).unwrap();
        transaction.commit().unwrap();

        let result = {
            let transaction = connection.transaction().unwrap();
            let profile = pet::load_profile_tx(&transaction).unwrap();
            let result = redeem_key_tx(&transaction, profile, &key, "2026-11-20T12:00:00Z");
            drop(transaction);
            result
        };

        assert_eq!(result.unwrap_err(), "宠物经验值超出 SQLite INTEGER 范围。");
        assert_eq!(
            pet::load_profile(&connection).unwrap().experience,
            i64::MAX - 1
        );
        let wallet_balance: i64 = connection
            .query_row(
                "SELECT balance FROM pet_wallets WHERE pet_id = ?1 AND currency_key = 'lp'",
                [PET_ID],
                |row| row.get(0),
            )
            .unwrap();
        assert_eq!(wallet_balance, 0);
        for table in [
            "pet_redeem_key_redemptions",
            "pet_economy_ledger",
            "pet_event_ledger",
        ] {
            let count: i64 = connection
                .query_row(&format!("SELECT COUNT(*) FROM {table}"), [], |row| {
                    row.get(0)
                })
                .unwrap();
            assert_eq!(count, 0, "unexpected persisted rows in {table}");
        }
    }
}
