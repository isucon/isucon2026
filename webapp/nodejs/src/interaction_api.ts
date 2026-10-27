import { randomUUID } from "node:crypto";
import type pg from "pg";
import { readJSON, cursor } from "./bind.js";
import type { C } from "./types/hono.js";
import {
  fail,
  validUserText,
  relationshipUserResponse,
  signedImageURL,
  page,
  type User,
  noContent,
} from "./core.js";
import { type DB, exec, first, must, one, rows, transaction } from "./db.js";
import {
  getPost,
  getUser,
  canComment,
  canViewPost,
  userColumnsAs,
  joinedUser,
} from "./models.js";
import { relationshipTx } from "./relationship_api.js";
import { multipart } from "./post_api.js";
import { decodeJPEG } from "./jpeg.js";
export interface Comment {
  id: string;
  post_id: string;
  author_id: string;
  body: string;
  created_at: string;
  deleted_at: string | null;
  author: User;
}
export interface RealMoji {
  id: string;
  post_id: string;
  reactor_id: string;
  image_id: string;
  reaction_type: string;
  created_at: string;
  replaced_at: string | null;
  deleted_at: string | null;
  reactor: User;
}
const getComment = (db: DB, id: string) =>
  one<Comment>(
    db,
    "SELECT id,post_id,author_id,body,created_at,deleted_at FROM comments WHERE id=$1",
    id,
  );
const getRealMoji = (db: DB, id: string) =>
  one<RealMoji>(
    db,
    "SELECT id, post_id, reactor_id, image_id, reaction_type, created_at, replaced_at, deleted_at FROM realmojis WHERE id=$1",
    id,
  );
export const commentResponse = (r: Comment, u: User) => ({
  id: r.id,
  author: relationshipUserResponse(u),
  body: r.body,
  createdAt: r.created_at,
});
export const realMojiResponse = (r: RealMoji, u: User) => ({
  id: r.id,
  reactor: relationshipUserResponse(u),
  reactionType: r.reaction_type,
  imageUrl: signedImageURL(u, r.image_id),
  createdAt: r.created_at,
});
export async function selectCommentsWithAuthor(
  db: DB,
  conditions: string,
  ...args: unknown[]
) {
  const records = await rows<Comment>(
    db,
    `SELECT c.id, c.post_id, c.author_id, c.body, c.created_at, c.deleted_at, ${userColumnsAs("u", "author")} FROM comments c JOIN users u ON u.id = c.author_id AND u.status = 'active' WHERE c.deleted_at IS NULL ${conditions}`,
    ...args,
  );
  for (const r of records) r.author = joinedUser(r, "author");
  return records;
}
export async function selectCurrentRealMojis(
  db: DB,
  conditions: string,
  ...args: unknown[]
) {
  const records = await rows<RealMoji>(
    db,
    `SELECT r.id, r.post_id, r.reactor_id, r.image_id, r.reaction_type, r.created_at, r.replaced_at, r.deleted_at, ${userColumnsAs("u", "reactor")} FROM realmojis r JOIN users u ON u.id = r.reactor_id AND u.status = 'active' WHERE r.replaced_at IS NULL AND r.deleted_at IS NULL ${conditions}`,
    ...args,
  );
  for (const r of records) r.reactor = joinedUser(r, "reactor");
  return records;
}
export function interactionHandlers(db: pg.Pool) {
  return {
    createComment: async (c: C) => {
      const { body: text = "" } = await readJSON(c, { body: "string" });
      if (!validUserText(text, 1, 1000)) fail(400, "invalid_interaction");
      const actor = c.var.session.user_id;
      const post =
        (await getPost(db, c.req.param("postId")!)) ??
        fail(403, "interaction_forbidden");
      const result = await transaction(db, async (tx) => {
        for (const id of new Set([actor, post.author_id].sort()))
          await one(
            tx,
            "SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE",
            id,
          );
        if (!(await canComment(tx, actor, post.id)))
          fail(403, "interaction_forbidden");
        const id = randomUUID();
        await exec(
          tx,
          "INSERT INTO comments(id,post_id,author_id,body,created_at) VALUES($1,$2,$3,$4,clock_timestamp())",
          id,
          post.id,
          actor,
          text,
        );
        return {
          comment: await getComment(tx, id),
          user: must(await getUser(tx, actor)),
        };
      });
      return c.json(commentResponse(result.comment, result.user), 201);
    },
    getComments: async (c: C) => {
      const { at, id } = cursor(c, "afterCreatedAt", "afterId"),
        postID = c.req.param("postId")!;
      if (!(await canComment(db, c.var.session.user_id, postID)))
        fail(403, "interaction_forbidden");
      const comments = await selectCommentsWithAuthor(
        db,
        "AND c.post_id=$1 AND ($2::timestamptz IS NULL OR (c.created_at,c.id)>($2,$3)) ORDER BY c.created_at,c.id LIMIT 51",
        postID,
        at,
        id,
      );
      return c.json(page(comments.map((r) => commentResponse(r, r.author))));
    },
    deleteComment: async (c: C) => {
      const actor = c.var.session.user_id,
        id = c.req.param("commentId")!;
      await transaction(db, async (tx) => {
        await one(
          tx,
          "SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE",
          actor,
        );
        const record =
          (await first<{ author_id: string; post_author_id: string }>(
            tx,
            "SELECT c.author_id,p.author_id AS post_author_id FROM comments c JOIN posts p ON p.id=c.post_id WHERE c.id=$1 AND c.deleted_at IS NULL FOR UPDATE OF c",
            id,
          )) ?? fail(404, "interaction_not_found");
        if (actor !== record.author_id && actor !== record.post_author_id)
          fail(403, "interaction_forbidden");
        await exec(
          tx,
          "UPDATE comments SET deleted_at=clock_timestamp() WHERE id=$1",
          id,
        );
      });
      return noContent();
    },
    replaceRealMoji: async (c: C) => {
      const form = await multipart(c),
        image = form.get("image"),
        reactionType = form.get("reactionType");
      if (
        !(image instanceof File) ||
        typeof reactionType !== "string" ||
        !["like", "smile", "surprise", "heart", "laugh", "instant"].includes(
          reactionType,
        )
      )
        return fail(400, "invalid_realmoji");
      const data = Buffer.from(await image.arrayBuffer());
      const img = decodeJPEG(data) ?? fail(400, "invalid_realmoji");
      if (img.width > 512 || img.height > 512) fail(400, "invalid_realmoji");
      const actor = c.var.session.user_id,
        post =
          (await getPost(db, c.req.param("postId")!)) ??
          fail(403, "interaction_forbidden");
      if (actor === post.author_id) fail(403, "interaction_forbidden");
      const result = await relationshipTx(
        db,
        actor,
        post.author_id,
        async (tx) => {
          if (!(await canViewPost(tx, actor, post.id)))
            fail(403, "interaction_forbidden");
          await exec(
            tx,
            "UPDATE realmojis SET replaced_at=clock_timestamp() WHERE post_id=$1 AND reactor_id=$2 AND replaced_at IS NULL AND deleted_at IS NULL",
            post.id,
            actor,
          );
          const imageID = randomUUID();
          await exec(
            tx,
            "INSERT INTO image_objects(id,owner_user_id,data,media_type) VALUES($1,$2,$3,'image/jpeg')",
            imageID,
            actor,
            data,
          );
          const id = randomUUID();
          await exec(
            tx,
            "INSERT INTO realmojis(id,post_id,reactor_id,image_id,reaction_type,created_at) VALUES($1,$2,$3,$4,$5,clock_timestamp())",
            id,
            post.id,
            actor,
            imageID,
            reactionType,
          );
          return {
            reaction: await getRealMoji(tx, id),
            user: must(await getUser(tx, actor)),
          };
        },
      );
      return c.json(realMojiResponse(result.reaction, result.user), 201);
    },
    getRealMojis: async (c: C) => {
      const { at, id } = cursor(c, "beforeCreatedAt", "beforeId"),
        postID = c.req.param("postId")!;
      if (!(await canViewPost(db, c.var.session.user_id, postID)))
        fail(403, "interaction_forbidden");
      const records = await selectCurrentRealMojis(
        db,
        "AND r.post_id=$1 AND ($2::timestamptz IS NULL OR (r.created_at,r.id)<($2,$3)) ORDER BY r.created_at DESC,r.id DESC LIMIT 51",
        postID,
        at,
        id,
      );
      return c.json(page(records.map((r) => realMojiResponse(r, r.reactor))));
    },
    deleteRealMoji: async (c: C) => {
      const actor = c.var.session.user_id,
        id = c.req.param("realMojiId")!;
      await transaction(db, async (tx) => {
        await one(
          tx,
          "SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE",
          actor,
        );
        const record =
          (await first<{ reactor_id: string; author_id: string }>(
            tx,
            "SELECT r.reactor_id,p.author_id FROM realmojis r JOIN posts p ON p.id=r.post_id WHERE r.id=$1 AND r.deleted_at IS NULL FOR UPDATE OF r",
            id,
          )) ?? fail(404, "interaction_not_found");
        if (actor !== record.reactor_id && actor !== record.author_id)
          fail(403, "interaction_forbidden");
        await exec(
          tx,
          "UPDATE realmojis SET deleted_at=clock_timestamp() WHERE id=$1",
          id,
        );
      });
      return noContent();
    },
  };
}
