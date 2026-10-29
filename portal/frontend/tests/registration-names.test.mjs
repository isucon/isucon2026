import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import test from "node:test";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server.js";
import ts from "typescript";

async function load(path) {
  const url = new URL(path, import.meta.url);
  const { outputText } = ts.transpileModule(await readFile(url, "utf8"), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.React, esModuleInterop: true },
  });
  const exports = {};
  const require = createRequire(url);
  new Function("require", "exports", outputText)((name) => {
    if (name === "react-router-dom") return { useNavigate: () => () => {} };
    if (name.includes("ErrorMessage") || name.includes("RegistrationConsentFields")) return {};
    if (name.includes("/proto/")) return { GetRegistrationSessionResponse_Status: { JOINED: 1 } };
    return require(name);
  }, exports);
  return exports;
}

const { RegistrationForm } = await load("../app/javascript/RegistrationForm.tsx");
const { AdminTeamEdit } = await load("../app/javascript/admin/AdminTeamEdit.tsx");

function assertLimited(html, id) {
  const input = html.match(new RegExp(`<input[^>]*id="${id}"[^>]*>`))?.[0];
  assert.ok(input, `missing ${id}`);
  assert.match(input, /maxLength="255"/i);
  assert.match(html, /255文字以内/);
}

test("registration team and contestant fields explain and enforce the name limit", () => {
  const form = new RegistrationForm({ session: {}, registrationSession: {} });
  assertLimited(renderToStaticMarkup(form.renderTeamFormFields()), "fieldTeamName");
  assertLimited(renderToStaticMarkup(form.renderContestantFormFields()), "fieldName");
});

test("admin team and member fields explain and enforce the name limit", () => {
  const html = renderToStaticMarkup(
    React.createElement(AdminTeamEdit, {
      team: { id: 1n, name: "team", members: [{ id: 2n, name: "member", detail: {} }], detail: {} },
    }),
  );
  assertLimited(html, "AdminTeamEdit-1-name");
  assertLimited(html, "AdminTeamEdit-1-2-name");
});
