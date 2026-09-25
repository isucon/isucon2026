"""Set a bounded local contest window before starting Docker Compose."""

import datetime
import os
from pathlib import Path


def contest_timing(now, environ):
    def format_time(value):
        return value.replace(microsecond=0).isoformat().replace("+00:00", "Z")

    start = environ.get("DEBUG_CONTEST_START") or format_time(
        now - datetime.timedelta(hours=8)
    )
    end = environ.get("DEBUG_CONTEST_END") or format_time(
        now + datetime.timedelta(hours=1)
    )
    freeze = environ.get("DEBUG_CONTEST_FREEZE") or end
    parsed = [
        datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))
        for value in (start, freeze, end)
    ]
    if (
        any(value.tzinfo is None for value in parsed)
        or not parsed[0] < parsed[1] <= parsed[2]
    ):
        raise ValueError("contest timing must have timezones and start < freeze <= end")
    return dict(
        zip(
            ("DEBUG_CONTEST_START", "DEBUG_CONTEST_FREEZE", "DEBUG_CONTEST_END"),
            (start, freeze, end),
        )
    )


if __name__ == "__main__":
    values = contest_timing(datetime.datetime.now(datetime.timezone.utc), os.environ)
    destination = Path(__file__).resolve().parents[1] / ".env.timing"
    destination.write_text("".join(f"{key}={value}\n" for key, value in values.items()))
