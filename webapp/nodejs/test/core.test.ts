import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";
import {
  validUserText,
  validLocation,
  normalizeSearchText,
  imageSignature,
  imageSigningKey,
  canView,
  timestamp,
  addSeconds,
  micros,
} from "../src/core.js";

test("Go rune lengths and NFC case folding", () => {
  assert.equal(validUserText("😀".repeat(100), 1, 100), true);
  assert.equal(validUserText("😀".repeat(101), 1, 100), false);
  assert.equal(normalizeSearchText("Straße Σς e\u0301"), "strasse σσ é");
});
test("location variants retain Go null semantics", () => {
  assert.equal(validLocation({ kind: "none" }), true);
  assert.equal(validLocation({ kind: "none", latitude: 0 }), false);
  assert.equal(
    validLocation({ kind: "exact", latitude: 90, longitude: -180 }),
    true,
  );
  assert.equal(
    validLocation({ kind: "exact", latitude: 91, longitude: 0 }),
    false,
  );
  assert.equal(validLocation({ kind: "approximate", locality: "" }), false);
});
test("image signature matches the Go reference test vector", () => {
  const key = createHash("sha256").update("test-secret").digest();
  assert.equal(
    imageSignature(
      key,
      "550e8400-e29b-41d4-a716-446655440000",
      1790553600n,
    ).toString("base64url"),
    "qv9Jrl_ciG5MowUjgzZ4_bYd2cxIxvpS7zOcTSqxx84",
  );
  assert.deepEqual(imageSigningKey(key.toString("hex")), key);
  assert.throws(() => imageSigningKey("ab".repeat(31)));
});
test("visibility preserves own post exception and friends of friends", () => {
  const viewer = {
    id: "a",
    hasPostedToday: false,
    friends: new Set(["b"]),
    friendsOfFriends: new Set(["c"]),
    blocked: new Set(["d"]),
  };
  assert.equal(
    canView(viewer, {
      author_id: "a",
      window_ended_at: "ended",
      visibility: "friends",
    }),
    true,
  );
  assert.equal(
    canView(viewer, {
      author_id: "b",
      window_ended_at: null,
      visibility: "public",
    }),
    false,
  );
  viewer.hasPostedToday = true;
  assert.equal(
    canView(viewer, {
      author_id: "c",
      window_ended_at: null,
      visibility: "friends_of_friends",
    }),
    true,
  );
  assert.equal(
    canView(viewer, {
      author_id: "c",
      window_ended_at: null,
      visibility: "friends",
    }),
    false,
  );
  assert.equal(
    canView(viewer, {
      author_id: "d",
      window_ended_at: null,
      visibility: "public",
    }),
    false,
  );
});
test("database timestamps preserve microseconds for cursors and deadlines", () => {
  assert.equal(
    timestamp("2026-10-04 12:00:00.123456+09"),
    "2026-10-04T03:00:00.123456Z",
  );
  assert.equal(
    addSeconds("2026-10-04T03:00:00.123456Z", 1),
    "2026-10-04T03:00:01.123456Z",
  );
  assert.equal(
    micros("2026-10-04T03:00:00.123457Z") -
      micros("2026-10-04T03:00:00.123456Z"),
    1n,
  );
});

test("Go patterns reject trailing newlines", async () => {
  const { usernamePattern } = await import("../src/core.js");
  assert.equal(usernamePattern.test("alice\n"), false);
  assert.throws(() => imageSigningKey("ab".repeat(32) + "\n"));
});
test("PostgreSQL numeric coordinates serialize as JSON numbers", async () => {
  await import("../src/db.js");
  const { default: pg } = await import("pg");
  assert.equal(pg.types.getTypeParser(1700)("35.123456"), 35.123456);
});
