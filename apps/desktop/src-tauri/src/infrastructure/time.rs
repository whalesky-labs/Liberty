use jiff::{tz::Offset, SignedDuration, Timestamp};
use std::time::{SystemTime, UNIX_EPOCH};

pub fn utc_now() -> String {
    #[cfg(test)]
    if let Some(timestamp) = TEST_NOW.with(std::cell::Cell::get) {
        return format_utc(timestamp);
    }
    format_utc(Timestamp::now())
}

fn format_utc(timestamp: Timestamp) -> String {
    let nanos = timestamp.subsec_nanosecond();
    let precision = if nanos == 0 {
        0
    } else if nanos % 1_000_000 == 0 {
        3
    } else if nanos % 1_000 == 0 {
        6
    } else {
        9
    };
    format!(
        "{:.*}",
        precision,
        timestamp.display_with_offset(Offset::UTC)
    )
}

pub fn parse_iso(value: &str) -> Result<Timestamp, String> {
    let invalid = || "时间格式无效".to_string();
    let (clock, suffix) = value.split_at_checked(19).ok_or_else(invalid)?;
    let valid_clock = clock.is_ascii()
        && clock.as_bytes()[4] == b'-'
        && clock.as_bytes()[7] == b'-'
        && matches!(clock.as_bytes()[10], b'T' | b't' | b' ')
        && clock.as_bytes()[13] == b':'
        && clock.as_bytes()[16] == b':';
    if !valid_clock {
        return Err(invalid());
    }
    let (fraction, offset) = if let Some(digits) = suffix.strip_prefix('.') {
        let length = digits.bytes().take_while(u8::is_ascii_digit).count();
        if length == 0 {
            return Err(invalid());
        }
        (&suffix[..1 + length.min(9)], &digits[length..])
    } else {
        ("", suffix)
    };
    let numeric_offset = matches!(
        offset.as_bytes(),
        [
            b'+' | b'-',
            b'0'..=b'9',
            b'0'..=b'9',
            b':',
            b'0'..=b'9',
            b'0'..=b'9'
        ]
    );
    if !(offset.eq_ignore_ascii_case("Z") || numeric_offset) {
        return Err(invalid());
    }
    format!("{clock}{fraction}{offset}")
        .parse()
        .map_err(|_| invalid())
}

pub fn add_seconds(timestamp: Timestamp, seconds: i64) -> Result<String, String> {
    timestamp
        .checked_add(SignedDuration::from_secs(seconds))
        .map(format_utc)
        .map_err(|_| "时间超出范围。".into())
}

pub fn compare_iso(left: &str, right: &str) -> Result<i64, String> {
    Ok(parse_iso(left)?.duration_since(parse_iso(right)?).as_secs())
}

pub fn seconds_until(target: &str, now: &str) -> Result<i64, String> {
    Ok(compare_iso(target, now)?.max(0))
}

pub fn local_date_string() -> String {
    #[cfg(test)]
    if let Some(timestamp) = TEST_NOW.with(std::cell::Cell::get) {
        return timestamp
            .to_zoned(jiff::tz::TimeZone::system())
            .date()
            .to_string();
    }
    jiff::Zoned::now().date().to_string()
}

pub fn unix_timestamp_millis() -> u128 {
    #[cfg(test)]
    if let Some(timestamp) = TEST_NOW.with(std::cell::Cell::get) {
        return timestamp.as_millisecond().max(0) as u128;
    }
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_millis())
        .unwrap_or_default()
}

pub fn unix_timestamp_millis_string() -> String {
    unix_timestamp_millis().to_string()
}

#[cfg(test)]
thread_local! {
    static TEST_NOW: std::cell::Cell<Option<Timestamp>> = const { std::cell::Cell::new(None) };
}

#[cfg(test)]
pub(crate) struct TestClockGuard {
    previous: Option<Timestamp>,
    thread_bound: std::marker::PhantomData<std::rc::Rc<()>>,
}

#[cfg(test)]
pub(crate) fn freeze_test_clock(now: &str) -> Result<TestClockGuard, String> {
    let timestamp = parse_iso(now)?;
    Ok(TestClockGuard {
        previous: TEST_NOW.with(|clock| clock.replace(Some(timestamp))),
        thread_bound: std::marker::PhantomData,
    })
}

#[cfg(test)]
impl Drop for TestClockGuard {
    fn drop(&mut self) {
        TEST_NOW.with(|clock| clock.set(self.previous));
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn frozen_clock_nests_restores_after_unwind_and_stays_thread_local() {
        assert!(TEST_NOW.with(std::cell::Cell::get).is_none());
        let outer_now = "2026-07-01T23:59:59.123456+08:00";
        let outer_timestamp = parse_iso(outer_now).unwrap();
        let outer = freeze_test_clock(outer_now).unwrap();
        assert_eq!(utc_now(), "2026-07-01T15:59:59.123456+00:00");
        assert_eq!(
            unix_timestamp_millis(),
            outer_timestamp.as_millisecond() as u128
        );
        assert_eq!(
            local_date_string(),
            outer_timestamp
                .to_zoned(jiff::tz::TimeZone::system())
                .date()
                .to_string()
        );
        let failure = std::panic::catch_unwind(|| {
            let _inner = freeze_test_clock("2026-07-02T00:00:01+08:00").unwrap();
            assert_eq!(utc_now(), "2026-07-01T16:00:01+00:00");
            panic!("synthetic clock unwind");
        });
        assert!(failure.is_err());
        assert_eq!(utc_now(), "2026-07-01T15:59:59.123456+00:00");
        std::thread::spawn(|| {
            assert!(TEST_NOW.with(std::cell::Cell::get).is_none());
            let inner = freeze_test_clock("2030-01-01T00:00:00Z").unwrap();
            assert_eq!(utc_now(), "2030-01-01T00:00:00+00:00");
            drop(inner);
            assert!(TEST_NOW.with(std::cell::Cell::get).is_none());
        })
        .join()
        .unwrap();
        assert_eq!(utc_now(), "2026-07-01T15:59:59.123456+00:00");
        drop(outer);
        assert!(TEST_NOW.with(std::cell::Cell::get).is_none());
        assert!(freeze_test_clock("invalid").is_err());
        assert!(TEST_NOW.with(std::cell::Cell::get).is_none());
    }

    #[test]
    fn preserves_existing_utc_text_and_precision() {
        for (input, expected) in [
            ("2000-01-01T08:00:00+08:00", "2000-01-01T00:00:00+00:00"),
            ("1969-12-31t23:59:59.1z", "1969-12-31T23:59:59.100+00:00"),
            (
                "2024-02-29 00:00:00.1234Z",
                "2024-02-29T00:00:00.123400+00:00",
            ),
            (
                "2024-02-29T00:00:00.1234567899Z",
                "2024-02-29T00:00:00.123456789+00:00",
            ),
        ] {
            assert_eq!(format_utc(parse_iso(input).unwrap()), expected);
        }
        for invalid in [
            "2024-02-30T00:00:00Z",
            "2024-01-01T00:00:00",
            "2024-01-01T00:00:00+0800",
            "2024-01-01T00:00:00Z[UTC]",
            "2024-01-01T00:00:00.Z",
        ] {
            assert!(parse_iso(invalid).is_err(), "{invalid}");
        }
    }

    #[test]
    fn workflow_seconds_truncate_toward_zero_across_midnight() {
        let start = "2024-02-29T23:59:59.100Z";
        let end = "2024-03-01T00:00:01.050Z";
        assert_eq!(compare_iso(end, start), Ok(1));
        assert_eq!(compare_iso(start, end), Ok(-1));
        assert_eq!(seconds_until(start, end), Ok(0));
        assert_eq!(
            add_seconds(parse_iso(start).unwrap(), 60).unwrap(),
            "2024-03-01T00:00:59.100+00:00"
        );
    }
}
