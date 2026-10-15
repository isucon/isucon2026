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

test("envcheck instructions describe resources without instance details", () => {
  const component = new exports.EnvCheck({ session: { team: {} }, client: {} });
  component.state.checkStatus = EnvCheckStatus.NOT_STARTED;
  const html = renderToStaticMarkup(component.render());
  assert.match(
    html,
    /このテンプレートではEC2 インスタンス、VPC、インターネットゲートウェイ、セキュリティグループ、また Availability Zone 情報取得に利用する Lambda 関数を作成します。/,
  );
  assert.doesNotMatch(html, /本番と同じ|c7i\.large|c6in\.large|5台|isucon2026-envcheck-|無料利用枠/);
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

test("only one SSH destination is displayed and one connection is required", () => {
  const component = new exports.EnvCheck({ session: { team: {} }, client: {} });
  component.state.checkStatus = EnvCheckStatus.CREATED_INSTANCE;
  component.state.instanceIPs = ["203.0.113.1", "203.0.113.2", "203.0.113.3", "203.0.113.4", "203.0.113.5"];
  const html = renderToStaticMarkup(component.render());
  assert.ok(html.includes("ssh -T isucon@203.0.113.1"));
  for (const ip of component.state.instanceIPs.slice(1)) assert.ok(!html.includes(ip));
  assert.match(html, /いずれか1台へのSSH接続/);
});

test("polling updates all SSH destinations and the actual progress", async () => {
  const component = new exports.EnvCheck({
    session: { team: {} },
    client: {
      getEnvCheckInformation: async () => ({
        template: "template",
        status: EnvCheckStatus.CREATED_INSTANCE,
        instanceIps: ["203.0.113.1", "203.0.113.2"],
      }),
    },
  });
  component.setState = (state) => Object.assign(component.state, state);
  assert.equal(await component.fetchEnvCheckInformation(), EnvCheckStatus.CREATED_INSTANCE);
  assert.deepEqual(component.state.instanceIPs, ["203.0.113.1", "203.0.113.2"]);
});
