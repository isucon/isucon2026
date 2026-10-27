use bytes::Bytes;
use isureal::jpeg::{Image, decode};

const SMALL: &[u8] = include_bytes!("fixtures/small.jpg");
const WIDE: &[u8] = include_bytes!("fixtures/wide.jpg");

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
async fn validation_checks_compressed_data_not_just_header() {
    let truncated = &SMALL[..SMALL.len() - 32];
    let mut reference = jpeg_decoder::Decoder::new(truncated);
    reference.read_info().unwrap();
    assert!(reference.decode().is_err());
    for _ in 0..2 {
        assert!(
            decode(Bytes::copy_from_slice(truncated), 4096)
                .await
                .is_err()
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
