use serde::{de, Deserialize, Deserializer};
use std::{collections::HashSet, fmt, marker::PhantomData, sync::LazyLock};

use super::model::{PetLevelSnapshot, PetStageThreshold};

pub const MAX_PET_LEVEL: i64 = 255;

const STAGES_JSON: &str = include_str!("../../../../../packages/desktop-resources/pet/stages.json");
static STAGE_THRESHOLDS: LazyLock<Vec<StageSeed>> =
    LazyLock::new(|| parse_stages(STAGES_JSON).expect("invalid embedded pet stages.json"));

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct StageSeed {
    stage: String,
    label_zh: String,
    label_en: String,
    level: i64,
}

#[derive(Deserialize)]
#[serde(transparent, bound(deserialize = "T: Deserialize<'de>"))]
pub(crate) struct SeedRecord<T>(#[serde(deserialize_with = "deserialize_seed_object")] pub T);

fn deserialize_seed_object<'de, T: Deserialize<'de>, D: Deserializer<'de>>(
    deserializer: D,
) -> Result<T, D::Error> {
    struct ObjectVisitor<T>(PhantomData<T>);

    impl<'de, T: Deserialize<'de>> de::Visitor<'de> for ObjectVisitor<T> {
        type Value = T;

        fn expecting(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
            formatter.write_str("a seed object with named fields")
        }

        fn visit_map<A: de::MapAccess<'de>>(self, map: A) -> Result<T, A::Error> {
            T::deserialize(de::value::MapAccessDeserializer::new(map))
        }
    }

    deserializer.deserialize_map(ObjectVisitor(PhantomData))
}

fn parse_stages(source: &str) -> Result<Vec<StageSeed>, String> {
    let stages: Vec<SeedRecord<StageSeed>> =
        serde_json::from_str(source).map_err(|err| err.to_string())?;
    let stages: Vec<_> = stages.into_iter().map(|seed| seed.0).collect();
    if stages.len() != 8 || stages[0].level != 1 {
        return Err("expected eight stages starting at level 1".into());
    }

    let mut keys = HashSet::new();
    let mut previous = 0;
    for seed in &stages {
        if matches!(seed.stage.as_str(), "baby" | "growing" | "mature")
            || !seed.stage.split('_').all(|part| {
                !part.is_empty()
                    && part
                        .bytes()
                        .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit())
            })
            || !keys.insert(seed.stage.as_str())
            || seed.label_zh.trim().is_empty()
            || seed.label_en.trim().is_empty()
            || seed.level <= previous
            || seed.level > MAX_PET_LEVEL
        {
            return Err(format!("invalid or duplicate stage: {}", seed.stage));
        }
        previous = seed.level;
    }
    Ok(stages)
}

pub(crate) fn required_exp_for_level(level: i64) -> i64 {
    let level = level.clamp(1, MAX_PET_LEVEL);
    if level >= MAX_PET_LEVEL {
        return 0;
    }

    match level {
        1..=10 => lerp_required(15, 30, level, 1, 10),
        11..=30 => lerp_required(35, 60, level, 11, 30),
        31..=60 => lerp_required(70, 110, level, 31, 60),
        61..=100 => lerp_required(120, 180, level, 61, 100),
        101..=140 => lerp_required(200, 280, level, 101, 140),
        141..=180 => lerp_required(300, 400, level, 141, 180),
        181..=220 => lerp_required(430, 560, level, 181, 220),
        _ => lerp_required(600, 800, level, 221, 254),
    }
}

pub(crate) fn level_snapshot_from_experience(experience: i64) -> PetLevelSnapshot {
    let total_experience = experience.max(0);
    let mut remaining = total_experience;
    let mut level = 1;

    while level < MAX_PET_LEVEL {
        let required = required_exp_for_level(level);
        if remaining < required {
            break;
        }
        remaining -= required;
        level += 1;
    }

    let is_max_level = level >= MAX_PET_LEVEL;
    let next_level_required = if is_max_level {
        0
    } else {
        required_exp_for_level(level)
    };
    let current_level_exp = if is_max_level {
        remaining.max(0)
    } else {
        remaining.clamp(0, next_level_required)
    };
    let progress_ratio = if is_max_level || next_level_required <= 0 {
        1.0
    } else {
        (current_level_exp as f64 / next_level_required as f64).clamp(0.0, 1.0)
    };
    let current_stage = stage_info_from_level(level);
    let next_stage = next_stage_from_level(level);

    PetLevelSnapshot {
        level,
        current_level_exp,
        next_level_required,
        total_experience,
        current_stage: current_stage.stage.clone(),
        current_stage_label_zh: current_stage.label_zh.clone(),
        current_stage_label_en: current_stage.label_en.clone(),
        next_stage: next_stage.as_ref().map(|stage| stage.stage.clone()),
        next_stage_level: next_stage.as_ref().map(|stage| stage.level),
        progress_ratio,
        is_max_level,
    }
}

pub(crate) fn total_required_exp_for_level(level: i64) -> i64 {
    let target_level = level.clamp(1, MAX_PET_LEVEL);
    (1..target_level).map(required_exp_for_level).sum()
}

pub(crate) fn stage_from_level(level: i64) -> &'static str {
    &stage_info_from_level(level).stage
}

pub(crate) fn stage_label_zh(stage: &str) -> &'static str {
    &stage_info_from_stage(stage).label_zh
}

pub(crate) fn stage_label_en(stage: &str) -> &'static str {
    &stage_info_from_stage(stage).label_en
}

pub(crate) fn stage_rank(stage: &str) -> i64 {
    STAGE_THRESHOLDS
        .iter()
        .position(|seed| seed.stage == normalize_stage_key(stage))
        .map(|index| index as i64 + 1)
        .unwrap_or(1)
}

pub(crate) fn normalize_stage(stage: &str, level: i64) -> String {
    let normalized = normalize_stage_key(stage);
    if is_known_stage(normalized) {
        return normalized.into();
    }
    stage_from_level(level).into()
}

pub(crate) fn growth_reward_multiplier(level: i64) -> f64 {
    match level.clamp(1, MAX_PET_LEVEL) {
        1..=30 => 1.0,
        31..=60 => 1.15,
        61..=100 => 1.3,
        101..=140 => 1.45,
        141..=180 => 1.6,
        181..=220 => 1.75,
        _ => 2.0,
    }
}

pub(crate) fn lp_reward_multiplier(level: i64) -> f64 {
    match level.clamp(1, MAX_PET_LEVEL) {
        1..=60 => 1.0,
        61..=140 => 1.15,
        141..=220 => 1.3,
        _ => 1.45,
    }
}

fn next_stage_from_level(level: i64) -> Option<PetStageThreshold> {
    STAGE_THRESHOLDS
        .iter()
        .find(|seed| seed.level > level)
        .map(|seed| PetStageThreshold {
            stage: seed.stage.clone(),
            label_zh: seed.label_zh.clone(),
            label_en: seed.label_en.clone(),
            level: seed.level,
        })
}

fn stage_info_from_level(level: i64) -> &'static StageSeed {
    let level = level.clamp(1, MAX_PET_LEVEL);
    STAGE_THRESHOLDS
        .iter()
        .rev()
        .find(|seed| level >= seed.level)
        .unwrap_or(&STAGE_THRESHOLDS[0])
}

fn stage_info_from_stage(stage: &str) -> &'static StageSeed {
    let normalized = normalize_stage_key(stage);
    STAGE_THRESHOLDS
        .iter()
        .find(|seed| seed.stage == normalized)
        .unwrap_or(&STAGE_THRESHOLDS[0])
}

fn normalize_stage_key(stage: &str) -> &str {
    match stage {
        "baby" => "first_meet",
        "growing" => "grow_together",
        "mature" => "deep_bond",
        value => value,
    }
}

pub(crate) fn is_known_stage(stage: &str) -> bool {
    STAGE_THRESHOLDS.iter().any(|seed| seed.stage == stage)
}

fn lerp_required(min: i64, max: i64, level: i64, start: i64, end: i64) -> i64 {
    if start == end {
        return min;
    }
    let ratio = (level - start) as f64 / (end - start) as f64;
    (min as f64 + (max - min) as f64 * ratio).round() as i64
}

#[cfg(test)]
mod tests {
    use super::*;

    fn exp_to_reach_level(level: i64) -> i64 {
        total_required_exp_for_level(level)
    }

    #[test]
    fn rejects_invalid_shared_stage_records() {
        for (original, replacement) in [
            ("\"level\": 11", "\"level\": 1"),
            ("\"level\": 11", "\"level\": 11.0"),
            ("\"level\": 11", "\"level\": 11, \"level\": 11"),
            ("\"level\": 11", "\"threshold\": 11"),
            ("\"familiar\"", "\"first_meet\""),
        ] {
            let invalid = STAGES_JSON.replacen(original, replacement, 1);
            assert_ne!(invalid, STAGES_JSON);
            assert!(parse_stages(&invalid).is_err(), "accepted {replacement}");
        }
        assert!(serde_json::from_str::<SeedRecord<StageSeed>>(
            r#"["first_meet","初遇","First Encounter",1]"#
        )
        .is_err());
    }

    #[test]
    fn calculates_expected_level_boundaries() {
        let level_two_exp = required_exp_for_level(1);
        assert_eq!(level_snapshot_from_experience(0).level, 1);
        assert_eq!(level_snapshot_from_experience(level_two_exp - 1).level, 1);
        assert_eq!(level_snapshot_from_experience(level_two_exp).level, 2);
        assert_eq!(
            level_snapshot_from_experience(exp_to_reach_level(11)).level,
            11
        );
        assert_eq!(
            level_snapshot_from_experience(exp_to_reach_level(61)).level,
            61
        );
        assert_eq!(
            level_snapshot_from_experience(exp_to_reach_level(141)).level,
            141
        );
        assert_eq!(
            level_snapshot_from_experience(exp_to_reach_level(221)).level,
            221
        );
        assert_eq!(
            level_snapshot_from_experience(exp_to_reach_level(255)).level,
            255
        );
    }

    #[test]
    fn caps_level_but_keeps_total_experience() {
        let max_level_exp = exp_to_reach_level(255);
        let snapshot = level_snapshot_from_experience(max_level_exp + 99_999);
        assert_eq!(snapshot.level, 255);
        assert!(snapshot.is_max_level);
        assert_eq!(snapshot.total_experience, max_level_exp + 99_999);
        assert_eq!(snapshot.next_level_required, 0);
    }

    #[test]
    fn keeps_late_requirements_higher_than_early_requirements() {
        assert!(required_exp_for_level(1) < required_exp_for_level(181));
        assert!(required_exp_for_level(10) < required_exp_for_level(254));
        assert!(required_exp_for_level(220) < required_exp_for_level(254));
    }

    #[test]
    fn maps_all_stage_thresholds() {
        assert_eq!(stage_from_level(1), "first_meet");
        assert_eq!(stage_from_level(11), "familiar");
        assert_eq!(stage_from_level(31), "steady_companion");
        assert_eq!(stage_from_level(61), "grow_together");
        assert_eq!(stage_from_level(101), "tacit_bond");
        assert_eq!(stage_from_level(141), "deep_bond");
        assert_eq!(stage_from_level(181), "long_company");
        assert_eq!(stage_from_level(221), "bond_forever");
    }

    #[test]
    fn normalizes_legacy_stages() {
        assert_eq!(normalize_stage("baby", 1), "first_meet");
        assert_eq!(normalize_stage("growing", 20), "grow_together");
        assert_eq!(normalize_stage("mature", 80), "deep_bond");
        assert_eq!(normalize_stage("unknown", 101), "tacit_bond");
    }

    #[test]
    fn keeps_lp_multiplier_below_growth_multiplier_after_mid_game() {
        for level in [1, 61, 141, 181, 221, 255] {
            assert!(lp_reward_multiplier(level) <= growth_reward_multiplier(level));
        }
    }
}
