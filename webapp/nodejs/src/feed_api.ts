import type { Handler } from "hono";
import { APIError } from "./api_error.ts";
import { bind } from "./bind.ts";
import { canView, loadViewerContext, selectPostsWithAuthor, type Post, type ViewerContext } from "./post.ts";
import { newPostResponse, type PostResponse } from "./post_api.ts";
import { jsonResponse } from "./response.ts";
import type { Env, HandlerContext } from "./server.ts";
import { getSession } from "./session.ts";

const getFeedRequest = {
  feed: { param: "feed" },
  beforePostedAt: { query: "beforePostedAt", type: "time" },
  beforeId: { query: "beforeId" },
} as const;

type GetFeedResponse = {
  items: PostResponse[];
  hasMore: boolean;
};

export function getFeedHandler(h: HandlerContext): Handler<Env> {
  return async (c) => {
    const input = await bind(c, getFeedRequest);
    switch (input.feed) {
      case "friends":
      case "friends-of-friends":
      case "public":
      case "following":
        break;
      default:
        throw new APIError(400, "invalid_post");
    }
    if ((input.beforePostedAt === null) !== (input.beforeId === "")) {
      throw new APIError(400, "invalid_pagination");
    }
    const viewer = await loadViewerContext(h.db, getSession(c)!.userID);
    if (!viewer) {
      const response: GetFeedResponse = { items: [], hasMore: false };
      return jsonResponse(c, 200, response);
    }
    const following = new Set<string>();
    if (input.feed === "following") {
      const followees = await h.db.select<{ followee_id: string }>(`SELECT followee_id FROM follows WHERE follower_id = $1`, viewer.user.id);
      for (const followee of followees) {
        following.add(followee.followee_id);
      }
    }
    const posts = await selectPostsWithAuthor(
      h.db,
      `
   AND p.author_id <> $1 AND w.ended_at IS NULL
   AND ($2::timestamptz IS NULL OR (p.posted_at, p.id) < ($2, $3))
   ORDER BY p.posted_at DESC, p.id DESC
  `,
      viewer.user.id,
      input.beforePostedAt,
      input.beforeId,
    );
    let items: PostResponse[] = [];
    for (const post of posts) {
      if (!canView(viewer, post.post) || !matchesFeed(viewer, following, post.post, input.feed)) {
        continue;
      }
      items.push(newPostResponse(post.post, post.author));
      if (items.length === 51) {
        break;
      }
    }
    const hasMore = items.length > 50;
    if (hasMore) {
      items = items.slice(0, 50);
    }
    const response: GetFeedResponse = { items, hasMore };
    return jsonResponse(c, 200, response);
  };
}

function matchesFeed(viewer: ViewerContext, following: Set<string>, post: Post, feed: string): boolean {
  switch (feed) {
    case "public":
      return post.visibility === "public";
    case "following":
      return following.has(post.authorID);
    case "friends":
      return viewer.friends.has(post.authorID);
    case "friends-of-friends":
      return !viewer.friends.has(post.authorID) && viewer.friendsOfFriends.has(post.authorID);
    default:
      return false;
  }
}
