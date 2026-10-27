use std::collections::{HashMap, VecDeque};
use std::sync::{LazyLock, Mutex};

use anyhow::anyhow;
use bytes::Bytes;
use sha2::{Digest, Sha256};
use tokio::sync::Semaphore;

use crate::api_error::Error;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Image {
    pub width: u16,
    pub height: u16,
}

#[derive(Default)]
struct ValidatedImages {
    values: HashMap<[u8; 32], Image>,
    order: VecDeque<[u8; 32]>,
}

static VALIDATED: LazyLock<Mutex<ValidatedImages>> = LazyLock::new(Mutex::default);
static DECODERS: Semaphore = Semaphore::const_new(2);
const CAPACITY: usize = 4096;

fn check_limit(image: Image, maximum_side: u16) -> Result<Image, Error> {
    if image.width > maximum_side || image.height > maximum_side {
        Err(anyhow!("JPEG dimensions exceed the limit").into())
    } else {
        Ok(image)
    }
}

pub async fn decode(data: Bytes, maximum_side: u16) -> Result<Image, Error> {
    // This caches a pure validation result, never authorization or visibility.
    let key: [u8; 32] = Sha256::digest(&data).into();
    if let Some(image) = VALIDATED.lock().unwrap().values.get(&key).copied() {
        return check_limit(image, maximum_side);
    }
    let permit = DECODERS
        .acquire()
        .await
        .expect("decoder semaphore is never closed");
    tokio::task::spawn_blocking(move || {
        let _permit = permit;
        if let Some(image) = VALIDATED.lock().unwrap().values.get(&key).copied() {
            return check_limit(image, maximum_side);
        }
        let mut decoder = jpeg_decoder::Decoder::new(&data[..]);
        decoder.read_info().map_err(anyhow::Error::from)?;
        let info = decoder
            .info()
            .ok_or_else(|| anyhow!("missing image info"))?;
        let image = check_limit(
            Image {
                width: info.width,
                height: info.height,
            },
            maximum_side,
        )?;
        // Parse the entire compressed stream, but discard the pixels. Reduced
        // IDCT scaling avoids producing a full-resolution pixel buffer.
        if info.coding_process != jpeg_decoder::CodingProcess::Lossless {
            decoder.scale(1, 1).map_err(anyhow::Error::from)?;
        }
        decoder.decode().map_err(anyhow::Error::from)?;
        let mut cache = VALIDATED.lock().unwrap();
        if !cache.values.contains_key(&key) {
            if cache.values.len() >= CAPACITY
                && let Some(old) = cache.order.pop_front()
            {
                cache.values.remove(&old);
            }
            cache.order.push_back(key);
            cache.values.insert(key, image);
        }
        Ok(image)
    })
    .await
    .map_err(anyhow::Error::from)?
}
