import importlib.util
import tempfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location(
    "export_registration", Path(__file__).with_name("export_registration.py")
)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class AvatarTests(unittest.TestCase):
    def test_copy_is_content_addressed_and_independent_of_signed_urls(self):
        import io

        content = b"\x89PNG\r\n\x1a\nsynthetic-image"

        class Storage:
            def open(self, key, mode):
                self_key.append(key)
                return io.BytesIO(content)

        self_key = []
        with tempfile.TemporaryDirectory() as directory:
            result = module.copy_avatar(
                Storage(),
                "icons/alice.png",
                Path(directory),
                "https://cdn.example.invalid/avatars",
            )
            self.assertEqual(self_key, ["icons/alice.png"])
            self.assertNotIn("?", result)
            self.assertEqual(
                (Path(directory) / result.rsplit("/", 1)[1]).read_bytes(), content
            )
            self.assertEqual(
                result,
                module.copy_avatar(
                    Storage(),
                    "icons/alice.png",
                    Path(directory),
                    "https://cdn.example.invalid/avatars",
                ),
            )

    def test_rejects_expiring_base_url_and_non_image(self):
        import io

        class Storage:
            def open(self, key, mode):
                return io.BytesIO(b"<script>bad</script>")

        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaises(ValueError):
                module.copy_avatar(
                    Storage(),
                    "key",
                    Path(directory),
                    "https://cdn.example.invalid/?Expires=1",
                )
            with self.assertRaises(ValueError):
                module.copy_avatar(
                    Storage(),
                    "key",
                    Path(directory),
                    "https://cdn.example.invalid/avatars",
                )


class QueryGuardTests(unittest.TestCase):
    def test_rejects_writes_before_they_reach_the_database(self):
        from unittest.mock import Mock

        execute = Mock(return_value=[1])
        self.assertEqual(
            module.select_only(
                execute, "SELECT id FROM authentication_user", (), False, {}
            ),
            [1],
        )
        for sql in (
            "UPDATE authentication_user SET email='x'",
            "DELETE FROM authentication_team",
            "INSERT INTO authentication_team VALUES (1)",
        ):
            with self.assertRaises(ValueError):
                module.select_only(execute, sql, (), False, {})
        self.assertEqual(execute.call_count, 1)


class SnapshotExportTests(unittest.TestCase):
    def test_export_preserves_null_false_and_microseconds_without_credentials(self):
        self.check_export("")

    def test_scoped_export_excludes_other_teams_and_their_identities(self):
        self.check_export("20")

    def check_export(self, team_id):
        import contextlib
        import datetime
        import json
        import sys
        from types import SimpleNamespace
        from unittest.mock import patch

        stamp = datetime.datetime(
            2026, 9, 1, 12, 0, 0, 123456, tzinfo=datetime.timezone.utc
        )
        user = dict.fromkeys(module.USER_FIELDS, "")
        user.update(
            id=10,
            team_id=20,
            is_student=True,
            is_staff=False,
            terms_agreed_at=stamp,
            privacy_agreed_at=None,
            sponsor_provide_agreed_at=None,
            sponsor_provide_withdrawn_at=stamp,
            minor_parental_consent=False,
            password="never-export",
            discord_access_token="never-export",
        )
        team = dict.fromkeys(module.TEAM_FIELDS, "")
        team.update(
            id=20,
            owner_id=10,
            created_at=stamp,
            updated_at=stamp,
            password="never-export",
            envcheck_token="never-export",
        )

        class Manager:
            def __init__(self, rows):
                self.rows = rows

            def order_by(self, _):
                return self

            def values(self, *fields):
                return [{key: row[key] for key in fields} for row in self.rows]

            def count(self):
                return len(self.rows)

            def filter(self, **criteria):
                def matches(row):
                    for key, value in criteria.items():
                        if key.endswith("__in"):
                            if row[key[:-4]] not in value:
                                return False
                        elif row[key] != value:
                            return False
                    return True

                return Manager([row for row in self.rows if matches(row)])

        def model(label, row):
            return SimpleNamespace(
                _base_manager=Manager([row]),
                _meta=SimpleNamespace(
                    label=label,
                    concrete_fields=[SimpleNamespace(attname=key) for key in row],
                    get_field=lambda _: SimpleNamespace(storage=None),
                ),
            )

        user_model = model("authentication.User", user)
        team_model = model("authentication.Team", team)
        user_model._base_manager.rows.append({**user, "id": 11, "team_id": 21})
        team_model._base_manager.rows.append({**team, "id": 21, "owner_id": 11})
        coupon_model = model(
            "authentication.RegisterCoupon",
            dict(
                id=1,
                name="invitation",
                token="synthetic",
                team_id=20,
                created_at=stamp,
                updated_at=stamp,
                used_at=None,
            ),
        )
        coupon_model._base_manager.rows.append(
            {
                **coupon_model._base_manager.rows[0],
                "id": 2,
                "team_id": 21,
                "token": "synthetic-other",
            }
        )
        unknown_model = SimpleNamespace(
            _base_manager=Manager([{}]), _meta=SimpleNamespace(label="contact.Ticket")
        )
        apps = SimpleNamespace(
            get_model=lambda app, name: {
                "User": user_model,
                "Team": team_model,
                "RegisterCoupon": coupon_model,
            }[name],
            get_app_configs=lambda: [
                SimpleNamespace(
                    name="isucon.portal.authentication",
                    get_models=lambda: [
                        user_model,
                        team_model,
                        coupon_model,
                        unknown_model,
                    ],
                )
            ],
        )
        statements = []
        connection = SimpleNamespace(
            vendor="mysql",
            cursor=lambda: contextlib.nullcontext(
                SimpleNamespace(execute=statements.append)
            ),
            execute_wrapper=lambda _: contextlib.nullcontext(),
        )
        modules = {
            "django.apps": SimpleNamespace(apps=apps),
            "django.core.serializers.json": SimpleNamespace(
                DjangoJSONEncoder=json.JSONEncoder
            ),
            "django.db": SimpleNamespace(
                connection=connection,
                transaction=SimpleNamespace(atomic=contextlib.nullcontext),
            ),
            "social_django.models": SimpleNamespace(
                UserSocialAuth=SimpleNamespace(
                    objects=Manager(
                        [
                            {"user_id": 10, "provider": "github", "uid": "123"},
                            {"user_id": 11, "provider": "github", "uid": "456"},
                        ]
                    )
                )
            ),
        }
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp) / "export"
            with (
                patch.dict(sys.modules, modules),
                patch.dict(
                    "os.environ",
                    {
                        "PORTAL_EXPORT_DIR": str(directory),
                        "PORTAL_AVATAR_BASE_URL": "https://cdn.example.invalid/avatars",
                        "PORTAL_EXPORT_TEAM_ID": team_id,
                    },
                ),
            ):
                module.export()
            raw = (directory / "snapshot.json").read_text()
            self.assertNotIn("never-export", raw)
            result = json.loads(raw)
            self.assertEqual(len(result["teams"]), 1 if team_id else 2)
            self.assertEqual(len(result["users"]), 1 if team_id else 2)
            self.assertEqual(
                result["users"][0]["terms_agreed_at"],
                "2026-09-01T12:00:00.123456+00:00",
            )
            self.assertIsNone(result["users"][0]["privacy_agreed_at"])
            self.assertIs(result["users"][0]["minor_parental_consent"], False)
            self.assertEqual(result["users"][0]["github_id"], "123")
            self.assertEqual(result["other"]["contact.Ticket"], 1)
            self.assertEqual(len(result["registration_coupons"]), 1 if team_id else 2)
            self.assertEqual(
                result["other"]["authentication.RegisterCoupon"],
                len(result["registration_coupons"]),
            )
            self.assertEqual(result["registration_coupons"][0]["token"], "synthetic")
            self.assertIsNone(result["registration_coupons"][0]["used_at"])
            self.assertEqual(
                (directory / "snapshot.json").stat().st_mode & 0o777, 0o600
            )
        self.assertEqual(
            statements, ["SET TRANSACTION ISOLATION LEVEL REPEATABLE READ"]
        )
