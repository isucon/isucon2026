import type { Context } from "hono";
import { createMiddleware } from "hono/factory";
import type pg from "pg";
import { fail } from "./core.js";
import { rows } from "./db.js";

const ipBanReloadInterval = 200;

interface IPBanSnapshot {
  loadedAt: number;
  bans: Map<string, number>;
}

async function loadIPBans(db: pg.Pool): Promise<IPBanSnapshot> {
  const records = await rows<{ ip: string; banned_until: string }>(
    db,
    "SELECT ip, banned_until FROM ip_bans WHERE banned_until > clock_timestamp()",
  );
  return {
    loadedAt: Date.now(),
    bans: new Map(records.map((r) => [r.ip, Date.parse(r.banned_until)])),
  };
}

// Like Go's echo.ExtractIPDirect, an IPv4 peer is reported without the
// IPv4-mapped IPv6 prefix that Node's dual-stack listener adds.
export function extractClientIP(c: Context) {
  const header = c.req.header("X-Client-IP");
  if (header) return header;
  const remote: string =
    c.env?.incoming?.socket?.remoteAddress ?? c.env?.remoteAddr ?? "";
  return remote.startsWith("::ffff:") && remote.includes(".")
    ? remote.slice(7)
    : remote;
}

export function ipBanMiddleware(db: pg.Pool) {
  let snapshot: IPBanSnapshot | undefined;
  let reloading = false;
  return createMiddleware(async (c, next) => {
    // While one request reloads the list, the others use the previous snapshot.
    if (
      (snapshot === undefined ||
        Date.now() - snapshot.loadedAt >= ipBanReloadInterval) &&
      !reloading
    ) {
      reloading = true;
      try {
        snapshot = await loadIPBans(db);
      } finally {
        reloading = false;
      }
    }
    const until = snapshot?.bans.get(extractClientIP(c));
    if (until !== undefined && Date.now() < until) fail(403, "ip_banned");
    await next();
  });
}
