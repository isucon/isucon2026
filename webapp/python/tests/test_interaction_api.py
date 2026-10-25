import io
import json
import zipfile
from typing import Any

from fastapi.testclient import TestClient

from .conftest import JPEG, Actors


def test_comments(client: TestClient, actors: Actors) -> None:
    alice = actors.user("alice")
    bob = actors.user("bob")
    carol = actors.user("carol")
    actors.befriend(alice, bob)
    actors.open_window()
    post = actors.post(alice[1], visibility="friends")
    actors.post(bob[1])
    path = f"/api/posts/{post['id']}/comments"

    assert client.post(path, json={"body": ""}, headers=bob[1]).json() == {
        "error": "invalid_interaction"
    }
    assert client.post(path, json={"body": "hi"}, headers=carol[1]).json() == {
        "error": "interaction_forbidden"
    }
    comment = client.post(path, json={"body": "nice"}, headers=bob[1])
    assert comment.status_code == 201
    assert comment.json()["author"]["id"] == bob[0]
    client.post(path, json={"body": "thanks"}, headers=alice[1])
    listed = client.get(path, headers=alice[1]).json()
    assert [c["body"] for c in listed["items"]] == ["nice", "thanks"]
    after = client.get(
        f"{path}?afterCreatedAt={listed['items'][0]['createdAt'].replace('+', '%2B')}"
        f"&afterId={listed['items'][0]['id']}",
        headers=alice[1],
    ).json()
    assert [c["body"] for c in after["items"]] == ["thanks"]

    comment_id = comment.json()["id"]
    assert (
        client.delete(f"/api/comments/{comment_id}", headers=carol[1]).status_code
        == 403
    )
    assert (
        client.delete(f"/api/comments/{comment_id}", headers=alice[1]).status_code
        == 204
    )
    assert (
        client.delete(f"/api/comments/{comment_id}", headers=alice[1]).status_code
        == 404
    )


def test_realmojis_memories_and_export(client: TestClient, actors: Actors) -> None:
    alice = actors.user("alice")
    bob = actors.user("bob")
    actors.open_window()
    post = actors.post(alice[1], caption="day one")
    actors.post(bob[1])
    path = f"/api/posts/{post['id']}/realmojis"

    def react(headers: dict[str, str], reaction_type: str) -> dict[str, Any]:
        response = client.post(
            path,
            files={"image": ("r.jpg", JPEG, "image/jpeg")},
            data={"reactionType": reaction_type},
            headers=headers,
        )
        assert response.status_code == 201, response.text
        body: dict[str, Any] = response.json()
        return body

    own = client.post(
        path,
        files={"image": ("r.jpg", JPEG, "image/jpeg")},
        data={"reactionType": "like"},
        headers=alice[1],
    )
    assert own.json() == {"error": "interaction_forbidden"}
    first = react(bob[1], "heart")
    second = react(bob[1], "laugh")
    # 置き換えられたリアクションの画像は見られない
    assert client.get(first["imageUrl"]).status_code == 403
    assert client.get(second["imageUrl"]).status_code == 200
    listed = client.get(path, headers=alice[1]).json()
    assert [r["reactionType"] for r in listed["items"]] == ["laugh"]

    memories = client.get("/api/memories?month=2099-01", headers=alice[1]).json()
    assert [m["id"] for m in memories["items"]] == [post["id"]]
    assert [r["id"] for r in memories["items"][0]["realmojis"]] == [second["id"]]
    assert memories["items"][0]["comments"] == []
    by_date = client.get("/api/memories/2099-01-01", headers=alice[1]).json()
    assert by_date == memories
    assert client.get("/api/memories/2099-01-02", headers=alice[1]).json() == {
        "items": [],
        "hasMore": False,
    }
    assert client.get("/api/memories?month=2099-1", headers=alice[1]).json() == {
        "error": "invalid_memory_period"
    }

    export = client.get("/api/me/export", headers=alice[1])
    assert export.headers["content-type"] == "application/zip"
    assert (
        export.headers["content-disposition"]
        == 'attachment; filename="isureal-posts.zip"'
    )
    archive = zipfile.ZipFile(io.BytesIO(export.content))
    assert archive.namelist() == ["images/000001.jpg", "posts.json"]
    assert archive.read("images/000001.jpg") == JPEG
    manifest = json.loads(archive.read("posts.json"))
    assert [(m["id"], m["imagePath"]) for m in manifest] == [
        (post["id"], "images/000001.jpg")
    ]

    realmoji_id = second["id"]
    assert (
        client.delete(f"/api/realmojis/{realmoji_id}", headers=alice[1]).status_code
        == 204
    )
    assert (
        client.delete(f"/api/realmojis/{realmoji_id}", headers=alice[1]).status_code
        == 404
    )
