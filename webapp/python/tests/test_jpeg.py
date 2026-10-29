import asyncio
import io

import pytest
from PIL import Image

from isureal.jpeg import jpeg_failure, valid_jpeg

from .conftest import make_jpeg

PHOTO = make_jpeg(300, 200)


def image_bytes(image: Image.Image, image_format: str, **options: object) -> bytes:
    data = io.BytesIO()
    image.save(data, image_format, **options)
    return data.getvalue()


# Go 実装（image/jpeg の Decode）が受け付けるもの
@pytest.mark.parametrize(
    "data",
    [
        PHOTO,
        make_jpeg(300, 200, progressive=True),
        make_jpeg(300, 200, optimize=True),
        make_jpeg(300, 200, subsampling=0),
        image_bytes(Image.new("L", (30, 20)), "JPEG"),
        image_bytes(Image.new("CMYK", (30, 20)), "JPEG"),
        PHOTO + b"trailing bytes after EOI",
        make_jpeg(512, 512),
        make_jpeg(512, 1),
    ],
)
def test_valid_jpeg_accepts(data: bytes) -> None:
    assert asyncio.run(valid_jpeg(data, 512))


# Go 実装が拒否するもの（壊れたデータ、JPEG 以外、大きすぎる画像）
@pytest.mark.parametrize(
    "data",
    [
        b"",
        b"unchanged image bytes\x00\xff",
        bytes.fromhex("ffd8ffe000104a46494600010100000100010000ffd9"),
        PHOTO[:200],
        PHOTO[: len(PHOTO) // 2],
        PHOTO[:-2],
        image_bytes(Image.new("RGB", (30, 20)), "PNG"),
        image_bytes(Image.new("RGB", (30, 20)), "GIF"),
        make_jpeg(513, 1),
        make_jpeg(1, 513),
    ],
)
def test_valid_jpeg_rejects(data: bytes) -> None:
    assert not asyncio.run(valid_jpeg(data, 512))


def test_jpeg_failure_distinguishes_invalid_data_from_dimensions() -> None:
    assert asyncio.run(jpeg_failure(b"not a JPEG", 512)) == "invalid_image"
    assert asyncio.run(jpeg_failure(make_jpeg(513, 1), 512)) == "too_large"
    assert asyncio.run(jpeg_failure(PHOTO, 512)) is None
