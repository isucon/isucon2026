use std::collections::{HashMap, VecDeque};
use std::sync::{Mutex, OnceLock};

use anyhow::anyhow;
use base64::Engine as _;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use chrono::{TimeDelta, Utc};
use hmac::{Hmac, Mac};
use percent_encoding::{AsciiSet, NON_ALPHANUMERIC, utf8_percent_encode};
use sha2::{Digest, Sha256, compress256};

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
    values: HashMap<SignatureKey, [u8; 32]>,
    order: VecDeque<SignatureKey>,
}

pub fn image_signature(key: &[u8], image_id: &str, exp: i64) -> [u8; 32] {
    // Only the pure cryptographic calculation is memoized. Database visibility,
    // current signing keys, and expiration are still checked by the handler.
    const SHARDS: usize = 16;
    const CAPACITY_PER_SHARD: usize = 16384;
    static CACHE: OnceLock<[Mutex<SignatureCache>; SHARDS]> = OnceLock::new();
    let caches = CACHE.get_or_init(|| std::array::from_fn(|_| Mutex::default()));
    let lookup = SignatureKey {
        key: MemoBytes::new(key),
        image_id: MemoBytes::new(image_id.as_bytes()),
        exp,
    };
    use std::hash::{Hash, Hasher};
    let mut hasher = std::collections::hash_map::DefaultHasher::new();
    lookup.hash(&mut hasher);
    let cache = &caches[hasher.finish() as usize % SHARDS];
    if let Some(value) = cache.lock().unwrap().values.get(&lookup) {
        return *value;
    }
    let value = calculate_signature(key, image_id, exp);
    let mut cache = cache.lock().unwrap();
    if !cache.values.contains_key(&lookup) {
        if cache.values.len() >= CAPACITY_PER_SHARD
            && let Some(old) = cache.order.pop_front()
        {
            cache.values.remove(&old);
        }
        cache.order.push_back(lookup.clone());
        cache.values.insert(lookup, value);
    }
    value
}

fn calculate_signature(key: &[u8], image_id: &str, exp: i64) -> [u8; 32] {
    let mut mac = Hmac::<Sha256>::new_from_slice(key).expect("HMAC accepts keys of any size");
    mac.update(format!("{exp}:{image_id}").as_bytes());
    let first: [u8; 32] = mac.finalize().into_bytes().into();

    // HMAC's first inner and outer blocks depend only on the key. After the
    // first HMAC, every message is exactly 32 bytes, so both SHA-256 tails have
    // the same padding and total length (64-byte pad + 32-byte digest).
    const INITIAL: [u32; 8] = [
        0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab,
        0x5be0cd19,
    ];
    let mut normalized = [0u8; 64];
    if key.len() > 64 {
        normalized[..32].copy_from_slice(&Sha256::digest(key));
    } else {
        normalized[..key.len()].copy_from_slice(key);
    }
    let mut inner = INITIAL;
    let mut outer = INITIAL;
    compress256(&mut inner, &[normalized.map(|b| b ^ 0x36).into()]);
    compress256(&mut outer, &[normalized.map(|b| b ^ 0x5c).into()]);
    let mut padded = [0u8; 64];
    padded[..32].copy_from_slice(&first);
    padded[32] = 0x80;
    padded[56..].copy_from_slice(&(96u64 * 8).to_be_bytes());
    let mut blocks = [padded.into()];
    for _ in 1..IMAGE_SIGNATURE_ITERATIONS {
        let mut state = inner;
        compress256(&mut state, &blocks);
        for (output, word) in blocks[0][..32].as_chunks_mut::<4>().0.iter_mut().zip(state) {
            output.copy_from_slice(&word.to_be_bytes());
        }
        let mut state = outer;
        compress256(&mut state, &blocks);
        for (output, word) in blocks[0][..32].as_chunks_mut::<4>().0.iter_mut().zip(state) {
            output.copy_from_slice(&word.to_be_bytes());
        }
    }
    blocks[0][..32].try_into().expect("SHA-256 digest length")
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
mod compression_tests {
    use super::*;

    fn reference(key: &[u8], image: &str, exp: i64) -> [u8; 32] {
        let base = Hmac::<Sha256>::new_from_slice(key).unwrap();
        let mut mac = base.clone();
        mac.update(format!("{exp}:{image}").as_bytes());
        let mut value: [u8; 32] = mac.finalize().into_bytes().into();
        for _ in 1..IMAGE_SIGNATURE_ITERATIONS {
            let mut mac = base.clone();
            mac.update(&value);
            value = mac.finalize().into_bytes().into();
        }
        value
    }

    #[test]
    #[ignore = "CPU timing experiment; run release with --ignored --nocapture"]
    fn compare_uncached_signature_cost() {
        use std::hint::black_box;
        use std::time::Instant;
        let key = [17u8; 32];
        let image = "550e8400-e29b-41d4-a716-446655440000";
        for round in 0..6 {
            let mut measurements = [0.0; 2];
            for mode in [round % 2, (round + 1) % 2] {
                let started = Instant::now();
                for exp in 0..1000 {
                    let result = if mode == 0 {
                        reference(black_box(&key), image, exp)
                    } else {
                        calculate_signature(black_box(&key), image, exp)
                    };
                    black_box(result);
                }
                measurements[mode] = started.elapsed().as_secs_f64();
            }
            println!(
                "round={round} reference_seconds={:.6} specialized_seconds={:.6}",
                measurements[0], measurements[1]
            );
        }
    }
}
