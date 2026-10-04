import pg from "pg";
import { timestamp } from "./core.js";

pg.types.setTypeParser(1184, timestamp);
pg.types.setTypeParser(1700, Number);
pg.types.setTypeParser(1082, (value) => value);
export const configFromEnv = () => ({
  region: process.env.ISUREAL_REGION || "asia",
  host: process.env.ISUREAL_DB_HOST || "127.0.0.1",
  port: Number(process.env.ISUREAL_DB_PORT || "5432"),
  user: process.env.ISUREAL_DB_USER || "isureal",
  password: process.env.ISUREAL_DB_PASSWORD || "isureal",
  database: process.env.ISUREAL_DB_NAME || "isureal",
});
export type Config = ReturnType<typeof configFromEnv>;
export type DB = pg.Pool | pg.PoolClient;
export function createPool(config: Config) {
  return new pg.Pool({ ...config, max: 30 });
}
export async function rows<T extends pg.QueryResultRow>(
  db: DB,
  sql: string,
  ...args: unknown[]
): Promise<T[]> {
  return (await db.query<T>(sql, args)).rows;
}
// Returns the first row, or undefined when the query matched nothing.
export async function first<T extends pg.QueryResultRow>(
  db: DB,
  sql: string,
  ...args: unknown[]
): Promise<T | undefined> {
  return (await rows<T>(db, sql, ...args))[0];
}
// For rows that must exist; a missing row is a server error (500).
export function must<T>(value: T | undefined): T {
  if (value === undefined) throw new Error("sql: no rows in result set");
  return value;
}
export async function one<T extends pg.QueryResultRow>(
  db: DB,
  sql: string,
  ...args: unknown[]
): Promise<T> {
  return must(await first<T>(db, sql, ...args));
}
export async function scalar<T>(
  db: DB,
  sql: string,
  ...args: unknown[]
): Promise<T> {
  return Object.values(await one(db, sql, ...args))[0] as T;
}
// Like scalar, but returns undefined when the query matched nothing.
export async function firstScalar<T>(
  db: DB,
  sql: string,
  ...args: unknown[]
): Promise<T | undefined> {
  const row = await first(db, sql, ...args);
  return row && (Object.values(row)[0] as T);
}
export async function exec(db: DB, sql: string, ...args: unknown[]) {
  return (await db.query(sql, args)).rowCount ?? 0;
}
export async function transaction<T>(
  db: pg.Pool,
  fn: (tx: pg.PoolClient) => Promise<T>,
): Promise<T> {
  const tx = await db.connect();
  try {
    await tx.query("BEGIN");
    const result = await fn(tx);
    await tx.query("COMMIT");
    return result;
  } catch (error) {
    await tx.query("ROLLBACK");
    throw error;
  } finally {
    tx.release();
  }
}
