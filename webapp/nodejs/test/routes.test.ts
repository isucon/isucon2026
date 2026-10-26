import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import type pg from "pg";
import { createApp } from "../src/server.js";

test("every Go route is present with the same authentication boundary", async () => {
  const db = {
    // Only the IP ban list, which every request checks, may be read.
    query: (sql: string) => {
      if (sql.includes("FROM ip_bans")) return { rows: [] };
      throw new Error("unexpected database access");
    },
  } as unknown as pg.Pool;
  const app = createApp(db, "asia", { logOutput: { write: () => true } });
  const source = await readFile(
    new URL("../../go/server.go", import.meta.url),
    "utf8",
  );
  const routes = [
    ...source.matchAll(/e\.(GET|POST|PUT|PATCH|DELETE)\("([^"]+)"([^\n]*)/g),
  ];
  assert.equal(routes.length, 42);
  for (const [, method, path, definition] of routes) {
    assert.ok(
      app.routes.some((r) => r.method === method && r.path === path),
      `${method} ${path}`,
    );
    if (!definition.includes("SessionMiddleware")) continue;
    const response = await app.request(path.replace(/:[^/]+/g, "test"), {
      method,
    });
    assert.equal(response.status, 401, `${method} ${path}`);
    assert.deepEqual(await response.json(), {
      error: definition.includes("AdminSessionMiddleware")
        ? "invalid_administrator_session"
        : "invalid_session",
    });
  }
});
test("malformed image signatures are rejected before querying the database", async () => {
  const db = {
    // Only the IP ban list, which every request checks, may be read.
    query: (sql: string) => {
      if (sql.includes("FROM ip_bans")) return { rows: [] };
      throw new Error("unexpected database access");
    },
  } as unknown as pg.Pool;
  const app = createApp(db, "asia", { logOutput: { write: () => true } });
  for (const query of [
    "",
    "exp=999999999999999999999999&sig=x",
    "exp=01800000000&sig=x",
    "exp=1800000000&sig=YQ",
    "exp=1800000000&exp=1800000000&sig=x",
    "exp=1800000000&sig=x&sig=y",
    "exp=1800000000&sig=%GG",
  ]) {
    const res = await app.request("/api/images/image?" + query);
    assert.equal(res.status, 403);
    assert.equal(res.headers.get("cache-control"), "no-store");
    assert.deepEqual(await res.json(), { error: "image_forbidden" });
  }
});
