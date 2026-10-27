import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { describe, it } from "node:test";
import jpeg from "jpeg-js";
import { parseQuery } from "../src/bind.ts";
import { imageSignature, imageSigningKey, signedImageURL } from "../src/image_signature.ts";
import { decodeJPEG } from "../src/jpeg.ts";
import { encodeJSON } from "../src/response.ts";
import { parsePostgresTimestamp, parseRFC3339, validDate } from "../src/time.ts";
import { validUserText, type User } from "../src/user.ts";

describe("parseQuery", () => {
  it("url.ParseQuery と同じ規則で分解する", () => {
    const { values, error } = parseQuery("a=1&a=2&b=x+y%21&c&=skip&&d=%zz&e=ok&f=%E3%81%82");
    assert.deepEqual(
      Object.fromEntries(values),
      { a: ["1", "2"], b: ["x y!"], c: [""], "": ["skip"], e: ["ok"], f: ["あ"] },
    );
    assert.equal(error, 'invalid URL escape "%zz"');
    assert.equal(parseQuery("a=1;b=2").error, "invalid semicolon separator in query");
  });
});

describe("time", () => {
  it("RFC 3339 の時刻だけを受け付け、マイクロ秒に切り詰める", () => {
    assert.equal(parseRFC3339("2026-10-26T01:02:03Z").toString(), "2026-10-26T01:02:03Z");
    assert.equal(parseRFC3339("2026-10-26T01:02:03.1234567891+09:00").toString(), "2026-10-25T16:02:03.123456Z");
    assert.equal(parseRFC3339("0001-01-01T00:00:00Z").toString(), "0001-01-01T00:00:00Z");
    for (const value of ["2026-10-26", "2026-10-26T01:02:03", "2026-02-30T00:00:00Z", "2026-10-26T24:00:00Z", "2026-10-26t01:02:03Z", ""]) {
      assert.throws(() => parseRFC3339(value), value);
    }
  });

  it("PostgreSQL の timestamptz を読み、RFC3339Nano の UTC で出す", () => {
    assert.equal(parsePostgresTimestamp("2026-10-26 01:02:03.120000+00").toString(), "2026-10-26T01:02:03.12Z");
    assert.equal(parsePostgresTimestamp("2026-10-26 10:02:03.000001+09").toString(), "2026-10-26T01:02:03.000001Z");
    assert.equal(parsePostgresTimestamp("2026-10-26 01:02:03-05:30").toString(), "2026-10-26T06:32:03Z");
    assert.equal(JSON.stringify({ t: parsePostgresTimestamp("2026-09-28 00:00:00.000001+00") }), '{"t":"2026-09-28T00:00:00.000001Z"}');
  });

  it("日付を検証する", () => {
    assert.ok(validDate(2024, 2, 29));
    assert.ok(!validDate(2026, 2, 29));
    assert.ok(!validDate(2026, 13, 1));
    assert.ok(!validDate(2026, 12, 32));
  });
});

describe("encodeJSON", () => {
  it("Go の encoding/json と同じく HTML の文字と U+2028/U+2029 をエスケープする", () => {
    assert.equal(
      encodeJSON({ a: "<b>&\b\f\\b  ", n: 1, f: 1e-7 }),
      '{"a":"\\u003cb\\u003e\\u0026\\b\\f\\\\b\\u2028\\u2029","n":1,"f":1e-7}\n',
    );
  });
});

describe("image signature", () => {
  it("Go と同じ署名を作る", () => {
    const key = createHash("sha256").update("test-secret").digest();
    const signature = imageSignature(key, "550e8400-e29b-41d4-a716-446655440000", 1790553600);
    assert.equal(signature.toString("base64url"), "qv9Jrl_ciG5MowUjgzZ4_bYd2cxIxvpS7zOcTSqxx84");
    assert.deepEqual(imageSigningKey(key.toString("hex")), key);
    for (const invalid of ["", "hash", "abcd"]) {
      assert.throws(() => imageSigningKey(invalid));
    }
  });

  it("24 時間有効な URL を作る", () => {
    const key = createHash("sha256").update("test-secret").digest();
    const user = { id: "owner", imageKey: key } as User;
    const before = Math.floor(Date.now() / 1000);
    const url = new URL(signedImageURL(user, "image-id"), "http://localhost");
    const after = Math.floor(Date.now() / 1000);
    const exp = Number(url.searchParams.get("exp"));
    assert.equal(url.pathname, "/api/images/image-id");
    assert.ok(exp >= before + 86400 && exp <= after + 86400);
    assert.equal(url.searchParams.get("sig"), imageSignature(key, "image-id", exp).toString("base64url"));
  });
});

describe("decodeJPEG", () => {
  it("JPEG の寸法を返し、JPEG でないものは拒否する", () => {
    const data = jpeg.encode({ width: 30, height: 20, data: Buffer.alloc(30 * 20 * 4, 128) }, 80).data;
    assert.deepEqual(decodeJPEG(data), { width: 30, height: 20 });
    assert.equal(decodeJPEG(data.subarray(0, data.length / 2)), null);
    assert.equal(decodeJPEG(Buffer.from("not a jpeg")), null);
    assert.equal(decodeJPEG(Buffer.alloc(0)), null);
  });
});

describe("validUserText", () => {
  it("コードポイント数で数える", () => {
    assert.ok(validUserText("😀".repeat(100), 1, 100));
    assert.ok(!validUserText("😀".repeat(101), 1, 100));
    assert.ok(!validUserText("", 1, 100));
    assert.ok(!validUserText("\ud800", 1, 100));
  });
});
