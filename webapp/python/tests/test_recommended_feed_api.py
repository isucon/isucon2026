import uuid
from datetime import UTC, datetime
from typing import Any
from urllib.parse import urlencode

import psycopg
from fastapi.testclient import TestClient

from .conftest import SECRET_KEY, sha256_hex

FEED = "/api/feeds/friends-of-friends"


def test_recommended_feed(client: TestClient, db: psycopg.Connection) -> None:
    for user_id in ["viewer", "f1", "f2", "a1", "a2", "x"]:
        db.execute(
            """
            INSERT INTO users (id, username, secret_key_hash, display_name, region)
            VALUES (%s, %s, %s, %s, 'asia')
            """,
            (user_id, user_id, sha256_hex(SECRET_KEY), user_id),
        )
    login = client.post(
        "/api/auth/login", json={"username": "viewer", "secretKey": SECRET_KEY}
    )
    assert login.status_code == 200, login.text
    headers = {"Authorization": f"Bearer {login.json()['token']}"}
    db.execute(
        """
        INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline, ended_at)
        VALUES ('w', 'asia', '2026-09-23', '2026-09-23T00:00:00Z', '2026-09-23T00:02:00Z', NULL)
        """
    )
    for i, author in enumerate(["viewer", "a1", "a2", "x"]):
        db.execute(
            """
            INSERT INTO image_objects (id, owner_user_id, data, media_type)
            VALUES (%s, %s, %s, 'image/jpeg')
            """,
            (f"{author}-image", author, b"image"),
        )
        db.execute(
            """
            INSERT INTO posts (id, author_id, posting_window_id, image_id, caption, visibility, location_kind, posted_at)
            VALUES (%s, %s, 'w', %s, 'caption', 'friends_of_friends', 'none', %s)
            """,
            (
                author,
                author,
                f"{author}-image",
                datetime(2026, 9, 23, 0, 1, i, tzinfo=UTC),
            ),
        )
    for pair in [
        ("f1", "viewer"),
        ("f2", "viewer"),
        ("a1", "f1"),
        ("a1", "f2"),
        ("a2", "f1"),
        ("a1", "x"),
        ("a2", "x"),
        ("f2", "x"),
    ]:
        db.execute(
            "INSERT INTO friendships (id, user_low_id, user_high_id) VALUES (%s, %s, %s)",
            (str(uuid.uuid4()), min(pair), max(pair)),
        )

    def request(path: str, status: int) -> Any:
        response = client.get(path, headers=headers)
        assert response.status_code == status, response.text
        return response.json()

    def assert_ranking(path: str, *expected: str) -> Any:
        page = request(path, 200)
        for item in page["items"]:
            assert isinstance(item.get("score"), int), f"{path}: {item['id']}"
        assert [f"{item['id']}:{item['score']}" for item in page["items"]] == list(
            expected
        )
        return page

    page = assert_ranking(FEED, "a1:7", "x:6", "a2:4")
    db.execute(
        """
        INSERT INTO realmojis (id, post_id, reactor_id, image_id, reaction_type, created_at)
        VALUES (%s, 'a2', 'f1', 'rm', 'smile', clock_timestamp())
        """,
        (str(uuid.uuid4()),),
    )
    db.execute(
        """
        INSERT INTO comments (id, post_id, author_id, body, created_at)
        VALUES (%s, 'a2', 'f1', 'nice', clock_timestamp()), (%s, 'a2', 'x', 'nice', clock_timestamp())
        """,
        (str(uuid.uuid4()), str(uuid.uuid4())),
    )
    page = assert_ranking(FEED, "a2:14", "a1:7", "x:6")
    db.execute(
        """
        INSERT INTO realmojis (id, post_id, reactor_id, image_id, reaction_type, created_at)
        VALUES (%s, 'a1', 'f2', 'rm', 'smile', clock_timestamp())
        """,
        (str(uuid.uuid4()),),
    )
    db.execute("UPDATE users SET status='deleted' WHERE id='f2'")
    assert_ranking(FEED, "a2:14", "a1:7", "x:6")

    cursor = page["items"][1]
    before = urlencode(
        {
            "beforeScore": cursor["score"],
            "beforePostedAt": cursor["postedAt"],
            "beforeId": cursor["id"],
        }
    )
    assert_ranking(f"{FEED}?{before}", "x:6")
    request(f"{FEED}?beforeScore=7", 400)
    request(f"{FEED}?beforePostedAt=2026-09-23T00:01:01Z&beforeId=a1", 400)

    for item in request("/api/feeds/friends", 200)["items"]:
        assert "score" not in item, item["id"]
