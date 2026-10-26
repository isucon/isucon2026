import threading
import time
import uuid
from typing import Any

import psycopg
from fastapi.testclient import TestClient

from isureal.config import Config

from .conftest import JPEG, Actors, make_jpeg


def test_create_post(client: TestClient, actors: Actors) -> None:
    alice_id, alice = actors.user("alice")
    no_window = client.post(
        "/api/posts",
        files={"image": ("a.jpg", JPEG, "image/jpeg")},
        data={"visibility": "public", "location_kind": "none"},
        headers=alice,
    )
    assert no_window.json() == {"error": "post_slot_unavailable"}

    window = actors.open_window(onTimeSeconds=600)
    assert window["localDate"] == "2099-01-01"
    current = client.get("/api/posting-window/current", headers=alice).json()
    assert current == {
        "window": window,
        "hasPosted": False,
        "canPost": True,
        "deletionCount": 0,
    }

    for data in (
        {"visibility": "secret", "location_kind": "none"},
        {"visibility": "public", "location_kind": "exact", "latitude": "1"},
        {"visibility": "public", "location_kind": "approximate"},
        {"visibility": "public", "location_kind": "none", "caption": "x" * 1001},
    ):
        response = client.post(
            "/api/posts",
            files={"image": ("a.jpg", JPEG, "image/jpeg")},
            data=data,
            headers=alice,
        )
        assert response.json() == {"error": "invalid_post"}, data
    # 画像は JPEG として最後まで復号でき、縦横とも 4096px 以下であること
    for image in (b"jpeg", JPEG[:-2], make_jpeg(4097, 1), make_jpeg(1, 4097)):
        response = client.post(
            "/api/posts",
            files={"image": ("a.jpg", image, "image/jpeg")},
            data={"visibility": "public", "location_kind": "none"},
            headers=alice,
        )
        assert response.json() == {"error": "invalid_post"}
    assert (
        client.post(
            "/api/posts", data={"visibility": "public"}, headers=alice
        ).status_code
        == 400
    )

    post = actors.post(
        alice,
        visibility="friends",
        caption="こんにちは",
        location_kind="exact",
        latitude="35.5",
        longitude="139",
    )
    assert post["author"]["id"] == alice_id
    assert post["postingWindowId"] == window["id"]
    assert post["location"] == {"kind": "exact", "latitude": 35.5, "longitude": 139}
    assert post["onTime"] is True
    assert post["lateSeconds"] == 0
    assert client.get(post["imageUrl"]).content == JPEG
    assert client.get("/api/posts/me/current", headers=alice).json() == post
    assert client.get(f"/api/posts/{post['id']}", headers=alice).json() == post

    again = client.post(
        "/api/posts",
        files={"image": ("a.jpg", JPEG, "image/jpeg")},
        data={"visibility": "public", "location_kind": "none"},
        headers=alice,
    )
    assert again.status_code == 409


def test_update_and_delete_post(client: TestClient, actors: Actors) -> None:
    _, alice = actors.user("alice")
    _, bob = actors.user("bob")
    actors.open_window()
    post = actors.post(alice)
    path = f"/api/posts/{post['id']}"
    assert client.patch(path, json={}, headers=alice).status_code == 400
    assert client.patch(path, json={"caption": "x"}, headers=bob).json() == {
        "error": "post_not_editable"
    }
    updated = client.patch(
        path, json={"caption": "edited", "visibility": "friends"}, headers=alice
    )
    assert updated.json()["caption"] == "edited"
    assert updated.json()["visibility"] == "friends"

    assert client.delete(path, headers=bob).status_code == 403
    assert client.delete(path, headers=alice).status_code == 204
    assert client.get(path, headers=alice).status_code == 404
    assert client.get(post["imageUrl"]).status_code == 403
    current = client.get("/api/posting-window/current", headers=alice).json()
    assert current["deletionCount"] == 1
    assert current["canPost"] is True

    # 削除は 1 日 2 回まで
    second = actors.post(alice)
    assert client.delete(f"/api/posts/{second['id']}", headers=alice).status_code == 204
    third = client.post(
        "/api/posts",
        files={"image": ("a.jpg", JPEG, "image/jpeg")},
        data={"visibility": "public", "location_kind": "none"},
        headers=alice,
    )
    assert third.json() == {"error": "post_slot_unavailable"}

    # 窓が閉じた投稿は編集できない
    _, carol = actors.user("carol")
    old = actors.post(carol)
    actors.open_window("2099-01-02")
    closed = client.patch(
        f"/api/posts/{old['id']}", json={"caption": "late"}, headers=carol
    )
    assert closed.json() == {"error": "post_not_editable"}


def test_post_visibility_and_feeds(client: TestClient, actors: Actors) -> None:
    alice = actors.user("alice")
    bob = actors.user("bob")
    carol = actors.user("carol")
    dave = actors.user("dave")
    frank = actors.user("frank")
    actors.befriend(alice, bob)
    actors.befriend(frank, bob)
    actors.befriend(bob, carol)
    actors.open_window()
    friends_post = actors.post(frank[1], visibility="friends")
    fof_post = actors.post(alice[1], visibility="friends_of_friends")
    public_post = actors.post(dave[1], visibility="public")

    # 当日に投稿していない人は他人の投稿を見られない
    assert (
        client.get(f"/api/posts/{public_post['id']}", headers=bob[1]).status_code == 404
    )
    actors.post(bob[1])
    actors.post(carol[1])
    assert (
        client.get(f"/api/posts/{friends_post['id']}", headers=bob[1]).status_code
        == 200
    )
    assert (
        client.get(f"/api/posts/{friends_post['id']}", headers=carol[1]).status_code
        == 404
    )
    assert (
        client.get(f"/api/posts/{fof_post['id']}", headers=carol[1]).status_code == 200
    )

    def feed(name: str, headers: dict[str, str]) -> list[str]:
        response = client.get(f"/api/feeds/{name}", headers=headers)
        assert response.status_code == 200, response.text
        assert response.json()["hasMore"] is False
        return [item["id"] for item in response.json()["items"]]

    assert friends_post["id"] in feed("friends", bob[1])
    assert fof_post["id"] in feed("friends-of-friends", carol[1])
    assert friends_post["id"] not in feed("friends-of-friends", carol[1])
    assert public_post["id"] in feed("public", carol[1])
    assert feed("following", carol[1]) == []
    assert client.get("/api/feeds/unknown", headers=bob[1]).json() == {
        "error": "invalid_post"
    }
    paged = client.get(
        "/api/feeds/public?beforePostedAt=2000-01-01T00:00:00Z", headers=bob[1]
    )
    assert paged.json() == {"error": "invalid_pagination"}
    bad_time = client.get(
        "/api/feeds/public?beforePostedAt=yesterday&beforeId=x", headers=bob[1]
    )
    assert bad_time.status_code == 400


def test_notifications(
    client: TestClient, actors: Actors, db: psycopg.Connection
) -> None:
    _, alice = actors.user("alice")
    admin = actors.admin()
    opened: dict[str, object] = {}

    def open_window_later() -> None:
        time.sleep(1)
        opened.update(
            client.post(
                "/api/admin/posting-window",
                json={"region": "asia", "localDate": "2099-01-01"},
                headers=admin,
            ).json()
        )

    thread = threading.Thread(target=open_window_later)
    thread.start()
    with client.stream("GET", "/api/notifications", headers=alice) as response:
        assert response.status_code == 200
        assert response.headers["content-type"].startswith("text/event-stream")
        body = response.read().decode()
    thread.join()
    assert body.startswith("data: ")
    assert body.endswith("\n\n")
    assert f'"postingWindowId":"{opened["id"]}"' in body
    assert '"region":"asia"' in body


def test_create_post_during_window_rollover(
    client: TestClient, actors: Actors, config: Config
) -> None:
    _, alice = actors.user("alice")
    actors.open_window()
    result: dict[str, Any] = {}

    def create_post() -> None:
        response = client.post(
            "/api/posts",
            files={"image": ("a.jpg", JPEG, "image/jpeg")},
            data={"visibility": "public", "location_kind": "none"},
            headers=alice,
        )
        result.update(status=response.status_code, body=response.json())

    # 窓を切り替えるトランザクションの途中で投稿し、切り替えの完了を待たせる
    with psycopg.connect(config.database_dsn) as rollover:
        rollover.execute(
            "UPDATE posting_windows SET ended_at = clock_timestamp() WHERE region = 'asia' AND ended_at IS NULL"
        )
        rollover.execute(
            """
            INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline)
            VALUES (%s, 'asia', '2099-01-02', clock_timestamp(), clock_timestamp() + interval '1 day')
            """,
            (str(uuid.uuid4()),),
        )
        thread = threading.Thread(target=create_post)
        thread.start()
        time.sleep(1)
        rollover.commit()
    thread.join()
    assert result["status"] == 201, result
