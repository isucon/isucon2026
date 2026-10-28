//! Read immutable historical image ranges from locally replicated archive files.
//! SQL still authorizes every image and supplies its exact immutable reference.
use std::{fs::File, io, os::unix::fs::FileExt, path::Path};

pub const MAX_ARCHIVE_BYTES: u64 = 8 * 1024 * 1024 * 1024;
const MAX_IMAGE_BYTES: u64 = 32 * 1024 * 1024;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ArchiveReference {
    pub archive: String,
    pub offset: u64,
    pub length: usize,
}

impl ArchiveReference {
    pub fn parse(key: &str) -> Option<Self> {
        let mut fields = key.strip_prefix("pack:")?.split(':');
        let archive = fields.next()?;
        let offset = fields.next()?;
        let length = fields.next()?;
        if fields.next().is_some()
            || uuid::Uuid::parse_str(archive).ok()?.simple().to_string() != archive
        {
            return None;
        }
        let position = offset.parse::<u64>().ok()?;
        let bytes = length.parse::<u64>().ok()?;
        if position.to_string() != offset
            || bytes.to_string() != length
            || bytes == 0
            || bytes > MAX_IMAGE_BYTES
            || position.checked_add(bytes)? > MAX_ARCHIVE_BYTES
        {
            return None;
        }
        Some(Self {
            archive: archive.into(),
            offset: position,
            length: bytes as usize,
        })
    }

    pub fn key(&self) -> String {
        format!("pack:{}:{}:{}", self.archive, self.offset, self.length)
    }

    pub fn read(&self, directory: &Path) -> io::Result<Vec<u8>> {
        let file = File::open(directory.join(&self.archive))?;
        let size = file.metadata()?.len();
        if size > MAX_ARCHIVE_BYTES || self.offset + self.length as u64 > size {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "image archive is truncated or oversized",
            ));
        }
        let mut bytes = vec![0; self.length];
        file.read_exact_at(&mut bytes, self.offset)?;
        Ok(bytes)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{fs, io::Write};

    #[test]
    fn references_are_canonical_and_bounded() {
        let id = uuid::Uuid::new_v4().simple().to_string();
        let key = format!("pack:{id}:123:456");
        assert_eq!(ArchiveReference::parse(&key).unwrap().key(), key);
        for key in [
            format!("pack:{id}:0:0"),
            format!("pack:{id}:00:1"),
            format!("pack:{id}:0:01"),
            format!("pack:{id}:-1:1"),
            format!("pack:{id}:0:33554433"),
            format!("pack:{id}:{MAX_ARCHIVE_BYTES}:1"),
            format!("pack:{id}:18446744073709551615:1"),
            format!("pack:{id}:0:1:extra"),
            format!("pack:{}:0:1", id.to_uppercase()),
            "pack:../../escape:0:1".into(),
        ] {
            assert!(ArchiveReference::parse(&key).is_none(), "{key}");
        }
    }

    #[test]
    fn ranges_preserve_exact_images_and_reject_missing_bytes() {
        let directory =
            std::env::temp_dir().join(format!("astra-pack-test-{}", uuid::Uuid::new_v4()));
        fs::create_dir(&directory).unwrap();
        let id = uuid::Uuid::new_v4().simple().to_string();
        let images = [
            include_bytes!("../tests/fixtures/small.jpg").as_slice(),
            include_bytes!("../tests/fixtures/wide.jpg").as_slice(),
        ];
        let mut file = File::create(directory.join(&id)).unwrap();
        for data in images {
            file.write_all(data).unwrap();
        }
        file.sync_all().unwrap();
        drop(file);
        let mut offset = 0;
        for data in images {
            let reference =
                ArchiveReference::parse(&format!("pack:{id}:{offset}:{}", data.len())).unwrap();
            assert_eq!(reference.read(&directory).unwrap(), data);
            offset += data.len();
        }
        assert!(
            ArchiveReference::parse(&format!("pack:{id}:{offset}:1"))
                .unwrap()
                .read(&directory)
                .is_err()
        );
        fs::remove_dir_all(directory).unwrap();
    }
}
