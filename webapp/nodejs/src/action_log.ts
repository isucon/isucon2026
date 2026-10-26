import { createMiddleware } from "hono/factory";
import { matchedRoutes } from "hono/route";
import { usernamePattern } from "./core.js";
import type { Env } from "./http.js";
import { extractClientIP } from "./ip_ban.js";

const actionNames: Record<string, string> = {
  "POST /api/initialize": "initialize",
  "POST /api/auth/register": "register",
  "POST /api/auth/login": "login",
  "POST /api/auth/logout": "logout",
  "GET /api/users": "search_users",
  "GET /api/users/:userId": "get_user",
  "GET /api/me/export": "export_posts",
  "GET /api/me": "get_me",
  "PATCH /api/me": "update_me",
  "PUT /api/me/profile-image": "put_profile_image",
  "DELETE /api/me/profile-image": "delete_profile_image",
  "POST /api/me/account-deletion": "delete_account",
  "POST /api/posts": "create_post",
  "GET /api/posting-window/current": "get_current_posting_window",
  "GET /api/posts/me/current": "get_my_current_post",
  "GET /api/posts/:postId": "get_post",
  "PATCH /api/posts/:postId": "update_post",
  "DELETE /api/posts/:postId": "delete_post",
  "GET /api/feeds/:feed": "get_feed",
  "GET /api/memories": "get_memories",
  "GET /api/memories/:date": "get_memories_by_date",
  "POST /api/posts/:postId/comments": "create_comment",
  "GET /api/posts/:postId/comments": "get_comments",
  "DELETE /api/comments/:commentId": "delete_comment",
  "POST /api/posts/:postId/realmojis": "replace_real_moji",
  "GET /api/posts/:postId/realmojis": "get_real_mojis",
  "DELETE /api/realmojis/:realMojiId": "delete_real_moji",
  "GET /api/images/:imageId": "get_image",
  "GET /api/notifications": "get_notifications",
  "POST /api/friend-requests": "create_friend_request",
  "POST /api/friend-requests/:requestId/:action": "transition_friend_request",
  "GET /api/relationships/:kind": "get_relationships",
  "DELETE /api/friendships/:userId": "delete_friendship",
  "PUT /api/follows/:userId": "follow",
  "DELETE /api/follows/:userId": "unfollow",
  "PUT /api/blocks/:userId": "block",
  "DELETE /api/blocks/:userId": "unblock",
  "POST /api/admin/auth/login": "admin_login",
  "POST /api/admin/auth/logout": "admin_logout",
  "PUT /api/admin/users/:userId/certification": "certify_user",
  "DELETE /api/admin/users/:userId/certification": "delete_user_certification",
  "POST /api/admin/posting-window": "open_posting_window",
};

const logLevelRanks: Record<string, number> = { debug: 0, info: 1, error: 2 };

export interface LogOutput {
  write(line: string): unknown;
}

// Writes one JSON line per request, like Go's ActionLogMiddleware.
export const actionLogMiddleware = (minLevel: string, output: LogOutput) =>
  createMiddleware<Env>(async (c, next) => {
    await next();
    const status = c.res.status;
    const level =
      status >= 500 ? "error" : c.req.method === "GET" ? "debug" : "info";
    if (logLevelRanks[level] < (logLevelRanks[minLevel] ?? 0)) return;
    // The route is known even when a middleware such as the IP ban rejected
    // the request before its handler ran, as with echo's RouteInfo.
    const route = matchedRoutes(c).findLast((r) => r.method !== "ALL");
    const action =
      (route && actionNames[`${route.method} ${route.path}`]) ?? null;
    // Keys are in the same (sorted) order as Go's encoding of a map.
    const entry: Record<string, unknown> = {
      action,
      ip: extractClientIP(c),
      level,
      status,
      user_id: c.var.session?.user_id ?? null,
    };
    if (action === "login" || action === "register") {
      const username = c.var.username;
      entry.username =
        username !== undefined && usernamePattern.test(username)
          ? username
          : null;
    }
    output.write(JSON.stringify(entry) + "\n");
  });
