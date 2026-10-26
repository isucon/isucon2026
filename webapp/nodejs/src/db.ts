import pg from "pg";
import type { Config } from "./config.ts";
import { parsePostgresTimestamp } from "./time.ts";
import { logError } from "./response.ts";

export type Row = Record<string, any>;

export interface Queryer {
  get<T = Row>(sql: string, ...args: unknown[]): Promise<T | undefined>;
  select<T = Row>(sql: string, ...args: unknown[]): Promise<T[]>;
  exec(sql: string, ...args: unknown[]): Promise<number>;
}

export class NoRowsError extends Error {
  constructor() {
    super("sql: no rows in result set");
  }
}

export function must<T>(value: T): Exclude<T, undefined> {
  if (value === undefined) {
    throw new NoRowsError();
  }
  return value as Exclude<T, undefined>;
}

const typeParsers: Record<number, (value: string) => unknown> = {
  [pg.types.builtins.INT8]: Number,
  [pg.types.builtins.NUMERIC]: Number,
  [pg.types.builtins.DATE]: (value) => value,
  [pg.types.builtins.TIMESTAMPTZ]: parsePostgresTimestamp,
};

const statementNames = new Map<string, string>();

function statement(sql: string, args: unknown[]): pg.QueryConfig {
  let name = statementNames.get(sql);
  if (name === undefined) {
    name = `isureal_${statementNames.size + 1}`;
    statementNames.set(sql, name);
  }
  return { name, text: sql, values: args };
}

export class DB implements Queryer {
  readonly #pool: pg.Pool;

  constructor(config: Config, size: number) {
    this.#pool = new pg.Pool({
      host: config.dbHost,
      port: Number(config.dbPort),
      user: config.dbUser,
      password: config.dbPassword,
      database: config.dbName,
      max: size,
      types: {
        getTypeParser: ((oid: number, format?: "text" | "binary") =>
          typeParsers[oid] ?? pg.types.getTypeParser(oid, format)) as typeof pg.types.getTypeParser,
      },
    });
    this.#pool.on("error", (err) => logError("database connection failed", err));
  }

  async ping(): Promise<void> {
    await this.#pool.query("SELECT 1");
  }

  async get<T = Row>(sql: string, ...args: unknown[]): Promise<T | undefined> {
    const result = await this.#pool.query(statement(sql, args));
    return result.rows[0];
  }

  async select<T = Row>(sql: string, ...args: unknown[]): Promise<T[]> {
    const result = await this.#pool.query(statement(sql, args));
    return result.rows;
  }

  async exec(sql: string, ...args: unknown[]): Promise<number> {
    const result = await this.#pool.query(statement(sql, args));
    return result.rowCount ?? 0;
  }

  async begin(): Promise<Tx> {
    const client = await this.#pool.connect();
    try {
      await client.query("BEGIN");
    } catch (err) {
      client.release(err as Error);
      throw err;
    }
    return new Tx(client);
  }

  async end(): Promise<void> {
    await this.#pool.end();
  }
}

export class Tx implements Queryer {
  readonly #client: pg.PoolClient;
  #done = false;

  constructor(client: pg.PoolClient) {
    this.#client = client;
  }

  async get<T = Row>(sql: string, ...args: unknown[]): Promise<T | undefined> {
    const result = await this.#client.query(statement(sql, args));
    return result.rows[0];
  }

  async select<T = Row>(sql: string, ...args: unknown[]): Promise<T[]> {
    const result = await this.#client.query(statement(sql, args));
    return result.rows;
  }

  async exec(sql: string, ...args: unknown[]): Promise<number> {
    const result = await this.#client.query(statement(sql, args));
    return result.rowCount ?? 0;
  }

  async commit(): Promise<void> {
    await this.#finish("COMMIT");
  }

  async rollback(): Promise<void> {
    if (!this.#done) {
      await this.#finish("ROLLBACK");
    }
  }

  async [Symbol.asyncDispose](): Promise<void> {
    await this.rollback().catch(() => {});
  }

  async #finish(command: string): Promise<void> {
    this.#done = true;
    try {
      await this.#client.query(command);
    } catch (err) {
      this.#client.release(err as Error);
      throw err;
    }
    this.#client.release();
  }
}
