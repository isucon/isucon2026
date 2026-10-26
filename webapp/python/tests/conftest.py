import hashlib
import io
import os
import time
import uuid
from collections.abc import Iterator
from pathlib import Path
from types import SimpleNamespace
from typing import Any

import psycopg
import pytest
from fastapi.testclient import TestClient
from PIL import Image

from isureal import image_signature
from isureal.config import Config
from isureal.main import create_app

SQL_DIR = Path(__file__).resolve().parents[2] / "sql"
# テスト用ユーザーのシークレットキーと管理者のパスワード
SECRET_KEY = "secret"
ADMIN_PASSWORD = "password"


def sha256_hex(value: str) -> str:
    return hashlib.sha256(value.encode()).hexdigest()


@pytest.fixture(scope="session")
def config() -> Config:
    # DB を使うテストは ISUREAL_TEST_DATABASE=1 のときだけ実行する。
    # ISUREAL_DB_* の DB のテーブルを作り直すので、使い捨ての DB を指定すること
    if os.getenv("ISUREAL_TEST_DATABASE") != "1":
        pytest.skip("ISUREAL_TEST_DATABASE is not 1")
    return Config.from_env()


@pytest.fixture
def db(config: Config) -> Iterator[psycopg.Connection]:
    with psycopg.connect(config.database_dsn, autocommit=True) as conn:
        conn.execute("SET client_min_messages = warning")
        conn.execute((SQL_DIR / "001-schema.sql").read_text())
        conn.execute(
            "INSERT INTO administrators (id, username, password_hash) VALUES (%s, 'admin', %s)",
            (str(uuid.uuid4()), sha256_hex(ADMIN_PASSWORD)),
        )
        yield conn


@pytest.fixture
def client(
    config: Config, db: psycopg.Connection, monkeypatch: pytest.MonkeyPatch
) -> Iterator[TestClient]:
    # 署名付き画像 URL の有効期限が秒の境目で変わらないよう、署名に使う時計を止める
    signed_at = int(time.time())
    monkeypatch.setattr(
        image_signature, "time", SimpleNamespace(time=lambda: signed_at)
    )
    with TestClient(create_app(config)) as client:
        yield client


class Actors:
    """テストで使うユーザーと管理者を作る"""

    def __init__(self, client: TestClient, db: psycopg.Connection) -> None:
        self.client = client
        self.db = db

    def user(
        self, username: str, region: str = "asia", account_type: str = "regular"
    ) -> tuple[str, dict[str, str]]:
        """ユーザーを作ってログインし、ID と認証ヘッダーを返す"""
        user_id = str(uuid.uuid4())
        self.db.execute(
            """
            INSERT INTO users (id, username, secret_key_hash, display_name, region, account_type)
            VALUES (%s, %s, %s, %s, %s, %s)
            """,
            (
                user_id,
                username,
                sha256_hex(SECRET_KEY),
                username.title(),
                region,
                account_type,
            ),
        )
        response = self.client.post(
            "/api/auth/login", json={"username": username, "secretKey": SECRET_KEY}
        )
        assert response.status_code == 200, response.text
        return user_id, {"Authorization": f"Bearer {response.json()['token']}"}

    def admin(self) -> dict[str, str]:
        response = self.client.post(
            "/api/admin/auth/login",
            json={"username": "admin", "password": ADMIN_PASSWORD},
        )
        assert response.status_code == 200, response.text
        return {"Authorization": f"Bearer {response.json()['token']}"}

    def open_window(
        self, local_date: str = "2099-01-01", region: str = "asia", **extra: object
    ) -> dict[str, Any]:
        response = self.client.post(
            "/api/admin/posting-window",
            json={"region": region, "localDate": local_date, **extra},
            headers=self.admin(),
        )
        assert response.status_code == 201, response.text
        body: dict[str, Any] = response.json()
        return body

    def post(
        self, headers: dict[str, str], visibility: str = "public", **fields: str
    ) -> dict[str, Any]:
        response = self.client.post(
            "/api/posts",
            files={"image": ("photo.jpg", JPEG, "image/jpeg")},
            data={"visibility": visibility, "location_kind": "none", **fields},
            headers=headers,
        )
        assert response.status_code == 201, response.text
        body: dict[str, Any] = response.json()
        return body

    def befriend(
        self, a: tuple[str, dict[str, str]], b: tuple[str, dict[str, str]]
    ) -> None:
        request = self.client.post(
            "/api/friend-requests", json={"recipientId": b[0]}, headers=a[1]
        )
        assert request.status_code == 201, request.text
        accept = self.client.post(
            f"/api/friend-requests/{request.json()['relationId']}/accept", headers=b[1]
        )
        assert accept.status_code == 200, accept.text


def make_jpeg(width: int = 2, height: int = 2, **options: Any) -> bytes:
    data = io.BytesIO()
    Image.new("RGB", (width, height)).save(data, "JPEG", **options)
    return data.getvalue()


JPEG = make_jpeg()


@pytest.fixture
def actors(client: TestClient, db: psycopg.Connection) -> Actors:
    return Actors(client, db)
