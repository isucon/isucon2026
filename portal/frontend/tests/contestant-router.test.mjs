import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import ts from "typescript";
import React from "react";
import TestRenderer from "react-test-renderer";

const { outputText } = ts.transpileModule(
  await readFile(new URL("../app/javascript/contestant/ContestantContestantInstanceList.tsx", import.meta.url), "utf8"),
  { compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.React, esModuleInterop: true } },
);
const exports = {};
new Function("require", "exports", outputText)((name) => {
  if (name === "react") return React;
  if (name === "../ContestantInstanceStatus") return { ContestantInstanceStatus: () => null };
  throw new Error(name);
}, exports);

for (const ip of ["203.0.113.5", "", undefined]) {
  test(`router preview with IP ${ip}`, async () => {
    let renderer;
    await TestRenderer.act(async () => {
      renderer = TestRenderer.create(
        React.createElement(exports.ContestantContestantInstanceList, {
          session: { contestantInstances: [], routerPublicIpv4Address: ip },
          client: { getCloudFormation: async () => ({ template: "test" }) },
        }),
      );
    });
    try {
      assert.ok(renderer.root.findAllByType("h1").some((h) => h.children.join("") === "表示確認用"));
      const links = renderer.root.findAllByType("a").filter((a) => a.props.href.startsWith("http://"));
      assert.equal(links.length, ip ? 1 : 0);
      if (ip) {
        const url = `http://${ip}:8080`;
        assert.equal(links[0].props.href, url);
        assert.equal(links[0].children.join(""), url);
      } else {
        assert.match(JSON.stringify(renderer.toJSON()), /未登録/);
      }
    } finally {
      renderer.unmount();
    }
  });
}
