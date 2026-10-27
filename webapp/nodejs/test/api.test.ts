import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import pg from "pg";
import { unzipSync, strFromU8 } from "fflate";
import { createApp } from "../src/server.js";
import { hash } from "../src/core.js";
import jpeg from "jpeg-js";

const jpegImage = (width: number, height: number) =>
  new Uint8Array(
    jpeg.encode(
      { width, height, data: Buffer.alloc(width * height * 4, 128) },
      80,
    ).data,
  );
const postImage = jpegImage(64, 64),
  mojiImage = jpegImage(32, 32),
  profileImage = jpegImage(16, 16);

const dsn = process.env.ISUREAL_NODE_TEST_DSN;
test(
  "Go API workflows on an isolated PostgreSQL database",
  { skip: !dsn },
  async (t) => {
    const db = new pg.Pool({ connectionString: dsn, max: 30 });
    t.after(() => db.end());
    await db.query(
      await readFile(
        new URL("../../sql/001-schema.sql", import.meta.url),
        "utf8",
      ),
    );
    const app = process.env.ISUREAL_TEST_BASE_URL
      ? {
          request: (path: string, init?: RequestInit) =>
            fetch(new URL(path, process.env.ISUREAL_TEST_BASE_URL), init),
        }
      : createApp(db, "asia", { logOutput: { write: () => true } });
    const request = async (
      method: string,
      path: string,
      token = "",
      body?: unknown,
      status = 200,
    ) => {
      const headers: Record<string, string> = {};
      if (token) headers.Authorization = "Bearer " + token;
      let payload: BodyInit | undefined;
      if (body instanceof FormData || body instanceof Uint8Array)
        payload = body as BodyInit;
      else if (body !== undefined) {
        headers["Content-Type"] = "application/json";
        payload = JSON.stringify(body);
      }
      const res = await app.request(path, { method, headers, body: payload });
      assert.equal(
        res.status,
        status,
        `${method} ${path}: ${await res.clone().text()}`,
      );
      return res;
    };
    const reg = async (name: string) => {
      const registered = await (
        await request(
          "POST",
          "/api/auth/register",
          "",
          { username: name, displayName: name, region: "asia" },
          201,
        )
      ).json();
      assert.equal(registered.token, undefined);
      const logged = await (
        await request("POST", "/api/auth/login", "", {
          username: name,
          secretKey: registered.secretKey,
        })
      ).json();
      return { ...registered, token: logged.token, id: registered.user.id };
    };
    const alice = await reg("alice"),
      bob = await reg("bob"),
      carol = await reg("carol");
    await request(
      "POST",
      "/api/auth/register",
      "",
      { username: "alice", displayName: "x", region: "asia" },
      409,
    );
    await request(
      "POST",
      "/api/auth/register",
      "",
      { username: "INVALID", displayName: "x", region: "asia" },
      400,
    );
    await request("GET", "/api/me", "", undefined, 401);
    await request("PATCH", "/api/me", alice.token, {
      displayName: "😀".repeat(100),
      bio: "private",
    });
    await request(
      "PATCH",
      "/api/me",
      alice.token,
      { displayName: "😀".repeat(101) },
      400,
    );
    await request("PATCH", "/api/me", alice.token, {}, 400);
    await request("PATCH", "/api/me", alice.token, { region: "europe" });
    await request("GET", "/api/me", alice.token, undefined, 403);
    await request("PATCH", "/api/me", alice.token, { region: "asia" });
    await db.query(
      "INSERT INTO administrators(id,username,password_hash) VALUES('admin','root',$1)",
      [hash("password")],
    );
    const admin = (
      await (
        await request("POST", "/api/admin/auth/login", "", {
          username: "root",
          password: "password",
        })
      ).json()
    ).token;
    await request(
      "PUT",
      `/api/admin/users/${carol.id}/certification`,
      admin,
      undefined,
      204,
    );
    await request(
      "DELETE",
      `/api/admin/users/${carol.id}/certification`,
      admin,
      undefined,
      409,
    );
    assert.equal(
      (
        await (
          await request("GET", "/api/posting-window/current", alice.token)
        ).json()
      ).window,
      null,
    );
    const window = await (
      await request(
        "POST",
        "/api/admin/posting-window",
        admin,
        { region: "asia", localDate: "2026-10-04" },
        201,
      )
    ).json();
    await request(
      "POST",
      "/api/admin/posting-window",
      admin,
      { region: "asia", localDate: "2026-10-04" },
      409,
    );
    const post = async (token: string, visibility = "public") => {
      const form = new FormData();
      form.set(
        "image",
        new Blob([postImage], { type: "image/jpeg" }),
        "image.jpg",
      );
      form.set("caption", "caption");
      form.set("visibility", visibility);
      form.set("location_kind", "none");
      return (await request("POST", "/api/posts", token, form, 201)).json();
    };
    const ap = await post(alice.token);
    await request("GET", `/api/posts/${ap.id}`, bob.token, undefined, 404);
    const bp = await post(bob.token);
    await request("GET", `/api/posts/${ap.id}`, bob.token);
    assert.equal(
      (
        await (
          await request("GET", "/api/posts/me/current", alice.token)
        ).json()
      ).id,
      ap.id,
    );
    assert.equal(
      (await (await request("GET", "/api/feeds/public", bob.token)).json())
        .items[0].id,
      ap.id,
    );
    await request(
      "GET",
      "/api/feeds/public?beforeId=x",
      bob.token,
      undefined,
      400,
    );
    const img = await request("GET", ap.imageUrl);
    assert.deepEqual(new Uint8Array(await img.arrayBuffer()), postImage);
    assert.equal(img.headers.get("cache-control"), "no-store");
    await request("GET", ap.imageUrl + "&exp=1", "", undefined, 403);
    await request(
      "GET",
      ap.imageUrl.replace(
        /sig=(.)/,
        (_: string, first: string) => "sig=" + (first === "x" ? "y" : "x"),
      ),
      "",
      undefined,
      403,
    );
    const comment = await (
      await request(
        "POST",
        `/api/posts/${ap.id}/comments`,
        bob.token,
        { body: "hello" },
        201,
      )
    ).json();
    assert.equal(
      (
        await (
          await request("GET", `/api/posts/${ap.id}/comments`, alice.token)
        ).json()
      ).items[0].id,
      comment.id,
    );
    const react = async () => {
      const f = new FormData();
      f.set("image", new Blob([mojiImage]), "r.jpg");
      f.set("reactionType", "heart");
      return (
        await request(
          "POST",
          `/api/posts/${ap.id}/realmojis`,
          bob.token,
          f,
          201,
        )
      ).json();
    };
    const r1 = await react(),
      r2 = await react();
    for (const [image, path, error] of [
      [jpegImage(4097, 1), "/api/posts", "invalid_post"],
      [new TextEncoder().encode("jpeg"), "/api/posts", "invalid_post"],
      [jpegImage(513, 1), `/api/posts/${ap.id}/realmojis`, "invalid_realmoji"],
      [jpegImage(1, 513), `/api/posts/${ap.id}/realmojis`, "invalid_realmoji"],
    ] as const) {
      const f = new FormData();
      f.set("image", new Blob([image]), "image.jpg");
      f.set("caption", "caption");
      f.set("visibility", "public");
      f.set("location_kind", "none");
      f.set("reactionType", "heart");
      const res = await request("POST", path, bob.token, f, 400);
      assert.deepEqual(await res.json(), { error });
    }
    await request("GET", r1.imageUrl, "", undefined, 403);
    await request("GET", r2.imageUrl);
    assert.equal(
      (
        await (
          await request("GET", `/api/posts/${ap.id}/realmojis`, alice.token)
        ).json()
      ).items.length,
      1,
    );
    const memories = await (
      await request("GET", "/api/memories?month=2026-10", alice.token)
    ).json();
    assert.equal(memories.items[0].comments.length, 1);
    assert.equal(memories.items[0].realmojis.length, 1);
    await request(
      "GET",
      "/api/memories/2026-02-30",
      alice.token,
      undefined,
      400,
    );
    await request("GET", "/api/memories/2026-10-04", alice.token);
    const zip = unzipSync(
      new Uint8Array(
        await (
          await request("GET", "/api/me/export", alice.token)
        ).arrayBuffer(),
      ),
    );
    assert.deepEqual(zip["images/000001.jpg"], postImage);
    assert.equal(JSON.parse(strFromU8(zip["posts.json"]))[0].id, ap.id);
    const fr = await (
      await request(
        "POST",
        "/api/friend-requests",
        alice.token,
        { recipientId: bob.id },
        201,
      )
    ).json();
    await request(
      "POST",
      `/api/friend-requests/${fr.relationId}/accept`,
      alice.token,
      undefined,
      403,
    );
    // Object.prototype members are not actions or relationship kinds.
    for (const action of ["toString", "constructor", "__proto__"])
      await request(
        "POST",
        `/api/friend-requests/${fr.relationId}/${action}`,
        bob.token,
        undefined,
        403,
      );
    for (const kind of ["toString", "constructor", "__proto__"])
      await request(
        "GET",
        `/api/relationships/${kind}`,
        alice.token,
        undefined,
        400,
      );
    await request(
      "POST",
      `/api/friend-requests/${fr.relationId}/accept`,
      bob.token,
    );
    assert.equal(
      (
        await (
          await request("GET", "/api/relationships/friends", alice.token)
        ).json()
      ).items.length,
      1,
    );
    assert.equal(
      (await (await request("GET", `/api/users/${alice.id}`, bob.token)).json())
        .bio,
      "private",
    );
    await request(
      "PUT",
      `/api/follows/${carol.id}`,
      alice.token,
      undefined,
      204,
    );
    await request(
      "PUT",
      `/api/follows/${carol.id}`,
      alice.token,
      undefined,
      409,
    );
    await request(
      "DELETE",
      `/api/follows/${carol.id}`,
      alice.token,
      undefined,
      204,
    );
    await request("PUT", `/api/blocks/${bob.id}`, alice.token, undefined, 204);
    await request("GET", `/api/users/${alice.id}`, bob.token, undefined, 404);
    await request("GET", `/api/posts/${ap.id}`, bob.token, undefined, 404);
    await request(
      "DELETE",
      `/api/blocks/${bob.id}`,
      alice.token,
      undefined,
      204,
    );
    await request(
      "POST",
      "/api/friend-requests",
      alice.token,
      { recipientId: bob.id },
      201,
    );
    const cross = await (
      await request(
        "POST",
        "/api/friend-requests",
        bob.token,
        { recipientId: alice.id },
        201,
      )
    ).json();
    assert.equal(cross.status, "accepted");
    await request(
      "DELETE",
      `/api/friendships/${bob.id}`,
      alice.token,
      undefined,
      204,
    );
    const profile = await app.request("/api/me/profile-image", {
      method: "PUT",
      headers: {
        Authorization: "Bearer " + alice.token,
        "Content-Type": "image/jpeg",
      },
      body: profileImage,
    });
    assert.equal(profile.status, 204);
    for (const body of [jpegImage(513, 1), new TextEncoder().encode("jpeg")]) {
      const rejected = await app.request("/api/me/profile-image", {
        method: "PUT",
        headers: {
          Authorization: "Bearer " + alice.token,
          "Content-Type": "image/jpeg",
        },
        body,
      });
      assert.equal(rejected.status, 400);
      assert.deepEqual(await rejected.json(), {
        error: "invalid_profile_image",
      });
    }
    const me = await (await request("GET", "/api/me", alice.token)).json();
    await request("GET", me.profileImageUrl);
    await request(
      "DELETE",
      "/api/me/profile-image",
      alice.token,
      undefined,
      204,
    );
    await request("GET", me.profileImageUrl, "", undefined, 403);
    await request(
      "DELETE",
      `/api/comments/${comment.id}`,
      alice.token,
      undefined,
      204,
    );
    await request(
      "DELETE",
      `/api/realmojis/${r2.id}`,
      alice.token,
      undefined,
      204,
    );
    await request("PATCH", `/api/posts/${ap.id}`, alice.token, {
      caption: "updated",
    });
    await request("DELETE", `/api/posts/${ap.id}`, alice.token, undefined, 204);
    await request("GET", ap.imageUrl, "", undefined, 403);
    const retry = await post(alice.token);
    await request(
      "DELETE",
      `/api/posts/${retry.id}`,
      alice.token,
      undefined,
      204,
    );
    assert.equal(
      (
        await (
          await request("GET", "/api/posting-window/current", alice.token)
        ).json()
      ).canPost,
      false,
    );
    const controller = new AbortController();
    const sse = await app.request("/api/notifications", {
      headers: { Authorization: "Bearer " + bob.token },
      signal: controller.signal,
    });
    assert.equal(sse.headers.get("content-type"), "text/event-stream");
    const event = sse.text();
    await request(
      "POST",
      "/api/admin/posting-window",
      admin,
      { region: "asia", localDate: "2026-10-05" },
      201,
    );
    const data = await event;
    assert.match(data, /^data: /);
    assert.notEqual(JSON.parse(data.slice(6)).postingWindowId, window.id);
    controller.abort();
    await request(
      "PATCH",
      `/api/posts/${bp.id}`,
      bob.token,
      { caption: "closed" },
      403,
    );
    const newLogin = await (
      await request("POST", "/api/auth/login", "", {
        username: "bob",
        secretKey: bob.secretKey,
      })
    ).json();
    await request("GET", "/api/me", bob.token, undefined, 401);
    await request("POST", "/api/auth/logout", newLogin.token, undefined, 204);
    await request(
      "POST",
      "/api/me/account-deletion",
      alice.token,
      undefined,
      204,
    );
    await request("GET", "/api/me", alice.token, undefined, 401);
    await request("POST", "/api/admin/auth/logout", admin, undefined, 204);
    await request(
      "PUT",
      `/api/admin/users/${bob.id}/certification`,
      admin,
      undefined,
      401,
    );
  },
);

test(
  "Go pagination, visibility, concurrent registration and transaction rollback",
  { skip: !dsn },
  async (t) => {
    const db = new pg.Pool({ connectionString: dsn, max: 30 });
    t.after(() => db.end());
    await db.query(
      await readFile(
        new URL("../../sql/001-schema.sql", import.meta.url),
        "utf8",
      ),
    );
    const app = process.env.ISUREAL_TEST_BASE_URL
      ? {
          request: (path: string, init?: RequestInit) =>
            fetch(new URL(path, process.env.ISUREAL_TEST_BASE_URL), init),
        }
      : createApp(db, "asia", { logOutput: { write: () => true } });
    const req = async (path: string, method = "GET", body?: unknown) =>
      app.request(path, {
        method,
        headers: {
          Authorization: "Bearer viewer-token",
          "Content-Type": "application/json",
        },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
    const registrations = await Promise.all(
      Array.from({ length: 8 }, () =>
        req("/api/auth/register", "POST", {
          username: "concurrent",
          displayName: "Concurrent",
          region: "asia",
        }),
      ),
    );
    assert.deepEqual(
      registrations.map((r) => r.status).sort(),
      [201, 409, 409, 409, 409, 409, 409, 409],
    );
    for (const id of ["viewer", "a", "b", "c", "d"])
      await db.query(
        "INSERT INTO users(id,username,secret_key_hash,display_name,region) VALUES($1::text,$1::text,$2,$1::text,'asia')",
        [id, hash("secret")],
      );
    await db.query(
      "INSERT INTO login_sessions(id,user_id,token_hash) VALUES('session','viewer',$1)",
      [hash("viewer-token")],
    );
    await db.query(
      "INSERT INTO posting_windows(id,region,local_date,starts_at,on_time_deadline) VALUES('w','asia','2026-10-04','2026-10-04T00:00:00Z','2026-10-04T00:02:00Z')",
    );
    const addPost = async (
      id: string,
      author: string,
      visibility: string,
      at = "2026-10-04T00:01:00.123456Z",
    ) => {
      await db.query(
        "INSERT INTO image_objects(id,owner_user_id,data,media_type) VALUES($1,$2,$3,'image/jpeg')",
        [id, author, Buffer.from("jpeg")],
      );
      await db.query(
        "INSERT INTO posts(id,author_id,posting_window_id,image_id,visibility,location_kind,latitude,longitude,posted_at) VALUES($1,$2,'w',$1,$3,'exact',35.123456,139.654321,$4)",
        [id, author, visibility, at],
      );
    };
    await addPost("a", "a", "friends");
    await addPost("b", "b", "friends_of_friends");
    await addPost("c", "c", "public");
    await addPost("d", "d", "public");
    await db.query(
      "INSERT INTO friendships(id,user_low_id,user_high_id) VALUES('va','a','viewer'),('ab','a','b')",
    );
    await db.query(
      "INSERT INTO follows(id,follower_id,followee_id) VALUES('vc','viewer','c'),('vb','viewer','b')",
    );
    await db.query(
      "INSERT INTO block_relations(id,blocker_id,blocked_id) VALUES('dv','d','viewer')",
    );
    const page = async (path: string) => {
      const r = await req(path);
      assert.equal(r.status, 200, await r.clone().text());
      return r.json();
    };
    const ids = async (feed: string) =>
      (await page("/api/feeds/" + feed)).items.map((p: { id: string }) => p.id);
    assert.deepEqual(await ids("public"), []);
    await addPost("own", "viewer", "public");
    assert.deepEqual(await ids("friends"), ["a"]);
    assert.deepEqual(await ids("friends-of-friends"), ["b"]);
    assert.deepEqual(await ids("following"), ["c", "b"]);
    assert.deepEqual(await ids("public"), ["c"]);
    const exact = await (await req("/api/posts/c")).json();
    assert.deepEqual(exact.location, {
      kind: "exact",
      latitude: 35.123456,
      longitude: 139.654321,
    });
    assert.equal((await req("/api/posts/b/comments")).status, 403);
    assert.equal((await req("/api/posts/b/realmojis")).status, 200);
    for (let i = 0; i < 55; i++)
      await addPost(
        "public-" + String(i).padStart(2, "0"),
        "c",
        "public",
        `2026-10-04T00:01:00.${String(123457 + i).padStart(6, "0")}Z`,
      );
    for (let i = 0; i < 55; i++)
      await addPost("hidden-" + i, "c", "friends", "2026-10-04T00:01:30Z");
    const first = await page("/api/feeds/public");
    assert.equal(first.items.length, 50);
    assert.equal(first.hasMore, true);
    const last = first.items.at(-1),
      second = await page(
        "/api/feeds/public?" +
          new URLSearchParams({
            beforePostedAt: last.postedAt,
            beforeId: last.id,
          }),
      );
    assert.equal(second.items.length, 6);
    assert.equal(second.hasMore, false);
    assert.equal(
      new Set([...first.items, ...second.items].map((p) => p.id)).size,
      56,
    );
    await db.query(
      "UPDATE users SET display_name='Straße',bio='private' WHERE id='c'",
    );
    const search = await page("/api/users?q=STRASSE");
    assert.equal(search.items[0].id, "c");
    assert.equal(search.items[0].bio, "");
    await db.query(
      "CREATE FUNCTION reject_node_test_session() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'insertion rejected'; END $$; CREATE TRIGGER reject_node_test_session BEFORE INSERT ON login_sessions FOR EACH ROW EXECUTE FUNCTION reject_node_test_session()",
    );
    try {
      const login = await req("/api/auth/login", "POST", {
        username: "viewer",
        secretKey: "secret",
      });
      assert.equal(login.status, 500);
      assert.equal((await req("/api/me")).status, 200);
      assert.equal(
        (await db.query("SELECT active FROM login_sessions WHERE id='session'"))
          .rows[0].active,
        true,
      );
    } finally {
      await db.query("DROP FUNCTION reject_node_test_session() CASCADE");
    }
  },
);
test(
  "IP bans and action logs follow Go",
  { skip: !dsn || !!process.env.ISUREAL_TEST_BASE_URL },
  async (t) => {
    const db = new pg.Pool({ connectionString: dsn, max: 30 });
    t.after(() => db.end());
    await db.query(
      await readFile(
        new URL("../../sql/001-schema.sql", import.meta.url),
        "utf8",
      ),
    );
    const lines: string[] = [];
    const app = createApp(db, "asia", {
      logOutput: { write: (line: string) => lines.push(line) },
    });
    const lastLog = () => JSON.parse(lines.at(-1)!);
    const login = (ip: string) =>
      app.request("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-Client-IP": ip },
        body: JSON.stringify({ username: "nobody", secretKey: "x" }),
      });
    assert.equal((await login("192.0.2.1")).status, 401);
    assert.equal(
      lines.at(-1),
      '{"action":"login","ip":"192.0.2.1","level":"info","status":401,"user_id":null,"username":"nobody"}\n',
    );
    await app.request("/api/me", { headers: { "X-Client-IP": "192.0.2.1" } });
    assert.deepEqual(lastLog(), {
      action: "get_me",
      ip: "192.0.2.1",
      level: "debug",
      status: 401,
      user_id: null,
    });
    await app.request("/api/unknown", { method: "POST" });
    assert.equal(lastLog().action, null);
    assert.equal(lastLog().status, 404);

    await db.query(
      "INSERT INTO ip_bans (ip, banned_until) VALUES ('192.0.2.1', clock_timestamp() + interval '1 minute'), ('192.0.2.2', clock_timestamp() - interval '1 second')",
    );
    await new Promise((resolve) => setTimeout(resolve, 250));
    const banned = await login("192.0.2.1");
    assert.equal(banned.status, 403);
    assert.deepEqual(await banned.json(), { error: "ip_banned" });
    assert.deepEqual(lastLog(), {
      action: "login",
      ip: "192.0.2.1",
      level: "info",
      status: 403,
      user_id: null,
      username: null,
    });
    assert.equal((await login("192.0.2.2")).status, 401);

    const quiet: string[] = [];
    const infoApp = createApp(db, "asia", {
      logLevel: "info",
      logOutput: { write: (line: string) => quiet.push(line) },
    });
    await infoApp.request("/api/me");
    assert.equal(quiet.length, 0);
    await infoApp.request("/api/auth/logout", { method: "POST" });
    assert.equal(JSON.parse(quiet[0]).action, "logout");
  },
);
