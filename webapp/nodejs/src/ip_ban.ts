import type { Context } from "hono";
import { createMiddleware } from "hono/factory";
import type pg from "pg";
import { fail } from "./core.js";
import { rows } from "./db.js";

const ipBanReloadInterval = Temporal.Duration.from({ milliseconds: 200 });

interface IPBanSnapshot {
  loadedAt: Temporal.Instant;
  bans: Map<string, Temporal.Instant>;
}

async function loadIPBans(db: pg.Pool): Promise<IPBanSnapshot> {
  const records = await rows<{ ip: string; banned_until: string }>(
    db,
    "SELECT ip, banned_until FROM ip_bans WHERE banned_until > clock_timestamp()",
  );
  return {
    loadedAt: Temporal.Now.instant(),
    bans: new Map(
      records.map((r) => [r.ip, Temporal.Instant.from(r.banned_until)]),
    ),
  };
}

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
    if (
      (!snapshot ||
        Temporal.Duration.compare(
          Temporal.Now.instant().since(snapshot.loadedAt),
          ipBanReloadInterval,
        ) >= 0) &&
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
    if (until && Temporal.Instant.compare(Temporal.Now.instant(), until) < 0)
      fail(403, "ip_banned");
    await next();
  });
}
