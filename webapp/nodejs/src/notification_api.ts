import type pg from "pg";
import { stream } from "hono/streaming";
import { type C } from "./http.js";
import { one, scalar, NoRows } from "./db.js";
import { getUser } from "./models.js";
export const notificationsHandler = (db: pg.Pool) => async (c: C) => {
  const session = c.var.session,
    connectedAt = await scalar<string>(db, "SELECT clock_timestamp()");
  c.header("Content-Type", "text/event-stream");
  return stream(c, async (output) => {
    let stopped = false;
    let wake: (() => void) | undefined;
    output.onAbort(() => {
      stopped = true;
      wake?.();
    });
    // Like Go's ticker, the interval starts before the first database read.
    const ticker = setInterval(() => wake?.(), 500);
    try {
      while (!stopped) {
        const user = await getUser(db, session.user_id);
        if (user.region !== session.region) return;
        try {
          const n = await one<{
            id: string;
            posting_window_id: string;
            region: string;
            created_at: string;
          }>(
            db,
            `SELECT n.id, n.posting_window_id, w.region, n.created_at FROM notifications n JOIN posting_windows w ON w.id = n.posting_window_id WHERE n.user_id = $1 AND n.created_at > $2 AND w.ended_at IS NULL ORDER BY n.created_at, n.id LIMIT 1`,
            session.user_id,
            connectedAt,
          );
          await output.write(
            "data: " +
              JSON.stringify({
                id: n.id,
                postingWindowId: n.posting_window_id,
                region: n.region,
                createdAt: n.created_at,
              }) +
              "\n\n",
          );
          return;
        } catch (e) {
          if (!(e instanceof NoRows)) throw e;
        }
        if (!stopped)
          await new Promise<void>((resolve) => {
            wake = resolve;
          });
        wake = undefined;
      }
    } catch (e) {
      if (!(e instanceof NoRows) && !stopped)
        console.error("notification failed", e);
    } finally {
      clearInterval(ticker);
    }
  });
};
