import base64
import hashlib
import time
from urllib.parse import parse_qs, urlsplit

import pytest

from isureal.errors import APIError
from isureal.image_signature import (
    encode_signature,
    image_signature,
    image_signing_key,
    signed_image_url,
)
from isureal.routers.images import parse_signed_query

KEY = hashlib.sha256(b"test-secret").digest()


def test_image_signature_vector() -> None:
    # Go 実装のテストと同じ値
    signature = image_signature(KEY, "550e8400-e29b-41d4-a716-446655440000", 1790553600)
    assert encode_signature(signature) == "qv9Jrl_ciG5MowUjgzZ4_bYd2cxIxvpS7zOcTSqxx84"


def test_image_signing_key() -> None:
    assert image_signing_key(KEY.hex()) == KEY
    for invalid in ("", "hash", "abcd"):
        with pytest.raises(ValueError):
            image_signing_key(invalid)


def test_signed_image_url_lifetime() -> None:
    before = int(time.time())
    url = urlsplit(signed_image_url(KEY, "image-id"))
    after = int(time.time())
    query = parse_qs(url.query)
    exp = int(query["exp"][0])
    assert url.path == "/api/images/image-id"
    assert set(query) == {"exp", "sig"}
    assert before + 86400 <= exp <= after + 86400
    assert query["sig"][0] == encode_signature(image_signature(KEY, "image-id", exp))
    assert parse_signed_query(url.query) == (exp, image_signature(KEY, "image-id", exp))


def _sign(exp: int) -> str:
    return encode_signature(image_signature(KEY, "image", exp))


NOW = int(time.time())
FUTURE = str(NOW + 86400)


@pytest.mark.parametrize(
    "query",
    [
        "",
        f"exp={FUTURE}",
        f"sig={_sign(NOW + 86400)}",
        "exp=tomorrow&sig=x",
        "exp=999999999999999999999999&sig=x",
        f"exp=0{FUTURE}&sig=x",
        f"exp=%2B{FUTURE}&sig={_sign(NOW + 86400)}",
        f"exp={FUTURE}&sig=***",
        f"exp={FUTURE}&sig=YQ",
        f"exp={FUTURE}&sig={_sign(NOW + 86400)}%0A",
        f"exp={FUTURE}&sig={base64.urlsafe_b64encode(b'x' * 32).decode()}",
        f"exp={FUTURE}&exp={FUTURE}&sig=x",
        f"exp={FUTURE}&sig=x&sig=y",
        f"exp={FUTURE}&sig=%zz",
        f"exp={NOW - 1}&sig={_sign(NOW - 1)}",
        f"exp={NOW}&sig={_sign(NOW)}",
    ],
)
def test_parse_signed_query_rejects(query: str) -> None:
    with pytest.raises(APIError) as error:
        parse_signed_query(query)
    assert error.value.status == 403
    assert error.value.code == "image_forbidden"
    assert error.value.headers == {"Cache-Control": "no-store"}
