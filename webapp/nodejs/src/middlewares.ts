import { createMiddleware } from "hono/factory";
import type pg from "pg";
import { fail, hash } from "./core.js";
import { first } from "./db.js";
import type { AdminSession, C, Env, Session } from "./types/hono.js";
const bearerToken = (c: C) => {
  const auth = c.req.header("Authorization");
  return auth?.startsWith("Bearer ") ? auth.slice(7) : undefined;
};
export const sessionMiddleware = (db: pg.Pool) =>
  createMiddleware<Env>(async (c, next) => {
    const token = bearerToken(c);
    const session =
      token &&
      (await first<Session>(
        db,
        `SELECT s.user_id, s.token_hash, s.created_at, u.region FROM login_sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = $1 AND s.active = true AND u.status = 'active'`,
        hash(token),
      ));
    c.set("session", session || fail(401, "invalid_session"));
    await next();
  });
export const adminSessionMiddleware = (db: pg.Pool) =>
  createMiddleware<Env>(async (c, next) => {
    const token = bearerToken(c);
    const session =
      token &&
      (await first<AdminSession>(
        db,
        "SELECT administrator_id, token_hash, created_at FROM administrator_sessions WHERE token_hash = $1 AND active = true",
        hash(token),
      ));
    c.set(
      "adminSession",
      session || fail(401, "invalid_administrator_session"),
    );
    await next();
  });
export const regionMiddleware = (region: string) =>
  createMiddleware<Env>(async (c, next) => {
    if (c.var.session.region !== region) fail(403, "region_mismatch");
    await next();
  });
