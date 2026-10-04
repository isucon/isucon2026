import { randomUUID } from "node:crypto";
import type pg from "pg";
import { type C, readJSON, noContent } from "./http.js";
import {
  fail,
  hash,
  usernamePattern,
  validRegion,
  validDate,
  addSeconds,
} from "./core.js";
import { exec, first, one, scalar, transaction } from "./db.js";
import { getPostingWindow, windowResponse } from "./models.js";
export function adminHandlers(db: pg.Pool) {
  return {
    login: async (c: C) => {
      const { username = "", password = "" } = await readJSON(c, {
        username: "string",
        password: "string",
      });
      if (!usernamePattern.test(username)) fail(401, "invalid_credentials");
      const admin =
        (await first<{ id: string; password_hash: string }>(
          db,
          "SELECT id, password_hash FROM administrators WHERE username = $1",
          username,
        )) ?? fail(401, "invalid_credentials");
      if (admin.password_hash !== hash(password))
        fail(401, "invalid_credentials");
      const token = await transaction(db, async (tx) => {
        const id = randomUUID(),
          token = randomUUID();
        await exec(
          tx,
          "INSERT INTO administrator_sessions (id, administrator_id, token_hash) VALUES ($1, $2, $3)",
          id,
          admin.id,
          hash(token),
        );
        await one(
          tx,
          "SELECT administrator_id, token_hash, created_at FROM administrator_sessions WHERE id = $1",
          id,
        );
        return token;
      });
      return c.json({ token, administratorId: admin.id });
    },
    logout: async (c: C) => {
      const s = c.var.adminSession;
      await exec(
        db,
        "UPDATE administrator_sessions SET active = false, invalidated_at = clock_timestamp() WHERE administrator_id = $1 AND token_hash = $2 AND active = true",
        s.administrator_id,
        s.token_hash,
      );
      return noContent();
    },
    certify: async (c: C) => {
      if (
        !(await exec(
          db,
          "UPDATE users SET account_type='certified_public', updated_at=clock_timestamp() WHERE id=$1 AND status='active' AND account_type='regular'",
          c.req.param("userId"),
        ))
      )
        fail(409, "certification_conflict");
      return noContent();
    },
    uncertify: async (_c: C) => fail(409, "certification_irreversible"),
    openWindow: async (c: C) => {
      const {
        region = "",
        localDate = "",
        onTimeSeconds = 86400,
      } = await readJSON(c, {
        region: "string",
        localDate: "string",
        onTimeSeconds: "number",
      });
      if (!validRegion(region) || !validDate(localDate))
        fail(400, "invalid_posting_window");
      const window = await transaction(db, async (tx) => {
        await exec(tx, "LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE");
        if (
          await scalar(
            tx,
            "SELECT EXISTS(SELECT 1 FROM posting_windows WHERE region = $1 AND local_date >= $2)",
            region,
            localDate,
          )
        )
          fail(409, "posting_window_conflict");
        const startsAt = await scalar<string>(tx, "SELECT clock_timestamp()");
        await exec(
          tx,
          "UPDATE posting_windows SET ended_at = $2 WHERE region = $1 AND ended_at IS NULL",
          region,
          startsAt,
        );
        const id = randomUUID();
        await exec(
          tx,
          "INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline) VALUES ($1, $2, $3, $4, $5)",
          id,
          region,
          localDate,
          startsAt,
          addSeconds(startsAt, onTimeSeconds),
        );
        await exec(
          tx,
          "INSERT INTO notifications (id, user_id, posting_window_id, created_at) SELECT gen_random_uuid()::text, id, $2, $3 FROM users WHERE region = $1 AND status = 'active'",
          region,
          id,
          startsAt,
        );
        return getPostingWindow(tx, id);
      });
      return c.json(windowResponse(window), 201);
    },
  };
}
