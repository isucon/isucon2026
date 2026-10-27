import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import test from "node:test";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server.js";
import { StaticRouter } from "react-router-dom/server.js";
import ts from "typescript";

const url = new URL("../app/javascript/Clarification.tsx", import.meta.url);
const { outputText } = ts.transpileModule(await readFile(url, "utf8"), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.React, esModuleInterop: true },
});
const exports = {};
const require = createRequire(url);
new Function("require", "exports", outputText)((name) => {
  if (name === "./Timestamp") return { Timestamp: () => null };
  if (name === "./TimeDuration") return { TimeDuration: () => null };
  return require(name);
}, exports);

for (const admin of [true, false]) {
  for (const excluded of [true, false, undefined]) {
    test(`Clar support tag: admin=${admin}, excluded=${excluded}`, () => {
      const clarification = {
        id: 1n,
        question: "help",
        originalQuestion: "help",
        team: excluded === undefined ? undefined : { id: 90001n, name: "nomeaning", onsiteSupportExcluded: excluded },
      };
      const html = renderToStaticMarkup(
        React.createElement(StaticRouter, null, React.createElement(exports.Clarification, { clarification, admin })),
      );
      assert.equal(html.includes("当日サポート外"), admin && excluded === true);
      if (admin && excluded) assert.match(html, /class="tag is-warning[^\"]*"[^>]*>当日サポート外/);
    });
  }
}
