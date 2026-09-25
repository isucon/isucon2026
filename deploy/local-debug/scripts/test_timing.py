import datetime
import unittest

from timing import contest_timing


class TimingTest(unittest.TestCase):
    def test_default_covers_history_and_next_hour(self):
        now = datetime.datetime(2026, 9, 25, 8, tzinfo=datetime.timezone.utc)
        self.assertEqual(
            contest_timing(now, {}),
            {
                "DEBUG_CONTEST_START": "2026-09-25T00:00:00Z",
                "DEBUG_CONTEST_FREEZE": "2026-09-25T09:00:00Z",
                "DEBUG_CONTEST_END": "2026-09-25T09:00:00Z",
            },
        )

    def test_explicit_values_survive(self):
        values = {
            "DEBUG_CONTEST_START": "2026-10-31T10:00:00+09:00",
            "DEBUG_CONTEST_FREEZE": "2026-10-31T17:00:00+09:00",
            "DEBUG_CONTEST_END": "2026-10-31T18:00:00+09:00",
        }
        self.assertEqual(
            contest_timing(datetime.datetime.now(datetime.timezone.utc), values), values
        )

    def test_freeze_defaults_to_explicit_end(self):
        now = datetime.datetime(2026, 9, 25, 8, tzinfo=datetime.timezone.utc)
        self.assertEqual(
            contest_timing(now, {"DEBUG_CONTEST_END": "2026-09-25T18:00:00Z"})[
                "DEBUG_CONTEST_FREEZE"
            ],
            "2026-09-25T18:00:00Z",
        )


if __name__ == "__main__":
    unittest.main()
