import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFile, readdir } from "node:fs/promises";
import { createRequire } from "node:module";
import test from "node:test";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server.js";
import ts from "typescript";

const componentURL = new URL("../app/javascript/Landing.tsx", import.meta.url);
const { outputText } = ts.transpileModule(await readFile(componentURL, "utf8"), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.React, esModuleInterop: true },
});
const exports = {};
new Function("require", "exports", outputText)(createRequire(componentURL), exports);

for (const [name, session] of [
  ["registered", { contestant: {} }],
  ["registration open", { contest: { status: 1 } }],
  ["registration closed", { contest: { status: 0 } }],
]) {
  test(`landing shows the October 31 event date when ${name}`, () => {
    const html = renderToStaticMarkup(React.createElement(exports.Landing, { session, client: {} }));
    assert.match(html, /2026年10月31日（土）/);
    assert.match(html, /dateTime="2026-10-31"/);
    assert.doesNotMatch(html, /2024年|予選参加確定枠/);
  });
}

const publicDir = new URL("../public/", import.meta.url);
test("all device PNG icons retain their declared dimensions", async () => {
  for (const name of await readdir(publicDir)) {
    if (!/^(icon-|android-chrome-|apple-touch-icon|site-tile-).*\.png$/.test(name)) continue;
    const png = await readFile(new URL(name, publicDir));
    assert.equal(png.subarray(1, 4).toString(), "PNG", name);
    const size = name.match(/(\d+)x(\d+)/);
    assert.equal(png.readUInt32BE(16), size ? Number(size[1]) : 180, name);
    assert.equal(png.readUInt32BE(20), size ? Number(size[2]) : 180, name);
  }
});

test("device icon no longer contains the ISUCON14 artwork", async () => {
  const png = await readFile(new URL("icon-192x192.png", publicDir));
  assert.notEqual(
    createHash("sha256").update(png).digest("hex"),
    "6edf7d4dc47534f25277f113ab0f4185502a22a23450690b3a54cee021637ad6",
  );
});

test("favicon includes 16, 32 and 48 pixel images with valid PNG payloads", async () => {
  const ico = await readFile(new URL("favicon.ico", publicDir));
  assert.equal(ico.readUInt16LE(2), 1);
  assert.equal(ico.readUInt16LE(4), 3);
  for (const [i, size] of [16, 32, 48].entries()) {
    const entry = 6 + 16 * i;
    assert.equal(ico[entry], size);
    assert.equal(ico[entry + 1], size);
    const length = ico.readUInt32LE(entry + 8);
    const offset = ico.readUInt32LE(entry + 12);
    assert.ok(offset + length <= ico.length);
    assert.equal(ico.subarray(offset + 1, offset + 4).toString(), "PNG");
    assert.equal(ico.readUInt32BE(offset + 16), size);
  }
});
