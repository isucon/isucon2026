import io
import json
import re
from typing import Annotated, Any

from fastapi import Depends, FastAPI, Request, Response
from fastapi.routing import APIRoute
from fastapi.testclient import TestClient

from isureal.action_log import ACTION_NAMES, ActionLogMiddleware, action_name
from isureal.binding import json_body
from isureal.errors import APIError
from isureal.main import api_error_handler, unexpected_error_handler
from isureal.models.session import Session
from isureal.routers import (
    admin,
    auth,
    comments,
    feeds,
    images,
    initialize,
    me,
    memories,
    notifications,
    posts,
    realmojis,
    relationships,
    users,
)
from isureal.routers.auth import LoginRequest


def new_server(min_level: str) -> tuple[TestClient, io.StringIO]:
    output = io.StringIO()
    app = FastAPI()
    app.add_exception_handler(APIError, api_error_handler)
    app.add_exception_handler(Exception, unexpected_error_handler)
    app.add_middleware(ActionLogMiddleware, min_level=min_level, output=output)

    @app.post("/api/auth/login")
    async def login(
        body: Annotated[LoginRequest, Depends(json_body(LoginRequest))],
        request: Request,
    ) -> None:
        request.state.username = body.username
        raise APIError(401, "invalid_credentials")

    @app.post("/api/posts")
    async def create_post(request: Request) -> Response:
        request.state.session = Session(user_id="user-1", token_hash="", region="asia")
        return Response(status_code=201)

    @app.get("/api/feeds/{feed}")
    async def get_feed(feed: str, request: Request) -> Response:
        request.state.session = Session(user_id="user-1", token_hash="", region="asia")
        return Response(status_code=200)

    @app.get("/api/users/{user_id}")
    async def get_user(user_id: str) -> None:
        raise RuntimeError("boom")

    @app.put("/api/me/profile-image")
    async def put_profile_image(request: Request) -> None:
        request.state.abuse = "invalid_image"
        raise APIError(400, "invalid_profile_image")

    @app.get("/api/images/{image_id}")
    async def get_image(image_id: str, request: Request) -> None:
        request.state.abuse = "invalid_signature"
        raise APIError(403, "image_forbidden")

    client = TestClient(app, raise_server_exceptions=False)
    client.headers["X-Client-IP"] = "203.0.113.7"
    return client, output


def lines(output: io.StringIO) -> list[dict[str, Any]]:
    return [json.loads(line) for line in output.getvalue().splitlines()]


def test_action_log_middleware() -> None:
    client, output = new_server("debug")
    client.post("/api/auth/login", json={"username": "alice", "secretKey": "wrong"})
    client.post("/api/auth/login", json={"username": "Not Valid", "secretKey": "wrong"})
    client.post("/api/posts", json={})
    client.get("/api/feeds/friends")
    client.get("/api/users/someone")
    client.get("/api/unknown")
    ip = "203.0.113.7"
    assert lines(output) == [
        {
            "level": "info",
            "action": "login",
            "status": 401,
            "ip": ip,
            "user_id": None,
            "username": "alice",
        },
        {
            "level": "info",
            "action": "login",
            "status": 401,
            "ip": ip,
            "user_id": None,
            "username": None,
        },
        {
            "level": "info",
            "action": "create_post",
            "status": 201,
            "ip": ip,
            "user_id": "user-1",
        },
        {
            "level": "debug",
            "action": "get_feed",
            "status": 200,
            "ip": ip,
            "user_id": "user-1",
        },
        {
            "level": "error",
            "action": "get_user",
            "status": 500,
            "ip": ip,
            "user_id": None,
        },
        {"level": "debug", "action": None, "status": 404, "ip": ip, "user_id": None},
    ]
    # キーの順序と区切りも Go 実装（encoding/json）と同じ 1 行の JSON にする
    assert output.getvalue().splitlines()[0] == (
        '{"action":"login","ip":"203.0.113.7","level":"info","status":401,'
        '"user_id":null,"username":"alice"}'
    )

    # info では読み取り（GET）の行を出さない
    client, output = new_server("info")
    client.get("/api/feeds/friends")
    client.post("/api/posts", json={})
    client.put("/api/me/profile-image")
    client.get("/api/images/image-id")
    assert [(entry["action"], entry.get("abuse"), entry["level"]) for entry in lines(output)] == [
        ("create_post", None, "info"),
        ("put_profile_image", "invalid_image", "info"),
        ("get_image", "invalid_signature", "info"),
    ]


def test_action_log_uses_peer_address_without_client_ip_header() -> None:
    client, output = new_server("debug")
    del client.headers["X-Client-IP"]
    client.get("/api/feeds/friends")
    client.get("/api/feeds/friends", headers={"X-Client-IP": ""})
    assert [entry["ip"] for entry in lines(output)] == ["testclient", "testclient"]


def test_every_route_has_an_action_name() -> None:
    # Go 実装と同じく、すべての API に行動名を付ける
    actions = set()
    for module in (
        initialize,
        auth,
        users,
        me,
        posts,
        feeds,
        memories,
        comments,
        realmojis,
        images,
        notifications,
        relationships,
        admin,
    ):
        for route in module.router.routes:
            assert isinstance(route, APIRoute)
            path = re.sub(r"\{\w+\}", "x", route.path)
            for method in route.methods or ():
                action = action_name({"method": method, "path": path})
                assert action is not None, (method, path)
                actions.add(action)
    assert actions == set(ACTION_NAMES.values())
