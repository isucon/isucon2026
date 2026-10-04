import type { Context, MiddlewareHandler } from "hono";
import type pg from "pg";
import { fail, hash } from "./core.js";
import { one, NoRows } from "./db.js";
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
export async function body(
  c: C,
  fields: Record<string, "string" | "number">,
): Promise<Record<string, unknown>> {
  const text = await c.req.text();
  if (!text) return {};
  if (!c.req.header("Content-Type")?.split(";")[0].includes("application/json"))
    return fail(415, "Unsupported Media Type");
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    return fail(400, "invalid JSON");
  }
  if (value === null) return {};
  if (typeof value !== "object" || Array.isArray(value))
    return fail(400, "invalid JSON");
  const data = value as Record<string, unknown>;
  for (const [key, type] of Object.entries(fields))
    if (
      data[key] != null &&
      (typeof data[key] !== type ||
        (type === "number" && !Number.isSafeInteger(data[key])))
    )
      fail(400, "invalid JSON field: " + key);
  return data;
}
export const string = (b: Record<string, unknown>, key: string) =>
  (b[key] as string) ?? "";
export function cursor(c: C, timeKey: string, idKey: string) {
  const raw = c.req.query(timeKey),
    id = c.req.query(idKey) ?? "";
  let at: string | null = null;
  if (raw !== undefined) {
    if (
      !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?(?:Z|[+-]\d\d:\d\d)$/.test(
        raw,
      ) ||
      Number.isNaN(Date.parse(raw))
    )
      fail(400, "invalid timestamp");
    at = raw;
  }
  if ((at === null) !== (id === "")) fail(400, "invalid_pagination");
  return [at, id] as const;
}
export function sessionMiddleware(
  db: pg.Pool,
  admin = false,
): MiddlewareHandler<Env> {
  return async (c, next) => {
    const code = admin ? "invalid_administrator_session" : "invalid_session";
    const auth = c.req.header("Authorization");
    if (!auth?.startsWith("Bearer ")) return fail(401, code);
    try {
      if (admin)
        c.set(
          "adminSession",
          await one<AdminSession>(
            db,
            "SELECT administrator_id, token_hash, created_at FROM administrator_sessions WHERE token_hash = $1 AND active = true",
            hash(auth.slice(7)),
          ),
        );
      else
        c.set(
          "session",
          await one<Session>(
            db,
            `SELECT s.user_id, s.token_hash, s.created_at, u.region FROM login_sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = $1 AND s.active = true AND u.status = 'active'`,
            hash(auth.slice(7)),
          ),
        );
    } catch (e) {
      if (e instanceof NoRows) return fail(401, code);
      throw e;
    }
    await next();
  };
}
export const regionMiddleware =
  (region: string): MiddlewareHandler<Env> =>
  async (c, next) => {
    if (c.var.session.region !== region) fail(403, "region_mismatch");
    await next();
  };
