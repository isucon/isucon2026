import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { Hono } from "hono";
import { initializeHandler } from "../src/initialize.ts";
import type { Env, HandlerContext } from "../src/server.ts";

test("initialize returns survey fields only after successful initialization", async () => {
  const previous = process.cwd();
  const root = await mkdtemp(join(tmpdir(), "isureal-initialize-"));
  try {
    await mkdir(join(root, "nodejs"));
    await mkdir(join(root, "sql"));
    const script = join(root, "sql", "init.sh");
    await writeFile(script, "#!/bin/sh\nexit 0\n", { mode: 0o755 });
    process.chdir(join(root, "nodejs"));
    const app = new Hono<Env>();
    app.post("/api/initialize", initializeHandler({} as HandlerContext));
    app.onError((_, c) => c.json({ error: "initialize failed" }, 500));
    const response = await app.request("/api/initialize", { method: "POST" });
    assert.equal(response.status, 200);
    assert.deepEqual(await response.json(), {
      language: "nodejs",
      agent: "human",
      harness: "hand",
    });
    await writeFile(script, "#!/bin/sh\nexit 1\n");
    const failure = await app.request("/api/initialize", { method: "POST" });
    assert.equal(failure.status, 500);
  } finally {
    process.chdir(previous);
    await rm(root, { recursive: true, force: true });
  }
});
