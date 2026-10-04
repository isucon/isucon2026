import pg from "pg";
import { APIError, timestamp } from "./core.js";

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
export class NoRows extends Error {
  constructor() {
    super("sql: no rows in result set");
  }
}
export async function rows<T extends pg.QueryResultRow>(
  db: DB,
  sql: string,
  ...args: unknown[]
): Promise<T[]> {
  return (await db.query<T>(sql, args)).rows;
}
export async function one<T extends pg.QueryResultRow>(
  db: DB,
  sql: string,
  ...args: unknown[]
): Promise<T> {
  const result = await rows<T>(db, sql, ...args);
  if (!result.length) throw new NoRows();
  return result[0];
}
export async function scalar<T>(
  db: DB,
  sql: string,
  ...args: unknown[]
): Promise<T> {
  return Object.values(await one(db, sql, ...args))[0] as T;
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
export async function notFound<T>(
  fn: () => Promise<T>,
  status: number,
  code: string,
): Promise<T> {
  try {
    return await fn();
  } catch (e) {
    if (e instanceof NoRows) {
      throw new APIError(status, code);
    }
    throw e;
  }
}
