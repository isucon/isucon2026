import json
import time

import psycopg
import pytest
from fastapi.testclient import TestClient

from isureal.ip_ban import RELOAD_INTERVAL

PROXY = ("127.0.0.1", 40000)


def ban(db: psycopg.Connection, ip: str, until: str) -> None:
    db.execute(
        "INSERT INTO ip_bans (ip, banned_until) VALUES (%s, clock_timestamp() + %s::interval)",
        (ip, until),
    )


def request(
    client: TestClient, method: str, path: str, client_ip: str, status: int
) -> None:
    headers = {"X-Client-IP": client_ip} if client_ip else {}
    response = client.request(
        method,
        path,
        json={"username": "nobody", "secretKey": "wrong"},
        headers=headers,
    )
    assert response.status_code == status, (method, path, client_ip, response.text)
    if status == 403:
        assert response.text == '{"error":"ip_banned"}'


def test_ip_ban(
    client: TestClient, db: psycopg.Connection, capsys: pytest.CaptureFixture[str]
) -> None:
    app = client.app
    proxy = TestClient(app, client=PROXY)
    banned_peer = TestClient(app, client=("203.0.113.7", 40000))

    ban(db, "203.0.113.7", "2 minutes")
    ban(db, "203.0.113.8", "-1 second")
    capsys.readouterr()
    request(proxy, "POST", "/api/auth/login", "203.0.113.7", 403)
    request(proxy, "GET", "/api/images/missing", "203.0.113.7", 403)
    request(proxy, "GET", "/api/unknown", "203.0.113.7", 403)
    request(proxy, "POST", "/api/auth/login", "203.0.113.8", 401)
    request(proxy, "POST", "/api/auth/login", "198.51.100.1", 401)
    request(banned_peer, "POST", "/api/auth/login", "", 403)
    request(banned_peer, "POST", "/api/auth/login", "198.51.100.1", 401)

    # BAN した IP の行動ログも、ルートの行動名で出す
    logs = [json.loads(line) for line in capsys.readouterr().out.splitlines()]
    assert [(e["action"], e["status"], e["ip"]) for e in logs] == [
        ("login", 403, "203.0.113.7"),
        ("get_image", 403, "203.0.113.7"),
        (None, 403, "203.0.113.7"),
        ("login", 401, "203.0.113.8"),
        ("login", 401, "198.51.100.1"),
        ("login", 403, "203.0.113.7"),
        ("login", 401, "198.51.100.1"),
    ]

    # 追加した BAN は再読み込みの間隔のうちに反映され、期限を過ぎたら再読み込みを待たずに解ける
    ban(db, "198.51.100.1", "600 milliseconds")
    time.sleep(RELOAD_INTERVAL + 0.05)
    request(proxy, "POST", "/api/auth/login", "198.51.100.1", 403)
    time.sleep(0.6)
    request(proxy, "POST", "/api/auth/login", "198.51.100.1", 401)


def test_action_log_is_written_to_stdout(
    client: TestClient, capsys: pytest.CaptureFixture[str]
) -> None:
    capsys.readouterr()
    client.post(
        "/api/auth/register",
        json={"username": "alice", "displayName": "A", "region": "asia"},
        headers={"X-Client-IP": "192.0.2.1"},
    )
    assert json.loads(capsys.readouterr().out) == {
        "action": "register",
        "ip": "192.0.2.1",
        "level": "info",
        "status": 201,
        "user_id": None,
        "username": "alice",
    }
