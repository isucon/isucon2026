import type { Queryer } from "./db.ts";
import type { Timestamp } from "./time.ts";

export type PostingWindow = {
  id: string;
  region: string;
  localDate: string;
  startsAt: Timestamp;
  onTimeDeadline: Timestamp;
  endedAt: Timestamp | null;
};

export async function getPostingWindow(db: Queryer, windowID: string): Promise<PostingWindow | undefined> {
  const row = await db.get(
    `SELECT id, region, local_date, starts_at, on_time_deadline, ended_at FROM posting_windows WHERE id = $1`,
    windowID,
  );
  if (row === undefined) {
    return undefined;
  }
  return {
    id: row.id,
    region: row.region,
    localDate: row.local_date,
    startsAt: row.starts_at,
    onTimeDeadline: row.on_time_deadline,
    endedAt: row.ended_at,
  };
}
