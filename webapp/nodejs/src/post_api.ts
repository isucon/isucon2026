import { randomUUID } from "node:crypto";
import type pg from "pg";
import { body, string, cursor, type C } from "./http.js";
import {
  fail,
  validUserText,
  validLocation,
  postResponse,
  canView,
  page,
  micros,
  type Location,
} from "./core.js";
import {
  exec,
  one,
  rows,
  scalar,
  transaction,
  notFound,
  NoRows,
} from "./db.js";
import {
  getUser,
  getPost,
  getPostWithAuthor,
  getPostingWindow,
  loadViewerContext,
  selectPostsWithAuthor,
  windowResponse,
  type PostingWindow,
} from "./models.js";
const validVisibility = (v: string) =>
  ["friends", "friends_of_friends", "public"].includes(v);
export async function multipart(c: C) {
  try {
    return await c.req.raw.formData();
  } catch {
    return fail(400, "invalid multipart form");
  }
}
function field(form: FormData, name: string) {
  const value = form.get(name);
  return typeof value === "string" ? value : "";
}
function coordinate(form: FormData, name: string) {
  const raw = form.get(name);
  if (raw === null) return null;
  const value = raw === "" ? 0 : Number(raw);
  if (typeof raw !== "string" || Number.isNaN(value))
    fail(400, "invalid coordinate");
  return value;
}
export function postHandlers(db: pg.Pool) {
  return {
    create: async (c: C) => {
      const form = await multipart(c),
        image = form.get("image"),
        caption = field(form, "caption"),
        visibility = field(form, "visibility");
      if (!(image instanceof File) || !validUserText(caption, 0, 1000))
        return fail(400, "invalid_post");
      if (!validVisibility(visibility)) fail(400, "invalid_post");
      const location: Location = {
        kind: field(form, "location_kind"),
        latitude: coordinate(form, "latitude"),
        longitude: coordinate(form, "longitude"),
        locality: form.has("locality") ? field(form, "locality") : null,
      };
      if (!validLocation(location)) fail(400, "invalid_post");
      const data = Buffer.from(await image.arrayBuffer()),
        userID = c.var.session.user_id;
      const result = await transaction(db, async (tx) => {
        await one(
          tx,
          "SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE",
          userID,
        );
        const user = await getUser(tx, userID);
        const windowQuery =
          "SELECT id, local_date, starts_at FROM posting_windows WHERE region = $1 AND ended_at IS NULL FOR UPDATE";
        let window: PostingWindow;
        try {
          window = await one(tx, windowQuery, user.region);
        } catch (e) {
          if (!(e instanceof NoRows)) throw e;
          window = await notFound(
            () => one<PostingWindow>(tx, windowQuery, user.region),
            409,
            "post_slot_unavailable",
          );
        }
        const counts = await one<{ active: string; deleted: string }>(
          tx,
          `SELECT count(*) FILTER (WHERE deleted_at IS NULL) AS active, count(*) FILTER (WHERE deleted_at IS NOT NULL) AS deleted FROM posts p JOIN posting_windows w ON w.id = p.posting_window_id WHERE p.author_id = $1 AND w.local_date = $2`,
          userID,
          window.local_date,
        );
        const postedAt = new Date().toISOString();
        if (
          Number(counts.active) !== 0 ||
          Number(counts.deleted) >= 2 ||
          micros(postedAt) < micros(window.starts_at)
        )
          fail(409, "post_slot_unavailable");
        const imageID = randomUUID();
        await exec(
          tx,
          "INSERT INTO image_objects (id, owner_user_id, data, media_type, created_at) VALUES ($1, $2, $3, 'image/jpeg', $4)",
          imageID,
          userID,
          data,
          postedAt,
        );
        const postID = randomUUID();
        await exec(
          tx,
          `INSERT INTO posts (id, author_id, posting_window_id, image_id, caption, visibility, location_kind, latitude, longitude, locality, posted_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
          postID,
          userID,
          window.id,
          imageID,
          caption,
          visibility,
          location.kind,
          location.latitude,
          location.longitude,
          location.locality,
          postedAt,
        );
        return { post: await getPost(tx, postID), user };
      });
      return c.json(postResponse(result.post, result.user), 201);
    },
    current: async (c: C) => {
      const id = await notFound(
        () =>
          scalar<string>(
            db,
            `SELECT p.id FROM posts p JOIN posting_windows w ON w.id = p.posting_window_id JOIN users u ON u.id = p.author_id JOIN posting_windows current ON current.region = u.region AND current.ended_at IS NULL AND current.local_date = w.local_date WHERE p.author_id = $1 AND p.deleted_at IS NULL`,
            c.var.session.user_id,
          ),
        404,
        "post_not_found",
      );
      const post = await notFound(() => getPost(db, id), 404, "post_not_found"),
        user = await getUser(db, c.var.session.user_id);
      return c.json(postResponse(post, user));
    },
    get: async (c: C) => {
      const post = await notFound(
        () => getPostWithAuthor(db, c.req.param("postId")!),
        404,
        "post_not_found",
      );
      const viewer = await notFound(
        () => loadViewerContext(db, c.var.session.user_id),
        404,
        "post_not_found",
      );
      if (!canView(viewer, post)) fail(404, "post_not_found");
      return c.json(postResponse(post, post.author));
    },
    update: async (c: C) => {
      const b = await body(c, { caption: "string", visibility: "string" });
      if (
        (b.caption == null && b.visibility == null) ||
        (b.caption != null && !validUserText(string(b, "caption"), 0, 1000)) ||
        (b.visibility != null && !validVisibility(string(b, "visibility")))
      )
        fail(400, "invalid_post");
      const userID = c.var.session.user_id;
      const result = await transaction(db, async (tx) => {
        await one(
          tx,
          "SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE",
          userID,
        );
        const post = await notFound(
          () => getPost(tx, c.req.param("postId")!),
          404,
          "post_not_found",
        );
        if (post.author_id !== userID) fail(403, "post_not_editable");
        const window = await getPostingWindow(tx, post.posting_window_id);
        if (window.ended_at != null) fail(403, "post_not_editable");
        if (b.caption != null)
          await exec(
            tx,
            "UPDATE posts SET caption = $2 WHERE id = $1",
            post.id,
            b.caption,
          );
        if (b.visibility != null)
          await exec(
            tx,
            "UPDATE posts SET visibility = $2 WHERE id = $1",
            post.id,
            b.visibility,
          );
        return {
          post: await getPost(tx, post.id),
          user: await getUser(tx, userID),
        };
      });
      return c.json(postResponse(result.post, result.user));
    },
    delete: async (c: C) => {
      const userID = c.var.session.user_id;
      await transaction(db, async (tx) => {
        await one(
          tx,
          "SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE",
          userID,
        );
        const post = await notFound(
          () => getPost(tx, c.req.param("postId")!),
          404,
          "post_not_found",
        );
        if (post.author_id !== userID) fail(403, "post_not_editable");
        const window = await getPostingWindow(tx, post.posting_window_id);
        const deletions = await scalar<string>(
          tx,
          "SELECT count(*) FROM posts p JOIN posting_windows w ON w.id = p.posting_window_id WHERE p.author_id = $1 AND w.local_date = $2 AND p.deleted_at IS NOT NULL",
          userID,
          window.local_date,
        );
        if (Number(deletions) >= 2) fail(409, "post_slot_unavailable");
        await exec(
          tx,
          "UPDATE posts SET deleted_at = clock_timestamp() WHERE id = $1",
          post.id,
        );
      });
      return c.body(null, 204);
    },
    window: async (c: C) => {
      let state: PostingWindow & { active: string; deleted: string };
      try {
        state = await one(
          db,
          `SELECT w.id, w.region, w.local_date, w.starts_at, w.on_time_deadline, w.ended_at, count(p.id) FILTER (WHERE p.deleted_at IS NULL) AS active, count(p.id) FILTER (WHERE p.deleted_at IS NOT NULL) AS deleted FROM users u JOIN posting_windows w ON w.region = u.region AND w.ended_at IS NULL LEFT JOIN posting_windows posted_window ON posted_window.local_date = w.local_date LEFT JOIN posts p ON p.author_id = u.id AND p.posting_window_id = posted_window.id WHERE u.id = $1 GROUP BY w.id`,
          c.var.session.user_id,
        );
      } catch (e) {
        if (e instanceof NoRows)
          return c.json({
            window: null,
            hasPosted: false,
            canPost: false,
            deletionCount: 0,
          });
        throw e;
      }
      return c.json({
        window: windowResponse(state),
        hasPosted: Number(state.active) > 0,
        canPost:
          Number(state.active) === 0 &&
          Number(state.deleted) < 2 &&
          micros(new Date().toISOString()) >= micros(state.starts_at),
        deletionCount: Number(state.deleted),
      });
    },
    feed: async (c: C) => {
      const feed = c.req.param("feed")!;
      if (
        !["friends", "friends-of-friends", "public", "following"].includes(feed)
      )
        fail(400, "invalid_post");
      const [at, id] = cursor(c, "beforePostedAt", "beforeId");
      let viewer;
      try {
        viewer = await loadViewerContext(db, c.var.session.user_id);
      } catch (e) {
        if (e instanceof NoRows) return c.json({ items: [], hasMore: false });
        throw e;
      }
      const following = new Set<string>();
      if (feed === "following")
        for (const r of await rows<{ followee_id: string }>(
          db,
          "SELECT followee_id FROM follows WHERE follower_id = $1",
          viewer.id,
        ))
          following.add(r.followee_id);
      const posts = await selectPostsWithAuthor(
        db,
        `AND p.author_id <> $1 AND w.ended_at IS NULL AND ($2::timestamptz IS NULL OR (p.posted_at, p.id) < ($2, $3)) ORDER BY p.posted_at DESC, p.id DESC`,
        viewer.id,
        at,
        id,
      );
      const items = [];
      for (const p of posts) {
        if (!canView(viewer, p)) continue;
        const matches =
          feed === "public"
            ? p.visibility === "public"
            : feed === "following"
              ? following.has(p.author_id)
              : feed === "friends"
                ? viewer.friends.has(p.author_id)
                : !viewer.friends.has(p.author_id) &&
                  viewer.friendsOfFriends.has(p.author_id);
        if (!matches) continue;
        items.push(postResponse(p, p.author));
        if (items.length === 51) break;
      }
      return c.json(page(items));
    },
  };
}
