import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import test from "node:test";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server.js";
import ts from "typescript";

const componentURL = new URL("../app/javascript/contestant/ContestantBenchmarkJobForm.tsx", import.meta.url);
const { outputText } = ts.transpileModule(await readFile(componentURL, "utf8"), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.React, esModuleInterop: true },
});
const exports = {};
const require = createRequire(componentURL);
new Function("require", "exports", outputText)((name) => {
  if (name === "@bufbuild/protobuf") return { create: (_schema, fields) => fields };
  if (name === "../ErrorMessage") return { ErrorMessage: () => null };
  if (name.endsWith("/benchmark_pb")) return { EnqueueBenchmarkJobRequestSchema: {} };
  return require(name);
}, exports);
function render(instances) {
  let tree, request;
  let storageReads = 0;
  globalThis.window = {
    localStorage: {
      getItem: () => {
        storageReads++;
        return "999";
      },
    },
  };
  const client = {
    enqueueBenchmarkJob: async (payload) => {
      request = payload;
      return { job: { id: 123n } };
    },
  };
  const html = renderToStaticMarkup(
    React.createElement(() => {
      tree = exports.ContestantBenchmarkJobForm({ session: { contestantInstances: instances }, client });
      return tree;
    }),
  );
  delete globalThis.window;
  function find(element, type) {
    if (element?.type === type) return element;
    for (const child of React.Children.toArray(element?.props?.children)) {
      const found = find(child, type);
      if (found) return found;
    }
  }
  return { html, storageReads, form: find(tree, "form"), button: find(tree, "button"), request: () => request };
}
test("enqueue has no selector or remembered target and submits the first registered target", async () => {
  const view = render([
    { id: 42n, number: 1n },
    { id: 99n, number: 2n },
  ]);
  assert.doesNotMatch(view.html, /<select|<option|最後に利用したサーバー|サーバーを選択/);
  assert.match(view.html, /Enqueue を押すと計測を開始します。/);
  assert.equal(view.storageReads, 0);
  assert.equal(view.button.props.disabled, false);
  await view.form.props.onSubmit({ preventDefault() {} });
  assert.equal(view.request().targetId, 42n);
});
test("enqueue is unavailable until a target is registered", async () => {
  const view = render([]);
  assert.equal(view.button.props.disabled, true);
  await view.form.props.onSubmit({ preventDefault() {} });
  assert.equal(view.request(), undefined);
});
