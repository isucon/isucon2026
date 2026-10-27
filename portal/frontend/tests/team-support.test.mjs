import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import test from "node:test";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server.js";
import ts from "typescript";

const url = new URL("../app/javascript/admin/AdminTeamTagList.tsx", import.meta.url);
const { outputText } = ts.transpileModule(await readFile(url, "utf8"), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.React, esModuleInterop: true },
});
const exports = {};
new Function("require", "exports", outputText)(createRequire(url), exports);

test("support exclusion is displayed independently from disqualification", () => {
  for (const excluded of [true, false]) {
    const html = renderToStaticMarkup(
      React.createElement(exports.AdminTeamTagList, { onsiteSupportExcluded: excluded }),
    );
    assert.equal(html.includes("当日サポート外"), excluded);
    assert.ok(!html.includes("失格"));
  }
});
