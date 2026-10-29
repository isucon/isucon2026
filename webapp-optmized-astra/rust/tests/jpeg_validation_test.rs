use bytes::Bytes;
use isureal::jpeg::{Image, decode, decode_profile};

const SMALL: &[u8] = include_bytes!("fixtures/small.jpg");
const WIDE: &[u8] = include_bytes!("fixtures/wide.jpg");

#[tokio::test]
async fn profile_validation_separates_invalid_jpeg_from_dimensions() {
    assert_eq!(
        decode_profile(Bytes::from_static(SMALL))
            .await
            .unwrap()
            .width,
        32
    );
    assert_eq!(
        decode_profile(Bytes::from_static(WIDE))
            .await
            .unwrap()
            .width,
        600
    );
    assert!(
        decode_profile(Bytes::from_static(b"not a JPEG"))
            .await
            .is_err()
    );
    assert!(
        decode_profile(Bytes::copy_from_slice(&SMALL[..SMALL.len() - 32]))
            .await
            .is_err()
    );
}

#[tokio::test]
async fn validation_preserves_original_dimensions_and_endpoint_limits() {
    assert_eq!(
        decode(Bytes::from_static(SMALL), 4096).await.unwrap(),
        Image {
            width: 32,
            height: 24
        }
    );
    assert_eq!(
        decode(Bytes::from_static(SMALL), 512).await.unwrap(),
        Image {
            width: 32,
            height: 24
        }
    );
    assert!(decode(Bytes::from_static(SMALL), 16).await.is_err());
    assert!(decode(Bytes::from_static(WIDE), 512).await.is_err());
    assert_eq!(
        decode(Bytes::from_static(WIDE), 4096).await.unwrap(),
        Image {
            width: 600,
            height: 400
        }
    );
    // A previous successful validation for a post must not bypass avatar limits.
    assert!(decode(Bytes::from_static(WIDE), 512).await.is_err());
}

#[tokio::test]
async fn validation_follows_documented_header_and_dimension_contract() {
    let truncated = &SMALL[..SMALL.len() - 32];
    let mut reference = jpeg_decoder::Decoder::new(truncated);
    reference.read_info().unwrap();
    assert!(reference.decode().is_err());
    for _ in 0..2 {
        assert_eq!(
            decode(Bytes::copy_from_slice(truncated), 4096)
                .await
                .unwrap(),
            Image {
                width: 32,
                height: 24
            }
        );
        assert!(
            decode(Bytes::from_static(b"not a JPEG"), 4096)
                .await
                .is_err()
        );
    }
    let mut corrupt = SMALL.to_vec();
    corrupt[0] = 0;
    assert!(decode(Bytes::from(corrupt), 4096).await.is_err());
}

#[tokio::test]
async fn baseline_progressive_and_malformed_frame_headers() {
    let marker = SMALL
        .windows(2)
        .position(|pair| pair == [0xff, 0xc0])
        .unwrap();
    let segment_len = u16::from_be_bytes([SMALL[marker + 2], SMALL[marker + 3]]) as usize;
    let end = marker + 2 + segment_len;
    for kind in [0xc0, 0xc2] {
        let mut header = SMALL[..end].to_vec();
        header[marker + 1] = kind;
        assert_eq!(
            decode(Bytes::from(header.clone()), 4096).await.unwrap(),
            Image {
                width: 32,
                height: 24
            }
        );
        header.pop();
        assert!(decode(Bytes::from(header), 4096).await.is_err());
    }
    for (height, width) in [(0u16, 32u16), (24, 0), (4097, 32), (24, 4097)] {
        let mut header = SMALL[..end].to_vec();
        header[marker + 5..marker + 7].copy_from_slice(&height.to_be_bytes());
        header[marker + 7..marker + 9].copy_from_slice(&width.to_be_bytes());
        assert!(decode(Bytes::from(header), 4096).await.is_err());
    }
    assert!(
        decode(Bytes::from_static(&[0xff, 0xd8, 0xff, 0xe0, 0, 1]), 4096)
            .await
            .is_err()
    );
}
