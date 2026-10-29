import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import React from "react";
import TestRenderer from "react-test-renderer";
import * as Router from "react-router-dom";
import ts from "typescript";

const source = await readFile(new URL("../app/javascript/admin/AdminNavbar.tsx", import.meta.url), "utf8");
const { outputText } = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.React, esModuleInterop: true },
});
const exports = {};
new Function("require", "exports", outputText)((name) => {
  if (name === "react") return React;
  if (name === "react-router-dom") return Router;
  if (name === "@rails/ujs") return {};
  throw new Error(`Unexpected import: ${name}`);
}, exports);

for (const path of ["/admin", "/admin/teams"]) {
  test(`admin brand navigates from ${path} to the admin dashboard`, () => {
    let renderer;
    let location;
    function Location() {
      location = Router.useLocation();
      return null;
    }
    TestRenderer.act(() => {
      renderer = TestRenderer.create(
        React.createElement(
          Router.MemoryRouter,
          { initialEntries: [path] },
          React.createElement(exports.AdminNavbar, { session: {}, client: {} }),
          React.createElement(Location),
        ),
      );
    });
    try {
      const brand = renderer.root.findAllByType("a").find((link) => link.children.includes("ISUCON2026 Admin"));
      TestRenderer.act(() => {
        brand.props.onClick({ button: 0, preventDefault() {} });
      });
      assert.equal(location.pathname, "/admin");
    } finally {
      TestRenderer.act(() => renderer.unmount());
    }
  });
}
