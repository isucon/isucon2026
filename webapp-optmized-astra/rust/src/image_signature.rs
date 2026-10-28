use std::collections::{HashMap, VecDeque};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, LazyLock, Mutex, OnceLock};

use anyhow::anyhow;
use base64::Engine as _;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use chrono::{TimeDelta, Utc};
use hmac::{Hmac, Mac};
use percent_encoding::{AsciiSet, NON_ALPHANUMERIC, utf8_percent_encode};
use sha2::Sha256;

use crate::api_error::Error;
use crate::user::User;

pub const IMAGE_URL_LIFETIME: TimeDelta = TimeDelta::hours(24);
pub const IMAGE_SIGNATURE_ITERATIONS: usize = 1000;

const PATH_SEGMENT: &AsciiSet = &NON_ALPHANUMERIC
    .remove(b'-')
    .remove(b'_')
    .remove(b'.')
    .remove(b'~')
    .remove(b'$')
    .remove(b'&')
    .remove(b'+')
    .remove(b':')
    .remove(b'=')
    .remove(b'@');

pub fn image_signing_key(secret_key_hash: &str) -> Result<Vec<u8>, Error> {
    match hex::decode(secret_key_hash) {
        Ok(key) if key.len() == 32 => Ok(key),
        _ => Err(anyhow!("invalid image signing key").into()),
    }
}

#[derive(Clone, Hash, Eq, PartialEq)]
enum MemoBytes<const N: usize> {
    Inline([u8; N]),
    Other(Vec<u8>),
}
impl<const N: usize> MemoBytes<N> {
    fn new(bytes: &[u8]) -> Self {
        match bytes.try_into() {
            Ok(bytes) => Self::Inline(bytes),
            Err(_) => Self::Other(bytes.to_vec()),
        }
    }
}

#[derive(Clone, Hash, Eq, PartialEq)]
struct SignatureKey {
    key: MemoBytes<32>,
    image_id: MemoBytes<36>,
    exp: i64,
}

#[derive(Default)]
struct SignatureCache {
    values: HashMap<SignatureKey, Arc<OnceLock<[u8; 32]>>>,
    order: VecDeque<SignatureKey>,
}

#[derive(Default)]
struct SignatureStats {
    hits: AtomicU64,
    misses: AtomicU64,
    pending: AtomicU64,
    computations: AtomicU64,
    evictions: AtomicU64,
}

struct SignatureMemo {
    shards: [Mutex<SignatureCache>; 16],
    capacity: usize,
    coalesce: bool,
    stats: SignatureStats,
}

impl SignatureMemo {
    fn new(capacity: usize, coalesce: bool) -> Self {
        assert!(capacity > 0);
        Self {
            shards: std::array::from_fn(|_| Mutex::default()),
            capacity,
            coalesce,
            stats: SignatureStats::default(),
        }
    }

    fn get_or_compute(&self, lookup: SignatureKey, compute: impl FnOnce() -> [u8; 32]) -> [u8; 32] {
        use std::hash::{Hash, Hasher};
        let mut hasher = std::collections::hash_map::DefaultHasher::new();
        lookup.hash(&mut hasher);
        let shard = &self.shards[hasher.finish() as usize % self.shards.len()];
        let cell = {
            let mut cache = shard.lock().unwrap();
            if let Some(cell) = cache.values.get(&lookup) {
                if let Some(value) = cell.get() {
                    self.stats.hits.fetch_add(1, Ordering::Relaxed);
                    return *value;
                }
                self.stats.pending.fetch_add(1, Ordering::Relaxed);
                cell.clone()
            } else {
                self.stats.misses.fetch_add(1, Ordering::Relaxed);
                if cache.values.len() >= self.capacity
                    && let Some(old) = cache.order.pop_front()
                {
                    cache.values.remove(&old);
                    self.stats.evictions.fetch_add(1, Ordering::Relaxed);
                }
                let cell = Arc::new(OnceLock::new());
                cache.order.push_back(lookup.clone());
                cache.values.insert(lookup, cell.clone());
                cell
            }
        };
        let calculate = || {
            self.stats.computations.fetch_add(1, Ordering::Relaxed);
            compute()
        };
        if self.coalesce {
            // Wait only on this signature, without holding a shard lock.
            // An evicted cell stays alive for readers already using it.
            *cell.get_or_init(calculate)
        } else {
            let value = calculate();
            let _ = cell.set(value);
            value
        }
    }
}

fn enabled(name: &str, default: &str) -> bool {
    let value = std::env::var(name).unwrap_or_else(|_| default.into());
    assert!(matches!(value.as_str(), "0" | "1"), "invalid {name}");
    value == "1"
}

static SIGNATURE_MEMO: LazyLock<SignatureMemo> =
    LazyLock::new(|| SignatureMemo::new(16384, enabled("ISUREAL_SIGNATURE_COALESCE", "1")));

pub(crate) fn initialize_signature_cache() {
    LazyLock::force(&SIGNATURE_MEMO);
    static METRICS_STARTED: OnceLock<()> = OnceLock::new();
    if enabled("ISUREAL_SIGNATURE_METRICS", "0") && METRICS_STARTED.set(()).is_ok() {
        tokio::spawn(async {
            loop {
                tokio::time::sleep(std::time::Duration::from_secs(10)).await;
                let stats = &SIGNATURE_MEMO.stats;
                // Only aggregate counters, never cache keys, images or tokens.
                use std::io::Write;
                let _ = writeln!(
                    std::io::stdout().lock(),
                    "{{\"level\":\"info\",\"metric\":\"signature_cache\",\"hits\":{},\"misses\":{},\"pending\":{},\"computations\":{},\"evictions\":{}}}",
                    stats.hits.load(Ordering::Relaxed),
                    stats.misses.load(Ordering::Relaxed),
                    stats.pending.load(Ordering::Relaxed),
                    stats.computations.load(Ordering::Relaxed),
                    stats.evictions.load(Ordering::Relaxed)
                );
            }
        });
    }
}

pub fn image_signature(key: &[u8], image_id: &str, exp: i64) -> [u8; 32] {
    // Memoize only the pure function; current credentials, visibility and
    // expiration remain checked by callers on every request.
    SIGNATURE_MEMO.get_or_compute(
        SignatureKey {
            key: MemoBytes::new(key),
            image_id: MemoBytes::new(image_id.as_bytes()),
            exp,
        },
        || calculate_signature(key, image_id, exp),
    )
}

fn calculate_signature(key: &[u8], image_id: &str, exp: i64) -> [u8; 32] {
    let base = Hmac::<Sha256>::new_from_slice(key).expect("HMAC accepts keys of any size");
    let mut mac = base.clone();
    mac.update(format!("{exp}:{image_id}").as_bytes());
    let mut value: [u8; 32] = mac.finalize().into_bytes().into();
    for _ in 1..IMAGE_SIGNATURE_ITERATIONS {
        let mut mac = base.clone();
        mac.update(&value);
        value = mac.finalize().into_bytes().into();
    }
    value
}

pub fn signed_image_url(user: &User, image_id: &str) -> String {
    let exp = (Utc::now() + IMAGE_URL_LIFETIME).timestamp();
    format!(
        "/api/images/{}?exp={}&sig={}",
        utf8_percent_encode(image_id, PATH_SEGMENT),
        exp,
        URL_SAFE_NO_PAD.encode(image_signature(&user.image_key, image_id, exp))
    )
}

#[cfg(test)]
mod cache_tests {
    use super::*;

    #[test]
    fn simultaneous_requests_share_one_calculation() {
        let memo = SignatureMemo::new(2, true);
        let key = SignatureKey {
            key: MemoBytes::new(&[1; 32]),
            image_id: MemoBytes::new(b"image"),
            exp: 1,
        };
        let start = std::sync::Barrier::new(16);
        std::thread::scope(|scope| {
            let threads: Vec<_> = (0..16)
                .map(|_| {
                    let key = key.clone();
                    let memo = &memo;
                    let start = &start;
                    scope.spawn(move || {
                        start.wait();
                        memo.get_or_compute(key, || {
                            std::thread::sleep(std::time::Duration::from_millis(30));
                            [7; 32]
                        })
                    })
                })
                .collect();
            for thread in threads {
                assert_eq!(thread.join().unwrap(), [7; 32]);
            }
        });
        assert_eq!(memo.stats.computations.load(Ordering::Relaxed), 1);
        assert_eq!(memo.stats.misses.load(Ordering::Relaxed), 1);
        assert_eq!(memo.get_or_compute(key, || panic!("cached")), [7; 32]);
        for exp in 2..100 {
            let key = SignatureKey {
                key: MemoBytes::new(&[1; 32]),
                image_id: MemoBytes::new(b"image"),
                exp,
            };
            assert_eq!(
                memo.get_or_compute(key, || [exp as u8; 32]),
                [exp as u8; 32]
            );
        }
        for shard in &memo.shards {
            let shard = shard.lock().unwrap();
            assert!(shard.values.len() <= 2);
            assert_eq!(shard.values.len(), shard.order.len());
        }
        assert!(memo.stats.evictions.load(Ordering::Relaxed) > 0);
    }
}
