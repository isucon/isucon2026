import hashlib
from datetime import UTC, datetime

import pytest

from isureal.models.post import Location, Post, Viewer
from isureal.models.user import User, valid_user_text, valid_username

NOW = datetime(2026, 10, 25, tzinfo=UTC)


def make_user(user_id: str = "viewer") -> User:
    return User(
        id=user_id,
        username=user_id,
        secret_key_hash=hashlib.sha256(b"secret").hexdigest(),
        display_name=user_id,
        bio="",
        profile_image_id=None,
        region="asia",
        account_type="regular",
        status="active",
    )


def make_post(
    author_id: str = "author", visibility: str = "public", ended: bool = False
) -> Post:
    return Post(
        id="post",
        author_id=author_id,
        posting_window_id="window",
        image_id="image",
        caption="",
        visibility=visibility,
        location=Location("none"),
        posted_at=NOW,
        on_time_deadline=NOW,
        window_ended_at=NOW if ended else None,
    )


@pytest.mark.parametrize(
    ("location", "valid"),
    [
        (Location("none"), True),
        (Location("none", latitude=1), False),
        (Location("exact", latitude=90, longitude=-180), True),
        (Location("exact", latitude=90.5, longitude=0), False),
        (Location("exact", latitude=float("nan"), longitude=0), False),
        (Location("exact", latitude=1), False),
        (Location("approximate", locality="Tokyo"), True),
        (Location("approximate", locality=""), False),
        (Location("approximate", locality="x" * 101), False),
        (Location("approximate", latitude=1, longitude=1, locality="Tokyo"), False),
        (Location(""), False),
    ],
)
def test_location_is_valid(location: Location, valid: bool) -> None:
    assert location.is_valid() is valid


def test_username_and_text_validation() -> None:
    assert valid_username("alice_01.x-y")
    assert not valid_username("Alice")
    assert not valid_username("alice\n")
    assert not valid_username("a" * 65)
    assert valid_user_text("あ" * 100, 1, 100)
    assert not valid_user_text("", 1, 100)


def test_viewer_can_view() -> None:
    viewer = Viewer(
        user=make_user(),
        has_posted_today=True,
        blocked={"blocked"},
        friends={"friend"},
        friends_of_friends={"fof"},
    )
    assert viewer.can_view(make_post("viewer", "friends", ended=True))
    assert viewer.can_view(make_post("stranger", "public"))
    assert not viewer.can_view(make_post("stranger", "public", ended=True))
    assert not viewer.can_view(make_post("blocked", "public"))
    assert viewer.can_view(make_post("friend", "friends"))
    assert not viewer.can_view(make_post("fof", "friends"))
    assert viewer.can_view(make_post("fof", "friends_of_friends"))
    assert not viewer.can_view(make_post("stranger", "friends_of_friends"))
    viewer.has_posted_today = False
    assert not viewer.can_view(make_post("stranger", "public"))
