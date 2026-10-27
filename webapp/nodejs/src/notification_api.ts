import type pg from "pg";
import { stream } from "hono/streaming";
import { type C } from "./http.js";
import { first, scalar } from "./db.js";
import { getUser } from "./models.js";
interface Notification {
  id: string;
  posting_window_id: string;
  region: string;
  created_at: string;
}
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
    const ticker = setInterval(() => wake?.(), 500);
    try {
      while (!stopped) {
        const user = await getUser(db, session.user_id);
        if (user?.region !== session.region) return;
        const n = await first<Notification>(
          db,
          `SELECT n.id, n.posting_window_id, w.region, n.created_at FROM notifications n JOIN posting_windows w ON w.id = n.posting_window_id WHERE n.user_id = $1 AND n.created_at > $2 AND w.ended_at IS NULL ORDER BY n.created_at, n.id LIMIT 1`,
          session.user_id,
          connectedAt,
        );
        if (n) {
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
        }
        if (!stopped)
          await new Promise<void>((resolve) => {
            wake = resolve;
          });
        wake = undefined;
      }
    } catch (e) {
      if (!stopped) console.error("notification failed", e);
    } finally {
      clearInterval(ticker);
    }
  });
};
