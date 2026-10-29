import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import test from "node:test";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server.js";
import ts from "typescript";
const sessionProto = { GetRegistrationSessionResponse_Status: { NOT_LOGGED_IN: 2 } };

const url = new URL("../app/javascript/Registration.tsx", import.meta.url);
const { outputText } = ts.transpileModule(await readFile(url, "utf8"), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.React, esModuleInterop: true },
});
const exports = {};
const require = createRequire(url);
new Function("require", "exports", outputText)((name) => {
  if (name.endsWith("/session_pb")) return sessionProto;
  if (name.startsWith("./")) return new Proxy({}, { get: () => () => null });
  return require(name);
}, exports);
const timestamp = (iso) => ({ seconds: BigInt(Date.parse(iso) / 1000), nanos: 0 });
function render(contest) {
  globalThis.document = { location: { search: "" } };
  const component = new exports.Registration({ session: { contest }, client: {} });
  component.state.registrationSession = { status: sessionProto.GetRegistrationSessionResponse_Status.NOT_LOGGED_IN };
  return renderToStaticMarkup(component.render());
}
test("registration shows each configured deadline in JST with its year and weekday", () => {
  const html = render({
    registrationClosesAt: timestamp("2026-10-28T10:00:00Z"),
    registrationInvitationClosesAt: timestamp("2026-10-29T10:00:00Z"),
    registrationUpdateClosesAt: timestamp("2026-10-30T10:00:00Z"),
  });
  assert.match(html, /チームの新規登録[^<]*2026年10月28日（水）19:00（日本時間）/);
  assert.match(html, /参加メンバーの追加[^<]*2026年10月29日（木）19:00（日本時間）/);
  assert.match(html, /登録内容の変更[^<]*2026年10月30日（金）19:00（日本時間）/);
});
test("missing deadlines never invent a date or claim unlimited acceptance", () => {
  const html = render({});
  assert.doesNotMatch(html, /11月7日|まで可能|無期限/);
  assert.match(html, /締切は設定されていません/);
});
