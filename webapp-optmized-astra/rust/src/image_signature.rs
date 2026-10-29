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
use subtle::ConstantTimeEq;

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
    url_hits: AtomicU64,
    url_misses: AtomicU64,
    url_evictions: AtomicU64,
    file_hits: AtomicU64,
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
    LazyLock::force(&crate::signature_file::SIGNATURE_FILE);
    LazyLock::force(&SIGNATURE_MEMO);
    LazyLock::force(&ISSUED_URLS_ENABLED);
    LazyLock::force(&ISSUED_URL_CAPACITY);
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
                    "{{\"level\":\"info\",\"metric\":\"signature_cache\",\"hits\":{},\"misses\":{},\"pending\":{},\"computations\":{},\"evictions\":{},\"url_hits\":{},\"url_misses\":{},\"url_evictions\":{},\"file_hits\":{}}}",
                    stats.hits.load(Ordering::Relaxed),
                    stats.misses.load(Ordering::Relaxed),
                    stats.pending.load(Ordering::Relaxed),
                    stats.computations.load(Ordering::Relaxed),
                    stats.evictions.load(Ordering::Relaxed),
                    stats.url_hits.load(Ordering::Relaxed),
                    stats.url_misses.load(Ordering::Relaxed),
                    stats.url_evictions.load(Ordering::Relaxed),
                    stats.file_hits.load(Ordering::Relaxed)
                );
            }
        });
    }
}

pub fn image_signature(key: &[u8], image_id: &str, exp: i64) -> [u8; 32] {
    if let Some(file) = &*crate::signature_file::SIGNATURE_FILE
        && let Some((expires, signature)) = file.lookup(key, image_id)
        && expires == exp
    {
        SIGNATURE_MEMO
            .stats
            .file_hits
            .fetch_add(1, Ordering::Relaxed);
        return signature;
    }
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

/// Reuse an issued signature only for its exact key, image and expiry.
/// The HTTP handler still checks expiration and current SQL authorization.
pub fn verify_image_signature(key: &[u8], image_id: &str, exp: i64, signature: &[u8]) -> bool {
    let cached = if *ISSUED_URLS_ENABLED {
        let lookup = issued_url_key(key, image_id);
        issued_url_shard(&lookup)
            .lock()
            .unwrap()
            .values
            .get(&lookup)
            .filter(|entry| entry.expires == exp)
            .map(|entry| entry.signature)
    } else {
        None
    };
    let expected = cached.unwrap_or_else(|| image_signature(key, image_id, exp));
    bool::from(signature.ct_eq(&expected))
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

struct IssuedUrl {
    expires: i64,
    signature: [u8; 32],
    url: String,
}

#[derive(Default)]
struct IssuedUrlCache {
    values: HashMap<SignatureKey, IssuedUrl>,
    order: VecDeque<SignatureKey>,
}

impl IssuedUrlCache {
    fn get_or_issue(
        &mut self,
        key: SignatureKey,
        now: i64,
        capacity: usize,
        issue: impl FnOnce(i64) -> IssuedUrl,
    ) -> String {
        if let Some(entry) = self.values.get(&key)
            && entry.expires > now + 60
        {
            SIGNATURE_MEMO
                .stats
                .url_hits
                .fetch_add(1, Ordering::Relaxed);
            return entry.url.clone();
        }
        SIGNATURE_MEMO
            .stats
            .url_misses
            .fetch_add(1, Ordering::Relaxed);
        // Renew near expiry. Reusing a URL never extends its original lifetime.
        if self.values.remove(&key).is_some() {
            self.order.retain(|entry| entry != &key);
        }
        if self.values.len() >= capacity
            && let Some(old) = self.order.pop_front()
        {
            self.values.remove(&old);
            SIGNATURE_MEMO
                .stats
                .url_evictions
                .fetch_add(1, Ordering::Relaxed);
        }
        let expires = now + IMAGE_URL_LIFETIME.num_seconds();
        let entry = issue(expires);
        let url = entry.url.clone();
        self.order.push_back(key.clone());
        self.values.insert(key, entry);
        url
    }
}

static ISSUED_URLS: LazyLock<[Mutex<IssuedUrlCache>; 16]> =
    LazyLock::new(|| std::array::from_fn(|_| Mutex::default()));
static ISSUED_URLS_ENABLED: LazyLock<bool> =
    LazyLock::new(|| enabled("ISUREAL_ISSUED_URL_CACHE", "1"));

static ISSUED_URL_CAPACITY: LazyLock<usize> = LazyLock::new(|| {
    let entries: usize = std::env::var("ISUREAL_ISSUED_URL_CACHE_ENTRIES")
        .unwrap_or_else(|_| "65536".into())
        .parse()
        .expect("issued URL cache capacity");
    assert!((16..=262144).contains(&entries) && entries.is_multiple_of(16));
    entries / 16
});

fn issued_url_key(key: &[u8], image_id: &str) -> SignatureKey {
    SignatureKey {
        key: MemoBytes::new(key),
        image_id: MemoBytes::new(image_id.as_bytes()),
        exp: 0,
    }
}

fn issued_url_shard(lookup: &SignatureKey) -> &'static Mutex<IssuedUrlCache> {
    use std::hash::{Hash, Hasher};
    let mut hasher = std::collections::hash_map::DefaultHasher::new();
    lookup.hash(&mut hasher);
    &ISSUED_URLS[hasher.finish() as usize % ISSUED_URLS.len()]
}

pub fn signed_image_url(user: &User, image_id: &str) -> String {
    let now = Utc::now().timestamp();
    let issue = |exp| {
        let (exp, signature) = crate::signature_file::SIGNATURE_FILE
            .as_ref()
            .and_then(|file| file.lookup(&user.image_key, image_id))
            .filter(|(expires, _)| *expires > now + 60)
            .inspect(|_| {
                SIGNATURE_MEMO
                    .stats
                    .file_hits
                    .fetch_add(1, Ordering::Relaxed);
            })
            .unwrap_or_else(|| (exp, image_signature(&user.image_key, image_id, exp)));
        let url = format!(
            "/api/images/{}?exp={}&sig={}",
            utf8_percent_encode(image_id, PATH_SEGMENT),
            exp,
            URL_SAFE_NO_PAD.encode(signature)
        );
        IssuedUrl {
            expires: exp,
            signature,
            url,
        }
    };
    if !*ISSUED_URLS_ENABLED {
        return issue(now + IMAGE_URL_LIFETIME.num_seconds()).url;
    }
    // This separate cache keys an already-issued URL by the current owner key
    // and image ID. Every caller still performs fresh authorization first.
    let lookup = issued_url_key(&user.image_key, image_id);
    // Compute under a small shard lock so simultaneous first issuances agree.
    // Verification releases this lock before falling back to image_signature.
    issued_url_shard(&lookup)
        .lock()
        .unwrap()
        .get_or_issue(lookup, now, *ISSUED_URL_CAPACITY, issue)
}

#[cfg(test)]
mod cache_tests {
    use super::*;

    #[test]
    fn issued_urls_preserve_original_expiry_and_renew_at_boundary() {
        let mut cache = IssuedUrlCache::default();
        let key = SignatureKey {
            key: MemoBytes::new(&[1; 32]),
            image_id: MemoBytes::new(b"image"),
            exp: 0,
        };
        let issue = |exp: i64| IssuedUrl {
            expires: exp,
            signature: [0; 32],
            url: exp.to_string(),
        };
        assert_eq!(cache.get_or_issue(key.clone(), 1000, 2, issue), "87400");
        assert_eq!(
            cache.get_or_issue(key.clone(), 2000, 2, |_| panic!("reuse")),
            "87400"
        );
        assert_eq!(
            cache.get_or_issue(key.clone(), 87339, 2, |_| panic!("reuse")),
            "87400"
        );
        assert_eq!(cache.get_or_issue(key.clone(), 87340, 2, issue), "173740");
        assert_eq!(cache.order.len(), 1);
        let changed = SignatureKey {
            key: MemoBytes::new(&[2; 32]),
            ..key.clone()
        };
        assert_eq!(cache.get_or_issue(changed, 87341, 2, issue), "173741");
        assert_eq!(cache.values.len(), 2);
        for i in 0..20 {
            let key = SignatureKey {
                image_id: MemoBytes::new(format!("other{i}").as_bytes()),
                ..key.clone()
            };
            cache.get_or_issue(key, 1000, 2, issue);
            assert_eq!(cache.values.len(), 2);
            assert_eq!(cache.order.len(), 2);
        }
    }

    #[test]
    fn file_issued_url_keeps_its_older_expiry_when_cached() {
        let mut cache = IssuedUrlCache::default();
        let key = SignatureKey {
            key: MemoBytes::new(&[1; 32]),
            image_id: MemoBytes::new(b"image"),
            exp: 0,
        };
        cache.get_or_issue(key.clone(), 1000, 2, |_| IssuedUrl {
            expires: 5000,
            signature: [0; 32],
            url: "file".into(),
        });
        assert_eq!(
            cache.get_or_issue(key.clone(), 4939, 2, |_| panic!("reuse")),
            "file"
        );
        assert_eq!(
            cache.get_or_issue(key, 4940, 2, |expires| IssuedUrl {
                expires,
                signature: [0; 32],
                url: "renewed".into()
            }),
            "renewed"
        );
    }

    #[test]
    fn verification_reuses_issued_signature_after_memo_eviction() {
        let image = uuid::Uuid::new_v4().to_string();
        let key = [31; 32];
        let user = User {
            image_key: key.to_vec(),
            ..User::default()
        };
        signed_image_url(&user, &image);
        let lookup = issued_url_key(&key, &image);
        let (exp, signature) = {
            let shard = issued_url_shard(&lookup).lock().unwrap();
            let entry = &shard.values[&lookup];
            (entry.expires, entry.signature)
        };
        assert_eq!(signature, calculate_signature(&key, &image, exp));
        let memo_key = SignatureKey {
            exp,
            ..lookup.clone()
        };
        for shard in &SIGNATURE_MEMO.shards {
            let mut shard = shard.lock().unwrap();
            shard.values.remove(&memo_key);
            shard.order.retain(|key| key != &memo_key);
        }
        assert!(verify_image_signature(&key, &image, exp, &signature));
        let mut altered = signature;
        altered[0] ^= 1;
        assert!(!verify_image_signature(&key, &image, exp, &altered));
        assert!(!verify_image_signature(&key, &image, exp, &signature[..31]));
        // Neither a valid nor an invalid comparison should recompute HMAC.
        for shard in &SIGNATURE_MEMO.shards {
            assert!(!shard.lock().unwrap().values.contains_key(&memo_key));
        }
        assert!(!verify_image_signature(&[32; 32], &image, exp, &signature));
        assert!(!verify_image_signature(
            &key,
            "another-image",
            exp,
            &signature
        ));
        assert!(!verify_image_signature(&key, &image, exp - 1, &signature));
        let other_exp = exp - 1;
        let other_signature = calculate_signature(&key, &image, other_exp);
        assert!(verify_image_signature(
            &key,
            &image,
            other_exp,
            &other_signature
        ));
        // URL renewal must not reject an older, still-valid issued signature.
        {
            let mut shard = issued_url_shard(&lookup).lock().unwrap();
            let entry = shard.values.get_mut(&lookup).unwrap();
            entry.expires = exp + 60;
            entry.signature = calculate_signature(&key, &image, exp + 60);
        }
        assert!(verify_image_signature(&key, &image, exp, &signature));
    }

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
