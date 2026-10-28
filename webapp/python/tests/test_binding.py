from typing import Annotated

import pytest
from fastapi import Depends, FastAPI
from fastapi.testclient import TestClient

from isureal.binding import Form, RequestBody, cursor_int, form_body, json_body
from isureal.errors import APIError
from isureal.main import api_error_handler


class Body(RequestBody):
    name: str = ""
    count: int | None = None


app = FastAPI()
app.add_exception_handler(APIError, api_error_handler)


@app.post("/json")
async def post_json(
    body: Annotated[Body, Depends(json_body(Body))],
) -> dict[str, object]:
    return {"name": body.name, "count": body.count}


@app.post("/form")
async def post_form(form: Annotated[Form, Depends(form_body)]) -> dict[str, object]:
    image = form.file("image")
    return {
        "caption": form.get("caption"),
        "latitude": form.float("latitude"),
        "image": await image.read() if image else None,
    }


client = TestClient(app)


@pytest.mark.parametrize(
    ("content", "headers", "expected"),
    [
        (b"", {}, {"name": "", "count": None}),
        (b"null", {"content-type": "application/json"}, {"name": "", "count": None}),
        (
            b'{"name":"a","count":2,"extra":1}',
            {"content-type": "application/json"},
            {"name": "a", "count": 2},
        ),
        (
            b'{"name":null}',
            {"content-type": "application/json; charset=utf-8"},
            {"name": "", "count": None},
        ),
        (
            b'{"name":"\\ud800"}',
            {"content-type": "application/json"},
            {"name": "�", "count": None},
        ),
    ],
)
def test_json_body(
    content: bytes, headers: dict[str, str], expected: dict[str, object]
) -> None:
    response = client.post("/json", content=content, headers=headers)
    assert response.status_code == 200
    assert response.json() == expected


@pytest.mark.parametrize(
    ("content", "content_type", "status"),
    [
        (b'{"name":"a"}', "text/plain", 415),
        (b'{"name":"a"}', "", 415),
        (b'{"name":', "application/json", 400),
        (b'{"name":"a"} x', "application/json", 400),
        (b"[]", "application/json", 400),
        (b'{"name":1}', "application/json", 400),
        (b'{"count":"1"}', "application/json", 400),
        (b'{"count":1.5}', "application/json", 400),
        (b'{"count":true}', "application/json", 400),
    ],
)
def test_json_body_rejects(content: bytes, content_type: str, status: int) -> None:
    response = client.post(
        "/json", content=content, headers={"content-type": content_type}
    )
    assert response.status_code == status
    assert response.json()["error"].startswith(f"code={status}, message=")


def test_form_body() -> None:
    response = client.post(
        "/form",
        files={"image": ("a.jpg", b"jpeg", "image/jpeg")},
        data={"caption": "hi", "latitude": "1.5"},
    )
    assert response.json() == {"caption": "hi", "latitude": 1.5, "image": "jpeg"}
    assert client.post("/form", data={"latitude": ""}).json()["latitude"] == 0.0
    assert client.post("/form").json() == {
        "caption": None,
        "latitude": None,
        "image": None,
    }
    assert client.post("/form", json={"caption": "x"}).json()["caption"] is None


@pytest.mark.parametrize("latitude", ["abc", " 1", "1_0", "0x10"])
def test_form_body_rejects_invalid_number(latitude: str) -> None:
    assert client.post("/form", data={"latitude": latitude}).status_code == 400


def test_form_body_rejects_unknown_media_type() -> None:
    response = client.post(
        "/form", content=b"x", headers={"content-type": "text/plain"}
    )
    assert response.status_code == 415


@pytest.mark.parametrize(
    ("value", "expected"),
    [
        (None, None),
        ("", 0),
        ("+007", 7),
        ("-12", -12),
        ("9223372036854775807", 2**63 - 1),
        ("-9223372036854775808", -(2**63)),
    ],
)
def test_cursor_int(value: str | None, expected: int | None) -> None:
    assert cursor_int(value) == expected


@pytest.mark.parametrize(
    "value", ["1_0", " 1", "0x10", "1.0", "abc", "\u0661", "9223372036854775808"]
)
def test_cursor_int_rejects(value: str) -> None:
    with pytest.raises(APIError):
        cursor_int(value)
