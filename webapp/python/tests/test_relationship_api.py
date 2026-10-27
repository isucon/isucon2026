from fastapi.testclient import TestClient

from .conftest import Actors


def kinds(
    client: TestClient, kind: str, headers: dict[str, str]
) -> list[tuple[str, str]]:
    response = client.get(f"/api/relationships/{kind}", headers=headers)
    assert response.status_code == 200, response.text
    return [
        (item["user"]["username"], item["status"]) for item in response.json()["items"]
    ]


def test_friend_requests(client: TestClient, actors: Actors) -> None:
    alice = actors.user("alice")
    bob = actors.user("bob")
    assert client.post(
        "/api/friend-requests", json={"recipientId": alice[0]}, headers=alice[1]
    ).json() == {"error": "invalid_relationship"}
    assert (
        client.post(
            "/api/friend-requests", json={"recipientId": "x"}, headers=alice[1]
        ).status_code
        == 404
    )
    request = client.post(
        "/api/friend-requests", json={"recipientId": bob[0]}, headers=alice[1]
    )
    assert request.status_code == 201
    assert request.json()["status"] == "pending"
    assert (
        client.post(
            "/api/friend-requests", json={"recipientId": bob[0]}, headers=alice[1]
        ).status_code
        == 409
    )
    assert kinds(client, "outgoing-requests", alice[1]) == [("bob", "pending")]
    assert kinds(client, "incoming-requests", bob[1]) == [("alice", "pending")]

    request_id = request.json()["relationId"]
    assert (
        client.post(
            f"/api/friend-requests/{request_id}/accept", headers=alice[1]
        ).status_code
        == 403
    )
    assert (
        client.post(
            f"/api/friend-requests/{request_id}/cancel", headers=bob[1]
        ).status_code
        == 403
    )
    accepted = client.post(f"/api/friend-requests/{request_id}/accept", headers=bob[1])
    assert accepted.json()["status"] == "accepted"
    assert kinds(client, "friends", alice[1]) == [("bob", "friends")]
    assert (
        client.post(
            f"/api/friend-requests/{request_id}/reject", headers=bob[1]
        ).status_code
        == 403
    )

    assert (
        client.delete(f"/api/friendships/{bob[0]}", headers=alice[1]).status_code == 204
    )
    assert (
        client.delete(f"/api/friendships/{bob[0]}", headers=alice[1]).status_code == 409
    )

    # 相手からの申請があれば、申請し返すと承認になる
    client.post("/api/friend-requests", json={"recipientId": alice[0]}, headers=bob[1])
    mutual = client.post(
        "/api/friend-requests", json={"recipientId": bob[0]}, headers=alice[1]
    )
    assert mutual.json()["status"] == "accepted"


def test_follow_and_block(client: TestClient, actors: Actors) -> None:
    alice = actors.user("alice")
    bob = actors.user("bob")
    star = actors.user("star", account_type="certified_public")
    assert client.put(f"/api/follows/{bob[0]}", headers=alice[1]).json() == {
        "error": "relationship_conflict"
    }
    assert client.put(f"/api/follows/{star[0]}", headers=alice[1]).status_code == 204
    assert client.put(f"/api/follows/{star[0]}", headers=alice[1]).status_code == 409
    assert kinds(client, "following", alice[1]) == [("star", "following")]
    assert kinds(client, "followers", star[1]) == [("alice", "followers")]
    assert client.delete(f"/api/follows/{star[0]}", headers=alice[1]).status_code == 204
    assert client.delete(f"/api/follows/{star[0]}", headers=alice[1]).status_code == 409

    actors.befriend(alice, bob)
    client.put(f"/api/follows/{star[0]}", headers=bob[1])
    assert client.put(f"/api/blocks/{bob[0]}", headers=alice[1]).status_code == 204
    assert client.put(f"/api/blocks/{bob[0]}", headers=alice[1]).status_code == 409
    assert kinds(client, "friends", alice[1]) == []
    assert client.get(f"/api/users/{alice[0]}", headers=bob[1]).json() == {
        "error": "relationship_not_found"
    }
    assert (
        client.post(
            "/api/friend-requests", json={"recipientId": alice[0]}, headers=bob[1]
        ).status_code
        == 409
    )
    assert client.delete(f"/api/blocks/{alice[0]}", headers=bob[1]).json() == {
        "error": "relationship_action_forbidden"
    }
    assert client.delete(f"/api/blocks/{bob[0]}", headers=alice[1]).status_code == 204


def test_users(client: TestClient, actors: Actors) -> None:
    alice = actors.user("alice")
    bob = actors.user("bob")
    star = actors.user("star", account_type="certified_public")
    for user, bio in ((bob, "bob bio"), (star, "star bio")):
        client.patch("/api/me", json={"bio": bio}, headers=user[1])

    # 自己紹介は認定ユーザーか友達にだけ見せる
    assert client.get(f"/api/users/{bob[0]}", headers=alice[1]).json()["bio"] == ""
    assert (
        client.get(f"/api/users/{star[0]}", headers=alice[1]).json()["bio"]
        == "star bio"
    )
    actors.befriend(alice, bob)
    assert (
        client.get(f"/api/users/{bob[0]}", headers=alice[1]).json()["bio"] == "bob bio"
    )
    assert client.get("/api/users/unknown", headers=alice[1]).status_code == 404

    search = client.get("/api/users?q=TAR", headers=alice[1]).json()
    assert [(u["username"], u["bio"]) for u in search["items"]] == [
        ("star", "star bio")
    ]
    assert (
        client.get("/api/users?q=b", headers=alice[1]).json()["items"][0]["bio"] == ""
    )
    assert client.get("/api/users", headers=alice[1]).json() == {
        "error": "invalid_relationship"
    }
    assert client.get("/api/users?q=a&beforeId=x", headers=alice[1]).json() == {
        "error": "invalid_pagination"
    }
    paged = client.get(
        "/api/users?q=a&beforeUsername=alice&beforeId=0", headers=alice[1]
    ).json()
    assert [u["username"] for u in paged["items"]] == ["star"]
