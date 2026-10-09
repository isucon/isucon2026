import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import test from "node:test";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server.js";
import ts from "typescript";
import { EnvCheckStatus } from "../proto/isuxportal/resources/env_check_pb.js";

const componentURL = new URL("../app/javascript/EnvCheck.tsx", import.meta.url);
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

test("envcheck instructions describe all five paid instances", () => {
  const component = new exports.EnvCheck({ session: { team: {} }, client: {} });
  component.state.checkStatus = EnvCheckStatus.NOT_STARTED;
  const html = renderToStaticMarkup(component.render());
  assert.match(html, /c7i\.large/);
  assert.match(html, /c6in\.large/);
  assert.match(html, /5台/);
  assert.match(html, /料金/);
  assert.doesNotMatch(html, /無料利用枠/);
});

test("DONE returned by the API stops envcheck progress polling", async () => {
  const component = new exports.EnvCheck({ session: { team: {} }, client: {} });
  component.fetchEnvCheckInformation = async () => EnvCheckStatus.DONE;
  const originalSetInterval = globalThis.setInterval;
  let started = false;
  globalThis.setInterval = () => {
    started = true;
    return 1;
  };
  try {
    component.componentDidMount();
    await Promise.resolve();
    assert.equal(started, false);
  } finally {
    globalThis.setInterval = originalSetInterval;
  }
});
