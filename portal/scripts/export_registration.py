"""Run inside the source Django environment with manage.py shell (see migration guide)."""

import hashlib
import json
import os
from pathlib import Path
from urllib.parse import urlsplit


MAX_AVATAR_BYTES = 10 * 1024 * 1024
USER_FIELDS = (
    "id",
    "team_id",
    "username",
    "display_name",
    "discord_id",
    "discord_username",
    "is_student",
    "is_staff",
    "is_active",
    "email",
    "tshirt_size",
    "terms_agreed_at",
    "privacy_agreed_at",
    "sponsor_provide_agreed_at",
    "sponsor_provide_withdrawn_at",
    "minor_parental_consent",
    "icon",
)
TEAM_FIELDS = (
    "id",
    "owner_id",
    "name",
    "is_active",
    "is_guest",
    "want_local_participation",
    "is_local_participation",
    "aws_az",
    "declined_at",
    "banned_at",
    "created_at",
    "updated_at",
    "envchecked_at",
    "coupon_agreed_at",
    "aws_coupon",
    "sacloud_coupon",
)


def copy_avatar(storage, key, directory, base_url):
    """Read the storage object itself, never export a temporary signed URL."""
    url = urlsplit(base_url)
    if (
        url.scheme != "https"
        or not url.netloc
        or url.query
        or url.fragment
        or url.username
    ):
        raise ValueError(
            "avatar base must be a permanent HTTPS URL without query/credentials"
        )
    with storage.open(key, "rb") as source:
        content = source.read(MAX_AVATAR_BYTES + 1)
    if len(content) > MAX_AVATAR_BYTES:
        raise ValueError("avatar exceeds 10 MiB")
    if content.startswith(b"\x89PNG\r\n\x1a\n"):
        extension = "png"
    elif content.startswith(b"\xff\xd8\xff"):
        extension = "jpg"
    elif content.startswith((b"GIF87a", b"GIF89a")):
        extension = "gif"
    elif content.startswith(b"RIFF") and content[8:12] == b"WEBP":
        extension = "webp"
    else:
        raise ValueError("unsupported avatar format")
    filename = hashlib.sha256(content).hexdigest() + "." + extension
    (directory / filename).write_bytes(content)
    return base_url.rstrip("/") + "/" + filename


def select_only(execute, sql, params, many, context):
    if not sql.lstrip().upper().startswith("SELECT "):
        raise ValueError("export attempted a non-SELECT query")
    return execute(sql, params, many, context)


def export():
    from django.apps import apps
    from django.core.serializers.json import DjangoJSONEncoder
    from django.db import connection, transaction
    from social_django.models import UserSocialAuth

    directory = Path(os.environ["PORTAL_EXPORT_DIR"])
    directory.mkdir(mode=0o700, parents=True, exist_ok=False)
    icons = directory / "avatars"
    icons.mkdir(mode=0o700)
    base_url = os.environ["PORTAL_AVATAR_BASE_URL"]
    user_model = apps.get_model("authentication", "User")
    team_model = apps.get_model("authentication", "Team")
    if connection.vendor != "mysql":
        raise ValueError("export requires the Django MySQL/TiDB source")
    # Use a SELECT-only credential as an additional safeguard. Do not call model.save().
    with connection.cursor() as cursor:
        cursor.execute("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ")
    with transaction.atomic(), connection.execute_wrapper(select_only):
        known_ignored = {
            "password",
            "last_login",
            "is_superuser",
            "first_name",
            "last_name",
            "date_joined",
            "discord_access_token",
            "discord_refresh_token",
            "discord_expired_at",
        }
        for model, selected, ignored in (
            (user_model, USER_FIELDS, known_ignored),
            (team_model, TEAM_FIELDS, {"password", "envcheck_token"}),
        ):
            unknown = (
                {field.attname for field in model._meta.concrete_fields}
                - set(selected)
                - ignored
            )
            if unknown:
                raise ValueError("unmapped source fields: " + ",".join(sorted(unknown)))
        users = list(user_model._base_manager.order_by("id").values(*USER_FIELDS))
        teams = list(team_model._base_manager.order_by("id").values(*TEAM_FIELDS))
        github = {}
        user_ids = {user["id"] for user in users}
        for auth in UserSocialAuth.objects.order_by("id").values(
            "user_id", "provider", "uid"
        ):
            if auth["user_id"] not in user_ids:
                raise ValueError("orphan social identity")
            if auth["provider"] != "github":
                raise ValueError(
                    "unsupported social auth provider: " + auth["provider"]
                )
            if auth["user_id"] in github:
                raise ValueError("multiple GitHub identities for one user")
            github[auth["user_id"]] = auth["uid"]
        other = {}
        for app in apps.get_app_configs():
            if not app.name.startswith("isucon.portal"):
                continue
            for model in app.get_models():
                if model not in (user_model, team_model):
                    other[model._meta.label] = model._base_manager.count()
        avatars = {}
        storage = user_model._meta.get_field("icon").storage
        for user in users:
            user["github_id"] = github.get(user["id"], "")
            user["icon"] = user["icon"] or ""
            if (
                user["team_id"] is not None
                and user["icon"]
                and user["icon"] not in avatars
            ):
                avatars[user["icon"]] = copy_avatar(
                    storage, user["icon"], icons, base_url
                )
        snapshot = {
            "version": 1,
            "users": users,
            "teams": teams,
            "other": other,
            "avatars": avatars,
        }

        # DjangoJSONEncoder truncates sub-millisecond precision; datetime.isoformat preserves it.
        class SnapshotEncoder(DjangoJSONEncoder):
            def default(self, value):
                from datetime import datetime

                if isinstance(value, datetime):
                    if value.tzinfo is None:
                        raise ValueError(
                            "naive datetime in source; enable USE_TZ before exporting"
                        )
                    return value.isoformat()
                return super().default(value)

        with (directory / "snapshot.json").open("x") as output:
            os.chmod(output.name, 0o600)
            json.dump(
                snapshot, output, cls=SnapshotEncoder, ensure_ascii=False, indent=2
            )
    print(
        "exported teams=%d users=%d avatars=%d" % (len(teams), len(users), len(avatars))
    )


if __name__ == "__main__":
    export()
