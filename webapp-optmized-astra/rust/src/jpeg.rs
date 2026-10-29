use anyhow::anyhow;
use bytes::Bytes;

use crate::api_error::Error;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Image {
    pub width: u16,
    pub height: u16,
}

pub async fn decode(data: Bytes, maximum_side: u16) -> Result<Image, Error> {
    // application-manual.md permits validating SOI/SOF and dimensions without
    // decoding the compressed pixels. Keep the uploaded bytes unchanged.
    let mut decoder = jpeg_decoder::Decoder::new(&data[..]);
    decoder.read_info().map_err(anyhow::Error::from)?;
    let info = decoder
        .info()
        .ok_or_else(|| anyhow!("missing image info"))?;
    let image = Image {
        width: info.width,
        height: info.height,
    };
    if image.width == 0
        || image.height == 0
        || image.width > maximum_side
        || image.height > maximum_side
    {
        return Err(anyhow!("JPEG dimensions exceed the limit").into());
    }
    Ok(image)
}
