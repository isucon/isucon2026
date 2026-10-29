import assert from "node:assert/strict";
import { Writable } from "node:stream";
import { it } from "node:test";
import { Hono } from "hono";
import { actionLogMiddleware } from "../src/action_log.ts";
import type { Env } from "../src/server.ts";

it("logs invalid images and signatures at info with their abuse marker", async () => {
  const lines: string[] = [];
  const output = new Writable({ write(chunk, _encoding, done) { lines.push(String(chunk).trim()); done(); } });
  const app = new Hono<Env>();
  app.use(actionLogMiddleware("info", output));
  app.put("/api/me/profile-image", (c) => {
    c.set("abuse", "invalid_image");
    return c.json({ error: "invalid_profile_image" }, 400);
  });
  app.get("/api/images/:imageId", (c) => {
    c.set("abuse", "invalid_signature");
    return c.json({ error: "image_forbidden" }, 403);
  });
  const bindings = (ip: string): Env["Bindings"] => ({ incoming: { headersDistinct: { "x-client-ip": [ip] } } }) as unknown as Env["Bindings"];
  await app.request("/api/me/profile-image", { method: "PUT" }, bindings("192.0.2.1"));
  await app.request("/api/images/image-id", {}, bindings("192.0.2.2"));
  assert.deepEqual(lines.map((line) => JSON.parse(line)), [
    { action: "put_profile_image", ip: "192.0.2.1", level: "info", status: 400, user_id: null, abuse: "invalid_image" },
    { action: "get_image", ip: "192.0.2.2", level: "info", status: 403, user_id: null, abuse: "invalid_signature" },
  ]);
});
