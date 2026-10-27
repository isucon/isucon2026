from http import HTTPStatus
from typing import Annotated

from fastapi import APIRouter, Query

from ..auth import RegionalSession
from ..binding import cursor_time
from ..db import DB, fetch_all
from ..errors import APIError
from ..models.post import Post, Viewer, load_viewer, select_posts_with_author
from ..schemas import Page, PostResponse

router = APIRouter(prefix="/api/feeds")

FEEDS = frozenset({"friends", "friends-of-friends", "public", "following"})


def matches_feed(viewer: Viewer, following: set[str], post: Post, feed: str) -> bool:
    match feed:
        case "public":
            return post.visibility == "public"
        case "following":
            return post.author_id in following
        case "friends":
            return post.author_id in viewer.friends
        case "friends-of-friends":
            return (
                post.author_id not in viewer.friends
                and post.author_id in viewer.friends_of_friends
            )
        case _:
            return False


@router.get("/{feed}")
async def get_feed(
    db: DB,
    session: RegionalSession,
    feed: str,
    before_posted_at: Annotated[str | None, Query(alias="beforePostedAt")] = None,
    before_id: Annotated[str, Query(alias="beforeId")] = "",
) -> Page[PostResponse]:
    before_at = cursor_time(before_posted_at)
    if feed not in FEEDS:
        raise APIError(HTTPStatus.BAD_REQUEST, "invalid_post")
    if (before_at is None) != (before_id == ""):
        raise APIError(HTTPStatus.BAD_REQUEST, "invalid_pagination")
    viewer = await load_viewer(db, session.user_id)
    if viewer is None:
        return Page.of([])
    following: set[str] = set()
    if feed == "following":
        rows = await fetch_all(
            db,
            "SELECT followee_id FROM follows WHERE follower_id = %(viewer)s",
            {"viewer": viewer.id},
        )
        following = {row["followee_id"] for row in rows}
    posts = await select_posts_with_author(
        db,
        """
        AND p.author_id <> %(viewer)s AND w.ended_at IS NULL
        AND (%(before_at)s::timestamptz IS NULL OR (p.posted_at, p.id) < (%(before_at)s, %(before_id)s))
        ORDER BY p.posted_at DESC, p.id DESC
        """,
        {"viewer": viewer.id, "before_at": before_at, "before_id": before_id},
    )
    items: list[PostResponse] = []
    for post, author in posts:
        if not viewer.can_view(post) or not matches_feed(viewer, following, post, feed):
            continue
        items.append(PostResponse.of(post, author))
        if len(items) > 50:
            break
    return Page.of(items)
