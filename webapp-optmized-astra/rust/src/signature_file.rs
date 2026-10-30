//! Private, regenerable cache of signatures derived from current database rows.
use anyhow::{Result, ensure};
use std::sync::LazyLock;

pub const MAGIC: &[u8; 8] = b"ASTRAS01";
pub const RECORD_BYTES: usize = 80;

pub struct SignatureFile {
    expires: i64,
    records: Vec<u8>,
}

impl SignatureFile {
    pub fn from_bytes(bytes: Vec<u8>) -> Result<Self> {
        ensure!(
            bytes.len() >= 16 && &bytes[..8] == MAGIC,
            "invalid signature cache header"
        );
        ensure!(
            (bytes.len() - 16).is_multiple_of(RECORD_BYTES),
            "invalid signature cache length"
        );
        let expires = i64::from_le_bytes(bytes[8..16].try_into()?);
        let records = bytes[16..].to_vec();
        ensure!(
            records
                .as_chunks::<RECORD_BYTES>()
                .0
                .windows(2)
                .all(|w| w[0][..48] < w[1][..48]),
            "signature cache must be uniquely sorted"
        );
        Ok(Self { expires, records })
    }

    pub fn lookup(&self, key: &[u8], image_id: &str) -> Option<(i64, [u8; 32])> {
        let id = uuid::Uuid::parse_str(image_id).ok()?;
        // HMAC input uses the exact textual ID, so only canonical IDs match.
        if key.len() != 32 || id.hyphenated().encode_lower(&mut [0; 36]) != image_id {
            return None;
        }
        let mut lookup = [0; 48];
        lookup[..32].copy_from_slice(key);
        lookup[32..].copy_from_slice(id.as_bytes());
        let records = self.records.as_chunks::<RECORD_BYTES>().0;
        let index = records
            .binary_search_by(|record| record[..48].cmp(&lookup))
            .ok()?;
        Some((self.expires, records[index][48..].try_into().unwrap()))
    }
}

pub(crate) static SIGNATURE_FILE: LazyLock<Option<SignatureFile>> = LazyLock::new(|| {
    let path = std::env::var("ISUREAL_SIGNATURE_FILE").unwrap_or_default();
    if path.is_empty() {
        return None;
    }
    Some(
        SignatureFile::from_bytes(std::fs::read(path).expect("read signature cache"))
            .expect("load signature cache"),
    )
});

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn matches_exact_current_key_and_canonical_image_id() {
        let id = uuid::Uuid::parse_str("abcdef12-3456-7890-abcd-ef1234567890").unwrap();
        let mut bytes = MAGIC.to_vec();
        bytes.extend_from_slice(&123456_i64.to_le_bytes());
        bytes.extend_from_slice(&[7; 32]);
        bytes.extend_from_slice(id.as_bytes());
        bytes.extend_from_slice(&[9; 32]);
        let cache = SignatureFile::from_bytes(bytes).unwrap();
        assert_eq!(
            cache.lookup(&[7; 32], &id.to_string()),
            Some((123456, [9; 32]))
        );
        assert_eq!(cache.lookup(&[8; 32], &id.to_string()), None);
        assert_eq!(cache.lookup(&[7; 32], &id.to_string().to_uppercase()), None);
        assert_eq!(cache.lookup(&[7; 32], "invalid"), None);
    }
}
