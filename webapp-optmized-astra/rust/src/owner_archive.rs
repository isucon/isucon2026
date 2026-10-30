//! Optional physical layout for immutable archive ranges. SQL continues to
//! supply and authorize the original key; missing aliases use the original file.
use crate::archive_store::{self, ArchiveReference, MAX_ARCHIVE_BYTES};
use bytes::Bytes;
use sha2::{Digest, Sha256};
use std::{
    collections::HashMap,
    fs, io,
    path::{Path, PathBuf},
};

#[derive(Clone, Debug)]
struct Alias {
    source: ([u8; 16], u64, u32),
    archive: [u8; 16],
    offset: u64,
}

pub struct OwnerArchive {
    root: PathBuf,
    aliases: Vec<Alias>,
}
fn invalid() -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, "invalid owner archive index")
}
impl OwnerArchive {
    pub fn load(root: PathBuf, expected_sha256: &str) -> io::Result<Self> {
        let path = root.join("index.bin");
        if fs::metadata(&path)?.len() > 16 + 2_000_000 * 52 {
            return Err(invalid());
        }
        let data = fs::read(path)?;
        if hex::encode(Sha256::digest(&data)) != expected_sha256
            || data.len() < 16
            || &data[..8] != b"ASTRAOA1"
        {
            return Err(invalid());
        }
        let count = u64::from_le_bytes(data[8..16].try_into().unwrap());
        if count == 0 || count > 2_000_000 || data.len() as u64 != 16 + count * 52 {
            return Err(invalid());
        }
        let mut aliases: Vec<Alias> = Vec::with_capacity(count as usize);
        let mut sizes = HashMap::new();
        for row in data[16..].chunks_exact(52) {
            let source = (
                row[..16].try_into().unwrap(),
                u64::from_le_bytes(row[16..24].try_into().unwrap()),
                u32::from_le_bytes(row[24..28].try_into().unwrap()),
            );
            let archive: [u8; 16] = row[28..44].try_into().unwrap();
            let offset = u64::from_le_bytes(row[44..52].try_into().unwrap());
            if source.2 == 0
                || source.2 > 32 * 1024 * 1024
                || source
                    .1
                    .checked_add(source.2.into())
                    .is_none_or(|end| end > MAX_ARCHIVE_BYTES)
                || aliases.last().is_some_and(|old| old.source >= source)
            {
                return Err(invalid());
            }
            let size = match sizes.get(&archive) {
                Some(size) => *size,
                None => {
                    let size = fs::metadata(
                        root.join(uuid::Uuid::from_bytes(archive).simple().to_string()),
                    )?
                    .len();
                    sizes.insert(archive, size);
                    size
                }
            };
            if size > MAX_ARCHIVE_BYTES
                || offset
                    .checked_add(source.2.into())
                    .is_none_or(|end| end > size)
            {
                return Err(invalid());
            }
            aliases.push(Alias {
                source,
                archive,
                offset,
            });
        }
        Ok(Self { root, aliases })
    }
    fn resolve(&self, source: &ArchiveReference) -> Option<ArchiveReference> {
        let key = (
            *uuid::Uuid::parse_str(&source.archive).ok()?.as_bytes(),
            source.offset,
            u32::try_from(source.length).ok()?,
        );
        let index = self.aliases.binary_search_by_key(&key, |a| a.source).ok()?;
        let alias = &self.aliases[index];
        Some(ArchiveReference {
            archive: uuid::Uuid::from_bytes(alias.archive).simple().to_string(),
            offset: alias.offset,
            length: source.length,
        })
    }
    pub fn read(&self, original: &Path, source: &ArchiveReference) -> io::Result<Vec<u8>> {
        match self.resolve(source) {
            Some(target) => target.read(&self.root),
            None => source.read(original),
        }
    }

    pub fn read_many(
        &self,
        original: &Path,
        references: &[ArchiveReference],
    ) -> io::Result<Vec<Bytes>> {
        let mut old = Vec::new();
        let mut mapped = Vec::new();
        let mut old_positions = Vec::new();
        let mut mapped_positions = Vec::new();
        for (position, source) in references.iter().enumerate() {
            if let Some(target) = self.resolve(source) {
                mapped.push(target);
                mapped_positions.push(position);
            } else {
                old.push(source.clone());
                old_positions.push(position);
            }
        }
        let mut result = vec![Bytes::new(); references.len()];
        for (position, bytes) in old_positions
            .into_iter()
            .zip(archive_store::read_many(original, &old)?)
        {
            result[position] = bytes;
        }
        for (position, bytes) in mapped_positions
            .into_iter()
            .zip(archive_store::read_many(&self.root, &mapped)?)
        {
            result[position] = bytes;
        }
        Ok(result)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn record(source: &ArchiveReference, archive: &str, offset: u64) -> Vec<u8> {
        let mut row = uuid::Uuid::parse_str(&source.archive)
            .unwrap()
            .as_bytes()
            .to_vec();
        row.extend(source.offset.to_le_bytes());
        row.extend((source.length as u32).to_le_bytes());
        row.extend(uuid::Uuid::parse_str(archive).unwrap().as_bytes());
        row.extend(offset.to_le_bytes());
        row
    }
    fn write_index(root: &Path, rows: &[Vec<u8>]) -> String {
        let mut bytes = b"ASTRAOA1".to_vec();
        bytes.extend((rows.len() as u64).to_le_bytes());
        for row in rows {
            bytes.extend(row);
        }
        fs::write(root.join("index.bin"), &bytes).unwrap();
        hex::encode(Sha256::digest(bytes))
    }
    #[test]
    fn mixed_layout_preserves_bytes_order_duplicates_and_checks_index_integrity() {
        let root = std::env::temp_dir().join(format!("astra-owner-{}", uuid::Uuid::new_v4()));
        let original = root.join("original");
        let mapped = root.join("mapped");
        fs::create_dir_all(&original).unwrap();
        fs::create_dir(&mapped).unwrap();
        let a = uuid::Uuid::new_v4().simple().to_string();
        let b = uuid::Uuid::new_v4().simple().to_string();
        let first = ArchiveReference {
            archive: a.clone(),
            offset: 0,
            length: 3,
        };
        let second = ArchiveReference {
            archive: a.clone(),
            offset: 3,
            length: 4,
        };
        fs::write(original.join(&a), b"oneTWO!last").unwrap();
        fs::write(mapped.join(&b), b"TWO!one").unwrap();
        let rows = vec![record(&first, &b, 4), record(&second, &b, 0)];
        let digest = write_index(&mapped, &rows);
        let cache = OwnerArchive::load(mapped.clone(), &digest).unwrap();
        let third = ArchiveReference {
            archive: a.clone(),
            offset: 7,
            length: 4,
        };
        let refs = [second.clone(), third, first.clone(), second.clone()];
        assert_eq!(
            cache.read_many(&original, &refs).unwrap(),
            archive_store::read_many(&original, &refs).unwrap()
        );
        // Only exact source ranges alias: an overlapping length remains original.
        let overlap = ArchiveReference {
            archive: a.clone(),
            offset: 0,
            length: 2,
        };
        assert_eq!(
            cache.read_many(&original, &[overlap]).unwrap()[0].as_ref(),
            b"on"
        );
        assert!(OwnerArchive::load(mapped.clone(), &"0".repeat(64)).is_err());
        let digest = write_index(&mapped, &[rows[1].clone(), rows[0].clone()]);
        assert!(OwnerArchive::load(mapped.clone(), &digest).is_err());
        let digest = write_index(&mapped, &[rows[0].clone(), rows[0].clone()]);
        assert!(OwnerArchive::load(mapped.clone(), &digest).is_err());
        let digest = write_index(&mapped, &rows);
        fs::write(mapped.join(&b), b"short").unwrap();
        assert!(OwnerArchive::load(mapped.clone(), &digest).is_err());
        assert!(cache.read_many(&original, &[first]).is_err());
        fs::remove_dir_all(root).unwrap();
    }
}
