import { randomUUID, timingSafeEqual } from "node:crypto";
import type pg from "pg";
import { type C } from "./http.js";
import { fail, imageSigningKey, imageSignature } from "./core.js";
import { exec, one, scalar, transaction, notFound } from "./db.js";
export function imageHandlers(db: pg.Pool) {
  return {
    get: async (c: C) => {
      c.header("Cache-Control", "no-store");
      const rawQuery = c.req.url.split("?")[1] ?? "",
        query = new URL(c.req.url).searchParams,
        id = c.req.param("imageId")!;
      if (
        /%(?![0-9a-fA-F]{2})/.test(rawQuery) ||
        rawQuery.includes(";") ||
        query.getAll("exp").length !== 1 ||
        query.getAll("sig").length !== 1
      )
        fail(403, "image_forbidden");
      const rawExp = query.get("exp")!,
        rawSig = query.get("sig")!;
      if (!/^-?(?:0|[1-9][0-9]*)$/.test(rawExp)) fail(403, "image_forbidden");
      const exp = BigInt(rawExp);
      if (
        exp.toString() !== rawExp ||
        exp > 9223372036854775807n ||
        exp <= BigInt(Math.floor(Date.now() / 1000))
      )
        fail(403, "image_forbidden");
      const sig = Buffer.from(rawSig, "base64url");
      if (sig.length !== 32 || sig.toString("base64url") !== rawSig)
        fail(403, "image_forbidden");
      const secret = await notFound(
        () =>
          scalar<string>(
            db,
            `SELECT u.secret_key_hash FROM image_objects i JOIN users u ON u.id = i.owner_user_id WHERE i.id = $1 AND u.status = 'active'`,
            id,
          ),
        403,
        "image_forbidden",
      );
      if (
        !timingSafeEqual(sig, imageSignature(imageSigningKey(secret), id, exp))
      )
        fail(403, "image_forbidden");
      const object = await notFound(
        () =>
          one<{ data: Buffer; media_type: string }>(
            db,
            `SELECT i.data, i.media_type FROM image_objects i JOIN users u ON u.id = i.owner_user_id AND u.status = 'active' WHERE i.id = $1 AND (u.profile_image_id = i.id OR EXISTS (SELECT 1 FROM posts p WHERE p.image_id = i.id AND p.author_id = u.id AND p.deleted_at IS NULL) OR EXISTS (SELECT 1 FROM realmojis r JOIN posts p ON p.id = r.post_id AND p.deleted_at IS NULL JOIN users author ON author.id = p.author_id AND author.status = 'active' WHERE r.image_id = i.id AND r.reactor_id = u.id AND r.replaced_at IS NULL AND r.deleted_at IS NULL))`,
            id,
          ),
        403,
        "image_forbidden",
      );
      if (exp <= BigInt(Math.floor(Date.now() / 1000)))
        fail(403, "image_forbidden");
      c.header("Content-Type", object.media_type);
      return c.body(new Uint8Array(object.data));
    },
    putProfile: async (c: C) => {
      if (c.req.header("Content-Type") !== "image/jpeg")
        fail(415, "unsupported_media_type");
      const data = Buffer.from(await c.req.arrayBuffer()),
        id = c.var.session.user_id;
      await transaction(db, async (tx) => {
        const oldID = await notFound(
          () =>
            scalar<string | null>(
              tx,
              "SELECT profile_image_id FROM users WHERE id=$1 FOR NO KEY UPDATE",
              id,
            ),
          404,
          "account_not_found",
        );
        const imageID = randomUUID();
        await exec(
          tx,
          "INSERT INTO image_objects(id,owner_user_id,data,media_type) VALUES ($1,$2,$3,'image/jpeg')",
          imageID,
          id,
          data,
        );
        await exec(
          tx,
          "UPDATE users SET profile_image_id=$2,updated_at=clock_timestamp() WHERE id=$1",
          id,
          imageID,
        );
        if (oldID != null)
          await exec(tx, "DELETE FROM image_objects WHERE id=$1", oldID);
      });
      return c.body(null, 204);
    },
    deleteProfile: async (c: C) => {
      const id = c.var.session.user_id;
      await transaction(db, async (tx) => {
        const imageID = await notFound(
          () =>
            scalar<string | null>(
              tx,
              "SELECT profile_image_id FROM users WHERE id=$1 FOR NO KEY UPDATE",
              id,
            ),
          404,
          "account_not_found",
        );
        if (imageID == null) fail(404, "account_not_found");
        await exec(
          tx,
          "UPDATE users SET profile_image_id=NULL,updated_at=clock_timestamp() WHERE id=$1",
          id,
        );
        await exec(tx, "DELETE FROM image_objects WHERE id=$1", imageID);
      });
      return c.body(null, 204);
    },
  };
}
