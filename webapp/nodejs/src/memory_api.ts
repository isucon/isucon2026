import type pg from "pg";
import { zipSync, strToU8, type Zippable } from "fflate";
import { type C, cursor } from "./http.js";
import { fail, validDate, postResponse, page, type Post } from "./core.js";
import { must, rows } from "./db.js";
import {
  selectPostsWithAuthor,
  placeholders,
  getUser,
  postColumns,
} from "./models.js";
import {
  selectCurrentRealMojis,
  selectCommentsWithAuthor,
  realMojiResponse,
  commentResponse,
} from "./interaction_api.js";
export function memoryHandlers(db: pg.Pool) {
  return {
    list: async (c: C) => {
      const day = c.req.param("date"),
        period = day ?? c.req.query("month") ?? "";
      if (!validDate(period, day === undefined))
        fail(400, "invalid_memory_period");
      const start =
          (day === undefined ? period + "-01" : period) + "T00:00:00Z",
        end = new Date(start);
      if (day === undefined) end.setUTCMonth(end.getUTCMonth() + 1);
      else end.setUTCDate(end.getUTCDate() + 1);
      const { at, id } = cursor(c, "beforePostedAt", "beforeId");
      const posts = await selectPostsWithAuthor(
        db,
        `AND p.author_id = $1 AND w.local_date >= $2 AND w.local_date < $3 AND ($4::timestamptz IS NULL OR (p.posted_at, p.id) < ($4, $5)) ORDER BY p.posted_at DESC, p.id DESC LIMIT 51`,
        c.var.session.user_id,
        start,
        end.toISOString(),
        at,
        id,
      );
      const result = page(
        posts.map((p) => ({
          ...postResponse(p, p.author),
          realmojis: [] as ReturnType<typeof realMojiResponse>[],
          comments: [] as ReturnType<typeof commentResponse>[],
        })),
      );
      if (result.items.length) {
        const ids = result.items.map((i) => i.id),
          byID = new Map(result.items.map((i) => [i.id, i]));
        const reactions = await selectCurrentRealMojis(
          db,
          `AND r.post_id IN (${placeholders(ids)}) ORDER BY r.created_at DESC, r.id DESC`,
          ...ids,
        );
        for (const r of reactions) {
          const item = byID.get(r.post_id)!;
          if (item.realmojis.length < 50)
            item.realmojis.push(realMojiResponse(r, r.reactor));
        }
        const comments = await selectCommentsWithAuthor(
          db,
          `AND c.post_id IN (${placeholders(ids)}) ORDER BY c.created_at, c.id`,
          ...ids,
        );
        for (const r of comments) {
          const item = byID.get(r.post_id)!;
          if (item.comments.length < 50)
            item.comments.push(commentResponse(r, r.author));
        }
      }
      return c.json(result);
    },
    export: async (c: C) => {
      const userID = c.var.session.user_id,
        author = must(await getUser(db, userID));
      const posts = await rows<Post & { image_data: Buffer }>(
        db,
        `SELECT ${postColumns}, i.data AS image_data FROM posts p JOIN posting_windows w ON w.id = p.posting_window_id JOIN image_objects i ON i.id = p.image_id WHERE p.author_id = $1 AND p.deleted_at IS NULL ORDER BY p.posted_at, p.id`,
        userID,
      );
      const files: Zippable = {},
        items = [];
      for (const post of posts) {
        const imagePath: string = `images/${String(items.length + 1).padStart(6, "0")}.jpg`;
        files[imagePath] = [post.image_data, { level: 0 }];
        items.push({ ...postResponse(post, author), imagePath });
      }
      files["posts.json"] = strToU8(JSON.stringify(items) + "\n");
      return new Response(zipSync(files), {
        headers: {
          "Content-Disposition": 'attachment; filename="isureal-posts.zip"',
          "Cache-Control": "no-store",
          "Content-Type": "application/zip",
        },
      });
    },
  };
}
