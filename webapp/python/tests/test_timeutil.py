import os
import time
from collections.abc import Iterator
from datetime import UTC, date, datetime, timedelta, timezone

import pytest

from isureal.timeutil import (
    add_months,
    format_rfc3339,
    parse_date,
    parse_month,
    parse_rfc3339,
)


@pytest.fixture
def local_timezone(request: pytest.FixtureRequest) -> Iterator[None]:
    original = os.environ.get("TZ")
    os.environ["TZ"] = request.param
    time.tzset()
    yield
    if original is None:
        del os.environ["TZ"]
    else:
        os.environ["TZ"] = original
    time.tzset()


@pytest.mark.parametrize("local_timezone", ["UTC"], indirect=True)
@pytest.mark.usefixtures("local_timezone")
@pytest.mark.parametrize(
    ("value", "expected"),
    [
        (datetime(2026, 10, 25, 1, 2, 3, tzinfo=UTC), "2026-10-25T01:02:03Z"),
        (datetime(2026, 10, 25, 1, 2, 3, 500000, tzinfo=UTC), "2026-10-25T01:02:03.5Z"),
        (
            datetime(2026, 10, 25, 1, 2, 3, 123456, tzinfo=UTC),
            "2026-10-25T01:02:03.123456Z",
        ),
        (
            datetime(2026, 10, 25, 9, 0, tzinfo=timezone(timedelta(hours=9))),
            "2026-10-25T00:00:00Z",
        ),
    ],
)
def test_format_rfc3339_in_utc(value: datetime, expected: str) -> None:
    assert format_rfc3339(value) == expected


@pytest.mark.parametrize("local_timezone", ["Asia/Tokyo"], indirect=True)
@pytest.mark.usefixtures("local_timezone")
def test_format_rfc3339_uses_local_offset() -> None:
    value = datetime(2026, 10, 25, 0, 0, 0, 10, tzinfo=UTC)
    assert format_rfc3339(value) == "2026-10-25T09:00:00.00001+09:00"


@pytest.mark.parametrize(
    ("value", "expected"),
    [
        ("2026-10-25T01:02:03Z", datetime(2026, 10, 25, 1, 2, 3, tzinfo=UTC)),
        (
            "2026-10-25T10:02:03.5+09:00",
            datetime(2026, 10, 25, 1, 2, 3, 500000, tzinfo=UTC),
        ),
        (
            "2026-10-25T01:02:03.123456789Z",
            datetime(2026, 10, 25, 1, 2, 3, 123456, tzinfo=UTC),
        ),
    ],
)
def test_parse_rfc3339(value: str, expected: datetime) -> None:
    assert parse_rfc3339(value) == expected


@pytest.mark.parametrize(
    "value",
    [
        "",
        "2026-10-25",
        "2026-10-25 01:02:03Z",
        "2026-10-25T01:02:03",
        "2026-13-01T00:00:00Z",
        "2026-10-25t01:02:03z",
    ],
)
def test_parse_rfc3339_rejects(value: str) -> None:
    assert parse_rfc3339(value) is None


def test_parse_date_and_month() -> None:
    assert parse_date("2026-02-28") == date(2026, 2, 28)
    assert parse_date("2026-02-30") is None
    assert parse_date("2026-2-28") is None
    assert parse_date("20260228") is None
    assert parse_month("2026-12") == date(2026, 12, 1)
    assert parse_month("2026-1") is None
    assert parse_month("2026-13") is None


def test_add_months() -> None:
    assert add_months(date(2026, 12, 1), 1) == date(2027, 1, 1)
    assert add_months(date(2026, 1, 1), 1) == date(2026, 2, 1)
