import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import test from "node:test";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server.js";
import ts from "typescript";

const componentURL = new URL("../app/javascript/contestant/ContestantContestantInstanceList.tsx", import.meta.url);
const { outputText } = ts.transpileModule(await readFile(componentURL, "utf8"), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.React, esModuleInterop: true },
});
const exports = {};
const require = createRequire(componentURL);
new Function("require", "exports", outputText)((name) => {
  if (name === "../ContestantInstanceStatus") return { ContestantInstanceStatus: () => null };
  return require(name);
}, exports);

test("server list shows names by instance number regardless of row order", () => {
  const instances = [4n, 2n, 1n, 3n].map((number) => ({
    id: number,
    number,
    publicIpv4Address: `203.0.113.${number}`,
    privateIpv4Address: `192.168.${number}.10`,
    status: 1,
  }));
  const html = renderToStaticMarkup(
    React.createElement(exports.ContestantContestantInstanceList, {
      session: { contestantInstances: instances },
      client: {},
    }),
  );
  assert.match(html, /<th>name<\/th>/);
  assert.doesNotMatch(html, /<th>#<\/th>/);
  assert.deepEqual(
    [...html.matchAll(/<tr><td>([^<]+)<\/td>/g)].map((match) => match[1]),
    ["asia2", "europe1", "asia1", "america1"],
  );
  for (const instance of instances) {
    assert.ok(html.includes(`ssh isucon@${instance.publicIpv4Address}`));
  }
});
