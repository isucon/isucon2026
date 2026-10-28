//! Export ZIPs with every image replaced by its digest.

use xxhash_rust::xxh3::xxh3_64_with_seed;

/// What an image becomes: MARKER, then the seed, the XXH3-64 and the length of
/// the image as big-endian u64. The benchmark recognizes it by MARKER.
pub const MARKER: &[u8; 14] = b"\x00isureal-xxh3:";
pub const DIGEST_LEN: usize = MARKER.len() + 24;

pub fn digest_entry(seed: u64, hash: u64, len: u64) -> [u8; DIGEST_LEN] {
    let mut out = [0u8; DIGEST_LEN];
    out[..14].copy_from_slice(MARKER);
    out[14..22].copy_from_slice(&seed.to_be_bytes());
    out[22..30].copy_from_slice(&hash.to_be_bytes());
    out[30..38].copy_from_slice(&len.to_be_bytes());
    out
}

fn le16(b: &[u8], i: usize) -> Option<u16> {
    b.get(i..i + 2).map(|s| u16::from_le_bytes([s[0], s[1]]))
}

fn le32(b: &[u8], i: usize) -> Option<u32> {
    b.get(i..i + 4)
        .map(|s| u32::from_le_bytes([s[0], s[1], s[2], s[3]]))
}

fn put16(out: &mut Vec<u8>, values: &[u16]) {
    for v in values {
        out.extend_from_slice(&v.to_le_bytes());
    }
}

fn put32(out: &mut Vec<u8>, values: &[u32]) {
    for v in values {
        out.extend_from_slice(&v.to_le_bytes());
    }
}

/// Writes into out a copy of the ZIP src where every entry except the JSON files
/// is replaced by its digest. The entries are found through the central
/// directory, so data descriptors are fine. None if src is not a ZIP this
/// understands (ZIP64, encryption or a method other than store and deflate).
pub fn rewrite(src: &[u8], seed: u64, out: &mut Vec<u8>) -> Option<()> {
    out.clear();
    let last = src.len().checked_sub(22)?;
    let first = last.saturating_sub(65535);
    let eocd = (first..=last)
        .rev()
        .find(|&i| le32(src, i) == Some(0x0605_4b50))?;
    let count = le16(src, eocd + 10)? as usize;
    let cd_off = le32(src, eocd + 16)?;
    if count == 0xFFFF || cd_off == u32::MAX {
        return None;
    }
    let mut cd = Vec::with_capacity(count * 96);
    let mut p = cd_off as usize;
    for _ in 0..count {
        if le32(src, p)? != 0x0201_4b50 {
            return None;
        }
        let made_by = le16(src, p + 4)?;
        let flags = le16(src, p + 8)?;
        let method = le16(src, p + 10)?;
        let time = le16(src, p + 12)?;
        let date = le16(src, p + 14)?;
        let crc = le32(src, p + 16)?;
        let csize = le32(src, p + 20)?;
        let usize_ = le32(src, p + 24)?;
        let nlen = le16(src, p + 28)? as usize;
        let elen = le16(src, p + 30)? as usize;
        let clen = le16(src, p + 32)? as usize;
        let ext_attr = le32(src, p + 38)?;
        let lho = le32(src, p + 42)?;
        if flags & 0x0001 != 0 || csize == u32::MAX || usize_ == u32::MAX || lho == u32::MAX {
            return None;
        }
        let name = src.get(p + 46..p + 46 + nlen)?;
        let lho = lho as usize;
        if le32(src, lho)? != 0x0403_4b50 {
            return None;
        }
        let start = lho + 30 + le16(src, lho + 26)? as usize + le16(src, lho + 28)? as usize;
        let data = src.get(start..start + csize as usize)?;

        let digest;
        let (method2, crc2, csize2, usize2, content): (u16, u32, u32, u32, &[u8]) =
            if name.ends_with(b".json") {
                (method, crc, csize, usize_, data)
            } else {
                let (hash, len) = match method {
                    0 => (xxh3_64_with_seed(data, seed), data.len() as u64),
                    8 => {
                        let raw = miniz_oxide::inflate::decompress_to_vec(data).ok()?;
                        (xxh3_64_with_seed(&raw, seed), raw.len() as u64)
                    }
                    _ => return None,
                };
                digest = digest_entry(seed, hash, len);
                (
                    0,
                    crc32fast::hash(&digest),
                    DIGEST_LEN as u32,
                    DIGEST_LEN as u32,
                    &digest[..],
                )
            };
        // The sizes are known now, so no data descriptor follows.
        let flags = flags & !0x0008;
        let offset = out.len() as u32;
        put32(out, &[0x0403_4b50]);
        put16(out, &[20, flags, method2, time, date]);
        put32(out, &[crc2, csize2, usize2]);
        put16(out, &[nlen as u16, 0]);
        out.extend_from_slice(name);
        out.extend_from_slice(content);

        put32(&mut cd, &[0x0201_4b50]);
        put16(&mut cd, &[made_by, 20, flags, method2, time, date]);
        put32(&mut cd, &[crc2, csize2, usize2]);
        put16(&mut cd, &[nlen as u16, 0, 0, 0, 0]);
        put32(&mut cd, &[ext_attr, offset]);
        cd.extend_from_slice(name);
        p += 46 + nlen + elen + clen;
    }
    let cd_start = out.len() as u32;
    out.extend_from_slice(&cd);
    put32(out, &[0x0605_4b50]);
    put16(out, &[0, 0, count as u16, count as u16]);
    put32(out, &[cd.len() as u32, cd_start]);
    put16(out, &[0]);
    Some(())
}

#[cfg(test)]
mod tests {
    use super::*;

    /// A ZIP as Go's archive/zip streams it: posts.json deflated, images stored,
    /// every entry followed by a data descriptor.
    fn sample() -> (Vec<u8>, Vec<u8>, Vec<u8>) {
        let json = br#"[{"id":"a"}]"#.to_vec();
        let deflated = miniz_oxide::deflate::compress_to_vec(&json, 6);
        let image: Vec<u8> = (0..300_000u32).map(|i| (i * 7 + i / 1000) as u8).collect();
        let mut z = Vec::new();
        let mut cd = Vec::new();
        for (name, method, data, raw) in [
            (&b"posts.json"[..], 8u16, &deflated, &json),
            (&b"images/a.jpg"[..], 0, &image, &image),
        ] {
            let offset = z.len() as u32;
            put32(&mut z, &[0x0403_4b50]);
            put16(&mut z, &[20, 0x0808, method, 0, 0x5b21]);
            put32(&mut z, &[0, 0, 0]);
            put16(&mut z, &[name.len() as u16, 0]);
            z.extend_from_slice(name);
            z.extend_from_slice(data);
            let crc = crc32fast::hash(raw);
            put32(
                &mut z,
                &[0x0807_4b50, crc, data.len() as u32, raw.len() as u32],
            );
            put32(&mut cd, &[0x0201_4b50]);
            put16(&mut cd, &[20, 20, 0x0808, method, 0, 0x5b21]);
            put32(&mut cd, &[crc, data.len() as u32, raw.len() as u32]);
            put16(&mut cd, &[name.len() as u16, 0, 0, 0, 0]);
            put32(&mut cd, &[0, offset]);
            cd.extend_from_slice(name);
        }
        let cd_start = z.len() as u32;
        z.extend_from_slice(&cd);
        put32(&mut z, &[0x0605_4b50]);
        put16(&mut z, &[0, 0, 2, 2]);
        put32(&mut z, &[cd.len() as u32, cd_start]);
        put16(&mut z, &[0]);
        (z, json, image)
    }

    /// The entries of a ZIP as (name, method, crc, stored bytes), read through the
    /// central directory and checked against the local headers.
    fn entries(z: &[u8]) -> Vec<(Vec<u8>, u16, u32, Vec<u8>)> {
        let eocd = z.len() - 22;
        let count = le16(z, eocd + 10).unwrap() as usize;
        let mut p = le32(z, eocd + 16).unwrap() as usize;
        let mut out = Vec::new();
        for _ in 0..count {
            let method = le16(z, p + 10).unwrap();
            let crc = le32(z, p + 16).unwrap();
            let csize = le32(z, p + 20).unwrap() as usize;
            let nlen = le16(z, p + 28).unwrap() as usize;
            let lho = le32(z, p + 42).unwrap() as usize;
            assert_eq!(le32(z, lho + 14).unwrap(), crc);
            assert_eq!(le32(z, lho + 18).unwrap() as usize, csize);
            assert_eq!(le16(z, lho + 6).unwrap() & 0x0008, 0);
            let start = lho + 30 + le16(z, lho + 26).unwrap() as usize;
            out.push((
                z[p + 46..p + 46 + nlen].to_vec(),
                method,
                crc,
                z[start..start + csize].to_vec(),
            ));
            p += 46 + nlen;
        }
        out
    }

    #[test]
    fn rewrites_images_only() {
        let (z, json, image) = sample();
        let mut out = Vec::new();
        rewrite(&z, 42, &mut out).unwrap();
        let got = entries(&out);
        assert_eq!(got.len(), 2);
        assert_eq!(got[0].0, b"posts.json");
        assert_eq!(got[0].1, 8);
        assert_eq!(got[0].2, crc32fast::hash(&json));
        assert_eq!(
            miniz_oxide::inflate::decompress_to_vec(&got[0].3).unwrap(),
            json
        );
        assert_eq!(got[1].0, b"images/a.jpg");
        assert_eq!(got[1].1, 0);
        let digest = digest_entry(42, xxh3_64_with_seed(&image, 42), image.len() as u64);
        assert_eq!(got[1].3, digest);
        assert_eq!(got[1].2, crc32fast::hash(&digest));
        assert!(out.len() < 400);
    }

    #[test]
    fn rejects_other_bodies() {
        let mut out = Vec::new();
        assert!(rewrite(b"not a zip at all, just some bytes", 1, &mut out).is_none());
        let (mut z, _, _) = sample();
        z.truncate(z.len() - 30);
        assert!(rewrite(&z, 1, &mut out).is_none());
    }

    /// XXH3 values from the benchmark's Go implementation (zeebo/xxh3 HashSeed).
    #[test]
    fn xxh3_matches_go() {
        let data: Vec<u8> = (0..500_000u32)
            .map(|i| (i.wrapping_mul(2_654_435_761) >> 13) as u8)
            .collect();
        for (n, want) in [
            (0, 0xcc1ca35a1b089c5cu64),
            (9, 0x33410e5f3e844592),
            (241, 0xae3beafb3ac2f987),
            (1024, 0xb75b005f39bf9183),
            (430_123, 0xe4f59ca26b56c409),
        ] {
            assert_eq!(
                xxh3_64_with_seed(&data[..n], 0x0123_4567_89ab_cdef),
                want,
                "length {n}"
            );
        }
    }
}
