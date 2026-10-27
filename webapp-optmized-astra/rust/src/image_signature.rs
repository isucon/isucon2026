use std::collections::{HashMap, VecDeque};
use std::sync::{Mutex, OnceLock};

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
struct SignatureKey {
    key: Vec<u8>,
    image_id: String,
    exp: i64,
}

#[derive(Default)]
struct SignatureCache {
    values: HashMap<SignatureKey, [u8; 32]>,
    order: VecDeque<SignatureKey>,
}

pub fn image_signature(key: &[u8], image_id: &str, exp: i64) -> Vec<u8> {
    // Only the pure cryptographic calculation is memoized. Database visibility,
    // current signing keys, and expiration are still checked by the handler.
    const SHARDS: usize = 16;
    const CAPACITY_PER_SHARD: usize = 512;
    static CACHE: OnceLock<[Mutex<SignatureCache>; SHARDS]> = OnceLock::new();
    let caches = CACHE.get_or_init(|| std::array::from_fn(|_| Mutex::default()));
    let lookup = SignatureKey {
        key: key.to_vec(),
        image_id: image_id.to_owned(),
        exp,
    };
    use std::hash::{Hash, Hasher};
    let mut hasher = std::collections::hash_map::DefaultHasher::new();
    lookup.hash(&mut hasher);
    let cache = &caches[hasher.finish() as usize % SHARDS];
    if let Some(value) = cache.lock().unwrap().values.get(&lookup) {
        return value.to_vec();
    }
    let base = Hmac::<Sha256>::new_from_slice(key).expect("HMAC accepts keys of any size");
    let mut mac = base.clone();
    mac.update(format!("{exp}:{image_id}").as_bytes());
    let mut value: [u8; 32] = mac.finalize().into_bytes().into();
    for _ in 1..IMAGE_SIGNATURE_ITERATIONS {
        let mut mac = base.clone();
        mac.update(&value);
        value = mac.finalize().into_bytes().into();
    }
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
    value.to_vec()
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
