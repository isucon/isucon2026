from fastapi.testclient import TestClient

from .conftest import JPEG, Actors, make_jpeg


def test_register_login_logout(client: TestClient, actors: Actors) -> None:
    response = client.post(
        "/api/auth/register",
        json={"username": "alice", "displayName": "アリス", "region": "asia"},
    )
    assert response.status_code == 201
    registered = response.json()
    assert registered["user"] == {
        "id": registered["user"]["id"],
        "username": "alice",
        "displayName": "アリス",
        "bio": "",
        "region": "asia",
        "accountType": "regular",
        "status": "active",
    }
    duplicate = client.post(
        "/api/auth/register",
        json={"username": "alice", "displayName": "A", "region": "asia"},
    )
    assert duplicate.status_code == 409
    assert duplicate.json() == {"error": "username_taken"}

    credentials = {"username": "alice", "secretKey": registered["secretKey"]}
    first = client.post("/api/auth/login", json=credentials).json()["token"]
    second = client.post("/api/auth/login", json=credentials).json()["token"]
    # ログインし直すと以前のセッションは無効になる
    assert (
        client.get("/api/me", headers={"Authorization": f"Bearer {first}"}).status_code
        == 401
    )
    me = client.get("/api/me", headers={"Authorization": f"Bearer {second}"})
    assert me.status_code == 200
    assert me.json()["profileImageUrl"] is None

    wrong = client.post("/api/auth/login", json={"username": "alice", "secretKey": "x"})
    assert wrong.status_code == 401
    assert wrong.json() == {"error": "invalid_credentials"}

    headers = {"Authorization": f"Bearer {second}"}
    assert client.post("/api/auth/logout", headers=headers).status_code == 204
    assert client.get("/api/me", headers=headers).status_code == 401


def test_register_validation(client: TestClient) -> None:
    for body in (
        {"username": "Alice", "displayName": "A", "region": "asia"},
        {"username": "alice", "displayName": "", "region": "asia"},
        {"username": "alice", "displayName": "A", "region": "mars"},
        {},
    ):
        response = client.post("/api/auth/register", json=body)
        assert response.status_code == 400
        assert response.json() == {"error": "invalid_registration"}


def test_session_header(client: TestClient, actors: Actors) -> None:
    assert client.get("/api/me").json() == {"error": "invalid_session"}
    assert (
        client.get("/api/me", headers={"Authorization": "Basic x"}).status_code == 401
    )
    assert (
        client.get("/api/me", headers={"Authorization": "Bearer unknown"}).status_code
        == 401
    )


def test_region(client: TestClient, actors: Actors) -> None:
    _, eve = actors.user("eve", region="europe")
    # 別リージョンのユーザーはリージョンを確認する API を使えない
    response = client.get("/api/me", headers=eve)
    assert response.status_code == 403
    assert response.json() == {"error": "region_mismatch"}
    # プロフィール更新はリージョンを確認しない
    updated = client.patch("/api/me", json={"region": "asia"}, headers=eve)
    assert updated.status_code == 200
    assert updated.json()["region"] == "asia"
    assert client.get("/api/me", headers=eve).status_code == 200


def test_update_me_and_profile_image(client: TestClient, actors: Actors) -> None:
    _, alice = actors.user("alice")
    assert client.patch("/api/me", json={}, headers=alice).json() == {
        "error": "invalid_profile"
    }
    assert (
        client.patch("/api/me", json={"bio": "x" * 501}, headers=alice).status_code
        == 400
    )
    response = client.patch(
        "/api/me", json={"displayName": "Alice", "bio": "hi"}, headers=alice
    )
    assert response.json()["displayName"] == "Alice"
    assert response.json()["bio"] == "hi"

    put = client.put(
        "/api/me/profile-image",
        content=b"png",
        headers={**alice, "Content-Type": "image/png"},
    )
    assert put.status_code == 415
    # JPEG として復号できないものと、512px を超えるものは受け付けない
    for content in (b"jpeg", JPEG[:-2], make_jpeg(513, 1), make_jpeg(1, 513)):
        put = client.put(
            "/api/me/profile-image",
            content=content,
            headers={**alice, "Content-Type": "image/jpeg"},
        )
        assert put.json() == {"error": "invalid_profile_image"}
    assert client.get("/api/me", headers=alice).json()["profileImageUrl"] is None
    put = client.put(
        "/api/me/profile-image",
        content=make_jpeg(512, 512),
        headers={**alice, "Content-Type": "image/jpeg"},
    )
    assert put.status_code == 204
    put = client.put(
        "/api/me/profile-image",
        content=JPEG,
        headers={**alice, "Content-Type": "image/jpeg"},
    )
    assert put.status_code == 204
    url = client.get("/api/me", headers=alice).json()["profileImageUrl"]
    image = client.get(url)
    assert image.status_code == 200
    assert image.content == JPEG
    assert image.headers["content-type"] == "image/jpeg"
    assert image.headers["cache-control"] == "no-store"

    assert client.delete("/api/me/profile-image", headers=alice).status_code == 204
    assert client.delete("/api/me/profile-image", headers=alice).status_code == 404
    assert client.get(url).status_code == 403


def test_account_deletion(client: TestClient, actors: Actors) -> None:
    _, alice = actors.user("alice")
    assert client.post("/api/me/account-deletion", headers=alice).status_code == 204
    assert client.get("/api/me", headers=alice).status_code == 401
    login = client.post(
        "/api/auth/login", json={"username": "alice", "secretKey": "secret"}
    )
    assert login.status_code == 401


def test_admin(client: TestClient, actors: Actors) -> None:
    bob_id, _ = actors.user("bob")
    assert (
        client.post(
            "/api/admin/auth/login", json={"username": "admin", "password": "x"}
        ).status_code
        == 401
    )
    admin = actors.admin()
    path = f"/api/admin/users/{bob_id}/certification"
    assert client.put(path).status_code == 401
    assert client.put(path, headers=admin).status_code == 204
    assert client.put(path, headers=admin).json() == {"error": "certification_conflict"}
    assert client.delete(path, headers=admin).json() == {
        "error": "certification_irreversible"
    }
    assert client.post("/api/admin/auth/logout", headers=admin).status_code == 204
    assert client.put(path, headers=admin).status_code == 401


def test_unknown_route(client: TestClient) -> None:
    response = client.get("/api/unknown")
    assert response.status_code == 404
    assert response.json() == {"error": "Not Found"}
    assert client.put("/api/me").status_code == 405
