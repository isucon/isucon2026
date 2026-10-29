import assert from "node:assert/strict";
import { createHash, randomUUID } from "node:crypto";
import { readFileSync } from "node:fs";
import type { AddressInfo } from "node:net";
import { after, before, beforeEach, describe, it } from "node:test";
import { serve, type ServerType } from "@hono/node-server";
import jpeg from "jpeg-js";
import pg from "pg";
import { configFromEnv } from "../src/config.ts";
import { newServer } from "../src/server.ts";

const enabled = process.env.ISUREAL_TEST_DATABASE === "1";
const config = { ...configFromEnv(), region: "asia", logLevel: "error" };
const sha256 = (value: string) => createHash("sha256").update(value).digest("hex");
const image = new Uint8Array(jpeg.encode({ width: 16, height: 16, data: Buffer.alloc(16 * 16 * 4, 200) }, 80).data);

let server: ServerType;
let baseURL: string;
let db: pg.Client;

async function request(method: string, path: string, options: { token?: string; json?: unknown; body?: BodyInit; headers?: Record<string, string> } = {}) {
  const headers: Record<string, string> = { ...options.headers };
  if (options.token !== undefined) {
    headers.Authorization = options.token;
  }
  let body = options.body;
  if (options.json !== undefined) {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(options.json);
  }
  const res = await fetch(baseURL + path, { method, headers, body });
  const text = await res.text();
  return { status: res.status, headers: res.headers, text, json: () => JSON.parse(text) };
}

async function login(username: string): Promise<string> {
  const res = await request("POST", "/api/auth/login", { json: { username, secretKey: `${username}-secret` } });
  assert.equal(res.status, 200, res.text);
  return `Bearer ${res.json().token}`;
}

describe("API", { skip: !enabled && "ISUREAL_TEST_DATABASE=1 のときだけ実行する" }, () => {
  before(async () => {
    db = new pg.Client({ host: config.dbHost, port: Number(config.dbPort), user: config.dbUser, password: config.dbPassword, database: config.dbName });
    await db.connect();
    const app = await newServer(config, 5);
    server = serve({ fetch: app.fetch, port: 0 });
    await new Promise((resolve) => server.once("listening", resolve));
    baseURL = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  });

  after(async () => {
    server.close();
    await db.end();
  });

  beforeEach(async () => {
    await db.query(readFileSync(new URL("../../sql/001-schema.sql", import.meta.url), "utf8"));
    await db.query(`INSERT INTO administrators (id, username, password_hash) VALUES ('admin1', 'admin', $1)`, [sha256("password")]);
    for (const [id, username, region] of [
      ["u-alice", "alice", "asia"],
      ["u-bob", "bob", "asia"],
      ["u-dave", "dave", "europe"],
    ]) {
      await db.query(`INSERT INTO users (id, username, secret_key_hash, display_name, region) VALUES ($1, $2, $3, $4, $5)`, [id, username, sha256(`${username}-secret`), username, region]);
    }
    await db.query(`INSERT INTO friendships (id, user_low_id, user_high_id) VALUES ('f1', 'u-alice', 'u-bob')`);
  });

  it("管理者セッションを検証する", async () => {
    const res = await request("POST", "/api/admin/auth/login", { json: { username: "admin", password: "password" } });
    assert.equal(res.status, 200);
    const admin = `Bearer ${res.json().token}`;
    const alice = await login("alice");
    const certify = (token?: string) => request("PUT", "/api/admin/users/u-bob/certification", { token });
    assert.equal((await certify()).status, 401);
    assert.equal((await certify("Basic invalid")).status, 401);
    assert.equal((await certify("Bearer unknown")).status, 401);
    assert.equal((await certify(alice)).status, 401);
    assert.equal((await certify(admin)).status, 204);
    assert.equal((await certify(admin)).status, 409);
    assert.equal((await request("POST", "/api/admin/auth/logout", { token: admin })).status, 204);
    assert.equal((await certify(admin)).status, 401);
  });

  it("投稿して友達のフィードと画像で読める", async () => {
    const admin = `Bearer ${(await request("POST", "/api/admin/auth/login", { json: { username: "admin", password: "password" } })).json().token}`;
    assert.equal((await request("POST", "/api/admin/posting-window", { token: admin, json: { region: "asia", localDate: "2026-10-26" } })).status, 201);
    const alice = await login("alice");
    const bob = await login("bob");
    const post = async (token: string, caption: string) => {
      const form = new FormData();
      form.append("image", new Blob([image], { type: "image/jpeg" }), "a.jpg");
      form.append("caption", caption);
      form.append("visibility", "friends");
      form.append("location_kind", "exact");
      form.append("latitude", "35.5");
      form.append("longitude", "139");
      return request("POST", "/api/posts", { token, body: form });
    };
    const created = await post(alice, "<hello>");
    assert.equal(created.status, 201, created.text);
    assert.match(created.text, /"caption":"\\u003chello\\u003e"/);
    assert.deepEqual(created.json().location, { kind: "exact", latitude: 35.5, longitude: 139 });
    assert.equal((await post(alice, "again")).status, 409);

    assert.equal((await request("GET", "/api/feeds/friends", { token: bob })).json().items.length, 0);
    assert.equal((await post(bob, "bob")).status, 201);
    const feed = (await request("GET", "/api/feeds/friends", { token: bob })).json();
    assert.equal(feed.items.length, 1);
    assert.equal(feed.items[0].id, created.json().id);

    const imageRes = await fetch(baseURL + feed.items[0].imageUrl);
    assert.equal(imageRes.status, 200);
    assert.equal(imageRes.headers.get("content-type"), "image/jpeg");
    assert.equal(imageRes.headers.get("cache-control"), "no-store");
    assert.deepEqual(new Uint8Array(await imageRes.arrayBuffer()), image);
    assert.equal((await fetch(baseURL + feed.items[0].imageUrl.replace(/sig=../, "sig=AA"))).status, 403);
  });

  it("友達の友達フィードをおすすめ順に並べる", async () => {
    for (const id of ["viewer", "f1", "f2", "a1", "a2", "x"]) {
      await db.query(`INSERT INTO users (id, username, secret_key_hash, display_name, region) VALUES ($1, $2, $3, $4, 'asia')`, [id, id, sha256(`${id}-secret`), id]);
    }
    const viewer = await login("viewer");
    await db.query(`INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline, ended_at) VALUES ('w', 'asia', '2026-09-23', '2026-09-23T00:00:00Z', '2026-09-23T00:02:00Z', NULL)`);
    for (const [i, author] of ["viewer", "a1", "a2", "x", "f1"].entries()) {
      await db.query(`INSERT INTO image_objects (id, owner_user_id, data, media_type) VALUES ($1, $2, $3, 'image/jpeg')`, [`${author}-image`, author, Buffer.from("image")]);
      await db.query(
        `INSERT INTO posts (id, author_id, posting_window_id, image_id, caption, visibility, location_kind, posted_at) VALUES ($1, $2, 'w', $3, 'caption', 'friends_of_friends', 'none', $4)`,
        [author, author, `${author}-image`, `2026-09-23T00:01:0${i}Z`],
      );
    }
    for (const pair of [["f1", "viewer"], ["f2", "viewer"], ["a1", "f1"], ["a1", "f2"], ["a2", "f1"], ["a1", "x"], ["a2", "x"], ["f2", "x"]]) {
      const [low, high] = pair.toSorted();
      await db.query(`INSERT INTO friendships (id, user_low_id, user_high_id) VALUES ($1, $2, $3)`, [randomUUID(), low, high]);
    }
    const assertRanking = async (path: string, ...expected: string[]) => {
      const res = await request("GET", path, { token: viewer });
      assert.equal(res.status, 200, res.text);
      const response = res.json();
      const got = response.items.map((item: { id: string; score?: number }) => {
        assert.ok(Number.isInteger(item.score), `${path}: post ${item.id} has no score`);
        return `${item.id}:${item.score}`;
      });
      assert.deepEqual(got, expected, path);
      return response;
    };
    const feed = "/api/feeds/friends-of-friends";
    await assertRanking(feed, "a1:7", "x:6", "a2:4");
    await db.query(`INSERT INTO realmojis (id, post_id, reactor_id, image_id, reaction_type, created_at) VALUES ($1, 'a2', 'f1', 'rm', 'smile', clock_timestamp())`, [randomUUID()]);
    await db.query(`INSERT INTO comments (id, post_id, author_id, body, created_at) VALUES ($1, 'a2', 'f1', 'nice', clock_timestamp()), ($2, 'a2', 'x', 'nice', clock_timestamp())`, [randomUUID(), randomUUID()]);
    const page = await assertRanking(feed, "a2:14", "a1:7", "x:6");
    await db.query(`INSERT INTO realmojis (id, post_id, reactor_id, image_id, reaction_type, created_at) VALUES ($1, 'a1', 'f2', 'rm', 'smile', clock_timestamp())`, [randomUUID()]);
    await db.query(`UPDATE users SET status='deleted' WHERE id='f2'`);
    await assertRanking(feed, "a2:14", "a1:7", "x:6");

    const cursor = page.items[1];
    const before = new URLSearchParams({ beforeScore: String(cursor.score), beforePostedAt: cursor.postedAt, beforeId: cursor.id });
    await assertRanking(`${feed}?${before}`, "x:6");
    assert.equal((await request("GET", `${feed}?beforeScore=7`, { token: viewer })).status, 400);
    assert.equal((await request("GET", `${feed}?beforePostedAt=2026-09-23T00:01:01Z&beforeId=a1`, { token: viewer })).status, 400);

    const friends = await request("GET", "/api/feeds/friends", { token: viewer });
    assert.equal(friends.status, 200, friends.text);
    const items = friends.json().items;
    assert.deepEqual(items.map((item: { id: string }) => item.id), ["f1"]);
    assert.ok(!Object.hasOwn(items[0], "score"), "friends feed returned a score for f1");
    assert.equal((await request("GET", "/api/feeds/friends?beforeScore=abc", { token: viewer })).status, 400);
  });

  it("通知はヘッダを先に返し、投稿期間が始まると 1 件送って閉じる", async () => {
    const alice = await login("alice");
    const res = await fetch(baseURL + "/api/notifications", { headers: { Authorization: alice } });
    assert.equal(res.status, 200);
    assert.equal(res.headers.get("content-type"), "text/event-stream");
    const admin = `Bearer ${(await request("POST", "/api/admin/auth/login", { json: { username: "admin", password: "password" } })).json().token}`;
    const window = (await request("POST", "/api/admin/posting-window", { token: admin, json: { region: "asia", localDate: "2026-10-26" } })).json();
    const text = await res.text();
    const match = /^data: (.*)\n\n$/.exec(text);
    assert.ok(match, text);
    const notification = JSON.parse(match[1]);
    assert.deepEqual(Object.keys(notification), ["id", "postingWindowId", "region", "createdAt"]);
    assert.equal(notification.postingWindowId, window.id);
  });

  it("切断された通知はポーリングをやめる", async () => {
    const scans = async () => {
      await db.query(`SELECT pg_stat_clear_snapshot()`);
      const { rows } = await db.query(`SELECT coalesce(seq_scan, 0) + coalesce(idx_scan, 0) AS scans FROM pg_stat_user_tables WHERE relname = 'notifications'`);
      return Number(rows[0].scans);
    };
    const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));
    const alice = await login("alice");
    const controller = new AbortController();
    const res = await fetch(baseURL + "/api/notifications", { headers: { Authorization: alice }, signal: controller.signal });
    assert.equal(res.status, 200);
    await sleep(1500);
    const connected = await scans();
    await sleep(2500);
    assert.ok((await scans()) > connected, "接続中はポーリングする");
    controller.abort();
    await sleep(1500);
    const disconnected = await scans();
    await sleep(2500);
    assert.equal(await scans(), disconnected);
  });

  it("通知を探している間に切断されても落ちない", async () => {
    const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));
    const alice = await login("alice");
    const controller = new AbortController();
    const res = await fetch(baseURL + "/api/notifications", { headers: { Authorization: alice }, signal: controller.signal });
    assert.equal(res.status, 200);
    const locker = new pg.Client({ host: config.dbHost, port: Number(config.dbPort), user: config.dbUser, password: config.dbPassword, database: config.dbName });
    await locker.connect();
    try {
      await locker.query(`BEGIN`);
      await locker.query(`LOCK TABLE notifications IN ACCESS EXCLUSIVE MODE`);
      await sleep(1000);
      controller.abort();
      await sleep(200);
      await locker.query(`INSERT INTO posting_windows (id, region, local_date, starts_at, on_time_deadline) VALUES ('w1', 'asia', '2026-10-26', clock_timestamp(), clock_timestamp())`);
      await locker.query(`INSERT INTO notifications (id, user_id, posting_window_id, created_at) VALUES ('n1', 'u-alice', 'w1', clock_timestamp())`);
      await locker.query(`COMMIT`);
    } finally {
      await locker.end();
    }
    await sleep(500);
    assert.equal((await request("GET", "/api/me", { token: alice })).status, 200);
  });

  it("未知のパスは 404、メソッド違いは 405 と Allow を返す", async () => {
    const notFound = await request("GET", "/api/unknown");
    assert.equal(notFound.status, 404);
    assert.equal(notFound.text, '{"error":"Not Found"}\n');
    const notAllowed = await request("PUT", "/api/posts");
    assert.equal(notAllowed.status, 405);
    assert.equal(notAllowed.headers.get("allow"), "OPTIONS, POST");
    const options = await request("OPTIONS", "/api/me");
    assert.equal(options.status, 204);
    assert.equal(options.headers.get("allow"), "OPTIONS, GET, PATCH");
  });

  it("BAN された IP は 403 になる", async () => {
    const alice = await login("alice");
    await db.query(`INSERT INTO ip_bans (ip, banned_until) VALUES ('203.0.113.9', clock_timestamp() + interval '1 minute')`);
    await new Promise((resolve) => setTimeout(resolve, 250));
    const banned = await request("GET", "/api/me", { token: alice, headers: { "X-Client-IP": "203.0.113.9" } });
    assert.equal(banned.status, 403);
    assert.equal(banned.json().error, "ip_banned");
    assert.equal((await request("GET", "/api/me", { token: alice, headers: { "X-Client-IP": "203.0.113.10" } })).status, 200);
  });

  it("リージョンが違うユーザーは 403 になる", async () => {
    const dave = await login("dave");
    const res = await request("GET", "/api/me", { token: dave });
    assert.equal(res.status, 403);
    assert.equal(res.json().error, "region_mismatch");
  });
});
