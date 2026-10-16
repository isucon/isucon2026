import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import test from "node:test";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server.js";
import ts from "typescript";
import { EnvCheckStatus } from "../proto/isuxportal/resources/env_check_pb.js";

const componentURL = new URL("../app/javascript/RegistrationStatus.tsx", import.meta.url);
const { outputText } = ts.transpileModule(await readFile(componentURL, "utf8"), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.React, esModuleInterop: true },
});
const exports = {};
const require = createRequire(componentURL);
new Function("require", "exports", outputText)((name) => {
  if (name === "./ErrorMessage") return { ErrorMessage: () => null };
  if (name.endsWith("/env_check_pb")) return { EnvCheckStatus };
  return require(name);
}, exports);

const render = (status, members = [{ detail: { isSshKeyRegistered: true, isDiscordGuildMember: true } }]) => {
  const component = new exports.RegistrationStatus({
    registrationSession: { team: { members }, envCheckStatus: status },
  });
  return renderToStaticMarkup(component.renderStatus());
};
for (const status of [EnvCheckStatus.NOT_STARTED, EnvCheckStatus.CREATED_INSTANCE]) {
  test(`unfinished environment ${status} is not ready`, () => {
    const html = render(status);
    assert.match(html, /is-danger/);
    assert.match(html, /競技環境確認/);
    assert.doesNotMatch(html, /準備が整っています/);
  });
}
test("all completed requirements are ready", () => {
  assert.match(render(EnvCheckStatus.DONE), /準備が整っています/);
});
test("unpublished envcheck is described as pending without claiming completion", () => {
  const html = render(EnvCheckStatus.PREPARING);
  assert.match(html, /公開準備中/);
  assert.doesNotMatch(html, /準備が整っています|is-danger/);
});
test("every member must have an SSH key and Discord membership", () => {
  const html = render(EnvCheckStatus.DONE, [
    { detail: { isSshKeyRegistered: true, isDiscordGuildMember: true } },
    { detail: { isSshKeyRegistered: false, isDiscordGuildMember: false } },
  ]);
  assert.match(html, /is-danger/);
  assert.match(html, /SSH/);
  assert.match(html, /Discord/);
  assert.doesNotMatch(html, /準備が整っています/);
});
test("Discord missing alone is not ready", () => {
  assert.match(
    render(EnvCheckStatus.DONE, [{ detail: { isSshKeyRegistered: true, isDiscordGuildMember: false } }]),
    /is-danger/,
  );
});

test("missing member details cannot be treated as ready", () => {
  assert.match(render(EnvCheckStatus.DONE, [{}]), /is-danger/);
  assert.match(render(EnvCheckStatus.DONE, []), /is-danger/);
});
