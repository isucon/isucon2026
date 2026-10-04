import type { Context } from "hono";
import { createMiddleware } from "hono/factory";
import type pg from "pg";
import { fail, hash } from "./core.js";
import { first } from "./db.js";
export interface Session {
  user_id: string;
  region: string;
  token_hash: string;
  created_at: string;
}
export interface AdminSession {
  administrator_id: string;
  token_hash: string;
  created_at: string;
}
export type Env = {
  Variables: { session: Session; adminSession: AdminSession };
};
export type C = Context<Env>;
type FieldType = "string" | "number";
type JSONBody<S extends Record<string, FieldType>> = {
  [K in keyof S]?: S[K] extends "string" ? string : number;
};
// Reads a JSON object body like echo's Bind: an empty body or `null` yields {},
// and a field of the wrong type is rejected like Go's json.Unmarshal.
export async function readJSON<S extends Record<string, FieldType>>(
  c: C,
  fields: S,
): Promise<JSONBody<S>> {
  const text = await c.req.text();
  if (!text) return {};
  if (!c.req.header("Content-Type")?.startsWith("application/json"))
    fail(415, "Unsupported Media Type");
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    fail(400, "invalid JSON");
  }
  if (value === null) return {};
  if (typeof value !== "object" || Array.isArray(value))
    fail(400, "invalid JSON");
  const data = value as Record<string, unknown>,
    body: Record<string, unknown> = {};
  for (const [key, type] of Object.entries(fields)) {
    // As in Go, a null field is the same as an absent one.
    if (data[key] == null) continue;
    if (
      typeof data[key] !== type ||
      (type === "number" && !Number.isSafeInteger(data[key]))
    )
      fail(400, "invalid JSON field: " + key);
    body[key] = data[key];
  }
  return body as JSONBody<S>;
}
export function cursor(c: C, timeKey: string, idKey: string) {
  const at = c.req.query(timeKey) ?? null,
    id = c.req.query(idKey) ?? "";
  if (
    at !== null &&
    (!/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?(?:Z|[+-]\d\d:\d\d)$/.test(
      at,
    ) ||
      Number.isNaN(Date.parse(at)))
  )
    fail(400, "invalid timestamp");
  if ((at === null) !== (id === "")) fail(400, "invalid_pagination");
  return { at, id };
}
export const noContent = () => new Response(null, { status: 204 });
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
