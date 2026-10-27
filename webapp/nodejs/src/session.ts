import { randomUUID } from "node:crypto";
import type pg from "pg";
import { hash } from "./core.js";
import { exec, one, transaction } from "./db.js";
import type { Session } from "./types/hono.js";
// 2026-09-03: リファクタリング後にこの関数の利用がないようです。消すのはいかがでしょうか
export const createLoginSession = (db: pg.Pool, userID: string) =>
  transaction(db, (tx) => createLoginSessionTx(tx, userID));
export async function createLoginSessionTx(tx: pg.PoolClient, userID: string) {
  const token = randomUUID();
  await one(
    tx,
    "SELECT id FROM users WHERE id = $1 AND status = 'active' FOR NO KEY UPDATE",
    userID,
  );
  await exec(
    tx,
    "UPDATE login_sessions SET active = false, invalidated_at = clock_timestamp(), reason = 'replaced' WHERE user_id = $1 AND active = true",
    userID,
  );
  const id = randomUUID();
  await exec(
    tx,
    "INSERT INTO login_sessions (id, user_id, token_hash) VALUES ($1, $2, $3)",
    id,
    userID,
    hash(token),
  );
  const session = await one<Omit<Session, "region">>(
    tx,
    "SELECT user_id, token_hash, created_at FROM login_sessions WHERE id = $1",
    id,
  );
  return { ...session, token };
}
