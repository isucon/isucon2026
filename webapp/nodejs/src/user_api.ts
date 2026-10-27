import { randomUUID } from "node:crypto";
import type pg from "pg";
import { readJSON } from "./bind.js";
import { createLoginSessionTx } from "./session.js";
import type { C } from "./types/hono.js";
import {
  hash,
  fail,
  usernamePattern,
  validUserText,
  validRegion,
  sessionUserResponse,
  getMeResponse,
  relationshipUserResponse,
  normalizeSearchText,
  imageSigningKey,
  page,
  type User,
  noContent,
} from "./core.js";
import { first, must, one, scalar, exec, rows, transaction } from "./db.js";
import {
  getUser,
  userColumns,
  getBlockedUserIDs,
  getFriendIDs,
} from "./models.js";
import { isBlocked } from "./relationship_api.js";
export function userHandlers(db: pg.Pool) {
  return {
    register: async (c: C) => {
      const {
        username = "",
        displayName = "",
        region = "",
      } = await readJSON(c, {
        username: "string",
        displayName: "string",
        // [作問] 登録時は自動的に国からリージョンを決めるべきかも。新方針では各リージョン1台ずつになったので
        region: "string",
      });
      c.set("username", username);
      if (
        !usernamePattern.test(username) ||
        !validUserText(displayName, 1, 100) ||
        !validRegion(region)
      )
        fail(400, "invalid_registration");
      const secretKey = randomUUID();
      const user = await transaction(db, async (tx) => {
        // ユーザー名が一意である必要があります。
        await exec(tx, "LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE");
        if (
          await scalar(
            tx,
            "SELECT EXISTS(SELECT 1 FROM users WHERE username = $1)",
            username,
          )
        )
          fail(409, "username_taken");
        const id = randomUUID();
        await exec(
          tx,
          "INSERT INTO users (id, username, secret_key_hash, display_name, region) VALUES ($1, $2, $3, $4, $5)",
          id,
          username,
          hash(secretKey),
          displayName,
          region,
        );
        return must(await getUser(tx, id));
      });
      return c.json({ secretKey, user: sessionUserResponse(user) }, 201);
    },
    login: async (c: C) => {
      const { username = "", secretKey = "" } = await readJSON(c, {
        username: "string",
        secretKey: "string",
      });
      c.set("username", username);
      if (!usernamePattern.test(username)) fail(401, "invalid_credentials");
      const result = await transaction(db, async (tx) => {
        const user =
          (await first<User>(
            tx,
            "SELECT id, username, secret_key_hash, display_name, bio, profile_image_id, region, account_type, status FROM users WHERE username = $1 FOR NO KEY UPDATE",
            username,
          )) ?? fail(401, "invalid_credentials");
        if (
          user.status !== "active" ||
          user.secret_key_hash !== hash(secretKey)
        )
          fail(401, "invalid_credentials");
        const session = await createLoginSessionTx(tx, user.id);
        return { token: session.token, user: sessionUserResponse(user) };
      });
      return c.json(result);
    },
    logout: async (c: C) => {
      const s = c.var.session;
      await transaction(db, async (tx) => {
        await one(
          tx,
          "SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE",
          s.user_id,
        );
        await exec(
          tx,
          "UPDATE login_sessions SET active = false, invalidated_at = clock_timestamp(), reason = 'logout' WHERE user_id = $1 AND active = true AND token_hash = $2",
          s.user_id,
          s.token_hash,
        );
      });
      return noContent();
    },
    getMe: async (c: C) => {
      const user =
        (await getUser(db, c.var.session.user_id)) ??
        fail(404, "account_not_found");
      return c.json(getMeResponse(user));
    },
    updateMe: async (c: C) => {
      const b = await readJSON(c, {
        displayName: "string",
        bio: "string",
        region: "string",
      });
      if (
        (b.displayName == null && b.bio == null && b.region == null) ||
        (b.displayName != null && !validUserText(b.displayName, 1, 100)) ||
        (b.bio != null && !validUserText(b.bio, 0, 500)) ||
        (b.region != null && !validRegion(b.region))
      )
        fail(400, "invalid_profile");
      const user = await transaction(db, async (tx) => {
        // [作問] 退会後に更新されることがあるが、それを参照する手段がないので良しとし、仕様を定めない
        if (b.displayName != null)
          await exec(
            tx,
            "UPDATE users SET display_name = $2, updated_at = clock_timestamp() WHERE id = $1",
            c.var.session.user_id,
            b.displayName,
          );
        if (b.bio != null)
          await exec(
            tx,
            "UPDATE users SET bio = $2, updated_at = clock_timestamp() WHERE id = $1",
            c.var.session.user_id,
            b.bio,
          );
        if (b.region != null)
          await exec(
            tx,
            "UPDATE users SET region = $2, updated_at = clock_timestamp() WHERE id = $1",
            c.var.session.user_id,
            b.region,
          );
        return (
          (await getUser(tx, c.var.session.user_id)) ??
          fail(404, "account_not_found")
        );
      });
      return c.json(getMeResponse(user));
    },
    search: async (c: C) => {
      const q = c.req.query("q") ?? "",
        beforeUsername = c.req.query("beforeUsername") ?? "",
        beforeID = c.req.query("beforeId") ?? "";
      if (!validUserText(q, 1, 100)) fail(400, "invalid_relationship");
      if ((beforeUsername === "") !== (beforeID === ""))
        fail(400, "invalid_pagination");
      const users = await rows<User>(
        db,
        `SELECT ${userColumns.join(", ")} FROM users WHERE status='active' AND id<>$1 AND ($2='' OR (username,id)>($2,$3)) ORDER BY username, id`,
        c.var.session.user_id,
        beforeUsername,
        beforeID,
      );
      const blocked = await getBlockedUserIDs(db, c.var.session.user_id),
        query = normalizeSearchText(q),
        items = [];
      for (const user of users) {
        if (
          !normalizeSearchText(user.username).includes(query) &&
          !normalizeSearchText(user.display_name).includes(query)
        )
          continue;
        if (blocked.has(user.id)) continue;
        user.imageKey = imageSigningKey(user.secret_key_hash);
        if (user.account_type !== "certified_public") user.bio = "";
        items.push(relationshipUserResponse(user));
        if (items.length === 51) break;
      }
      return c.json(page(items));
    },
    getUser: async (c: C) => {
      const viewer = c.var.session.user_id,
        user =
          (await getUser(db, c.req.param("userId")!)) ??
          fail(404, "relationship_not_found");
      if (await isBlocked(db, viewer, user.id))
        fail(404, "relationship_not_found");
      const friends = await getFriendIDs(db, viewer);
      if (
        user.account_type !== "certified_public" &&
        !friends.includes(user.id)
      )
        user.bio = "";
      return c.json(relationshipUserResponse(user));
    },
    deleteAccount: async (c: C) => {
      const id = c.var.session.user_id;
      await transaction(db, async (tx) => {
        if (
          !(await first(
            tx,
            "SELECT id FROM users WHERE id=$1 AND status='active' FOR NO KEY UPDATE",
            id,
          ))
        )
          fail(404, "account_not_found");
        await exec(
          tx,
          "UPDATE users SET status='deleted', deleted_at=clock_timestamp(), updated_at=clock_timestamp() WHERE id=$1",
          id,
        );
        await exec(
          tx,
          "UPDATE login_sessions SET active=false, invalidated_at=clock_timestamp(), reason='account_deletion' WHERE user_id=$1 AND active=true",
          id,
        );
      });
      return noContent();
    },
  };
}
