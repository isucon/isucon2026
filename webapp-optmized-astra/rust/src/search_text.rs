//! Pure search-text matching. Only Unicode transformations are cached: account
//! state, names read from the database, and visibility remain request-local.
use std::collections::{HashMap, VecDeque};
use std::hash::{Hash, Hasher};
use std::sync::{Arc, LazyLock, Mutex};

use unicode_normalization::UnicodeNormalization;

const SHARDS: usize = 16;
const CAPACITY: usize = 1024;
// API names are <= 100 Unicode scalars. Do not cache oversized legacy values.
const MAX_BYTES: usize = 512;

#[derive(Default)]
struct Cache {
    values: HashMap<Arc<str>, Arc<str>>,
    order: VecDeque<Arc<str>>,
}
static CACHES: LazyLock<[Mutex<Cache>; SHARDS]> =
    LazyLock::new(|| std::array::from_fn(|_| Mutex::default()));

pub fn normalize(value: &str) -> String {
    if value.is_ascii() {
        value.to_ascii_lowercase()
    } else {
        caseless::default_case_fold_str(&value.nfc().collect::<String>())
            .nfc()
            .collect()
    }
}

pub fn matches(value: &str, normalized_query: &str) -> bool {
    if value.is_ascii() {
        // Usernames normally already contain only lowercase ASCII, so neither
        // a Unicode pass nor an allocation is necessary for them.
        return if value.bytes().any(|byte| byte.is_ascii_uppercase()) {
            value.to_ascii_lowercase().contains(normalized_query)
        } else {
            value.contains(normalized_query)
        };
    }
    if value.len() > MAX_BYTES {
        return normalize(value).contains(normalized_query);
    }
    let mut hash = std::collections::hash_map::DefaultHasher::new();
    value.hash(&mut hash);
    let cache = &CACHES[hash.finish() as usize % SHARDS];
    {
        let cache = cache.lock().unwrap();
        if let Some(normalized) = cache.values.get(value) {
            return normalized.contains(normalized_query);
        }
    }
    let normalized: Arc<str> = normalize(value).into();
    let found = normalized.contains(normalized_query);
    let mut cache = cache.lock().unwrap();
    if !cache.values.contains_key(value) {
        if cache.values.len() >= CAPACITY
            && let Some(oldest) = cache.order.pop_front()
        {
            cache.values.remove(&oldest);
        }
        let key: Arc<str> = value.into();
        cache.order.push_back(key.clone());
        cache.values.insert(key, normalized);
    }
    found
}

#[cfg(test)]
mod tests {
    use super::*;

    fn reference(value: &str) -> String {
        caseless::default_case_fold_str(&value.nfc().collect::<String>())
            .nfc()
            .collect()
    }

    #[test]
    fn search_matches_preserve_unicode_and_literal_semantics() {
        let names = [
            "",
            "ASCII.MiXeD_01",
            "already_lowercase",
            "Café Straße",
            "Cafe\u{301} STRASSE",
            "Σσς",
            "İIıi",
            "KＫk",
            "ﬃffi",
            "日本語かなカナ",
            "가가",
            "a\u{301}\u{323}",
            "a\u{323}\u{301}",
            "%_\\",
            "😀😃",
            "\0\n\r\t",
        ];
        let queries = [
            "",
            "A",
            "CAFE\u{301}",
            "STRASSE",
            "σ",
            "i",
            "i\u{307}",
            "k",
            "ｋ",
            "ffi",
            "日本",
            "가",
            "á\u{323}",
            "%",
            "_",
            "\\",
            "😀",
            "\0",
        ];
        for name in names {
            assert_eq!(normalize(name), reference(name));
            for query in queries {
                let query = reference(query);
                let expected = reference(name).contains(&query);
                // Check cold and cached results. Full raw text is the key, so
                // alternate spellings and edits cannot reuse a stale name.
                for _ in 0..2 {
                    assert_eq!(
                        matches(name, &query),
                        expected,
                        "name={name:?}, q={query:?}"
                    );
                }
            }
        }
        for byte in 0..=127u8 {
            let text = (byte as char).to_string();
            assert_eq!(normalize(&text), reference(&text));
        }
        let long = "é".repeat(MAX_BYTES);
        assert!(matches(&long, "éé"));
        assert!(!matches(&long, "ee"));
    }

    #[test]
    #[ignore = "local CPU comparison, run with --release --ignored --nocapture"]
    fn search_text_cpu_comparison() {
        use std::hint::black_box;
        use std::time::Instant;
        let names: Vec<String> = (0..2000)
            .flat_map(|i| [format!("user_{i}"), format!("利用者{i} Café Straße")])
            .collect();
        let start = Instant::now();
        for _ in 0..100 {
            for name in &names {
                black_box(reference(black_box(name)).contains(black_box("strasse")));
            }
        }
        let baseline = start.elapsed();
        let start = Instant::now();
        for _ in 0..100 {
            for name in &names {
                black_box(matches(black_box(name), black_box("strasse")));
            }
        }
        eprintln!(
            "normalization baseline={baseline:?}, cached={:?}",
            start.elapsed()
        );
    }
}
