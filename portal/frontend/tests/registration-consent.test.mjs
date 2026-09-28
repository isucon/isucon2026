import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import test from "node:test";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server.js";
import ts from "typescript";

const componentURL = new URL("../app/javascript/RegistrationConsentFields.tsx", import.meta.url);
const { outputText } = ts.transpileModule(await readFile(componentURL, "utf8"), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.React, esModuleInterop: true },
});
const exports = {};
new Function("require", "exports", outputText)(createRequire(componentURL), exports);

test("new registration requires explicit terms/privacy, sponsor remains optional and unchecked", () => {
  const html = renderToStaticMarkup(
    React.createElement(exports.RegistrationConsentFields, { value: {}, onChange() {} }),
  );
  assert.match(html, /name="agreeTerms"[^>]*required/);
  assert.match(html, /name="agreePrivacy"[^>]*required/);
  assert.doesNotMatch(html.match(/<input[^>]+name="agreeSponsor"[^>]*>/)?.[0] ?? "", /required|checked/);
  assert.match(html, /privacy.html/);
});
