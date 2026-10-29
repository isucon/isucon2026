import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import ts from "typescript";

const { outputText } = ts.transpileModule(
  await readFile(new URL("../app/javascript/admin/attentionLevel.ts", import.meta.url), "utf8"),
  {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  },
);
const exports = {};
new Function("exports", outputText)(exports);
const now = 1_000_000;
const ago = (ms) => ({ seconds: BigInt((now - ms) / 1000), nanos: 0 });
const empty = { unansweredClarificationCount: 0n, pendingBenchmarkJobCount: 0n };
for (const [name, data, expected] of [
  ["empty", empty, "normal"],
  [
    "unanswered clar",
    { ...empty, unansweredClarificationCount: 1n, earliestUnansweredClarificationAt: ago(299000) },
    "warning",
  ],
  [
    "clar at five minutes",
    { ...empty, unansweredClarificationCount: 1n, earliestUnansweredClarificationAt: ago(300000) },
    "danger",
  ],
  [
    "ordinary pending",
    { ...empty, pendingBenchmarkJobCount: 100n, earliestPendingBenchmarkJobAt: ago(59000) },
    "normal",
  ],
  [
    "pending at one minute",
    { ...empty, pendingBenchmarkJobCount: 1n, earliestPendingBenchmarkJobAt: ago(60000) },
    "danger",
  ],
  [
    "pending takes precedence",
    { unansweredClarificationCount: 1n, pendingBenchmarkJobCount: 1n, earliestPendingBenchmarkJobAt: ago(60000) },
    "danger",
  ],
  [
    "resolved ignores old timestamps",
    { ...empty, earliestPendingBenchmarkJobAt: ago(60000), earliestUnansweredClarificationAt: ago(300000) },
    "normal",
  ],
]) {
  test(name, () => assert.equal(exports.attentionLevel(data, now), expected));
}

const React = (await import("react")).default;
const TestRenderer = (await import("react-test-renderer")).default;
const Router = await import("react-router-dom");
const component = ts.transpileModule(
  await readFile(new URL("../app/javascript/admin/AdminAttention.tsx", import.meta.url), "utf8"),
  {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2020,
      jsx: ts.JsxEmit.React,
      esModuleInterop: true,
    },
  },
);
const sound = ts.transpileModule(
  await readFile(new URL("../app/javascript/admin/AdminAttentionSound.tsx", import.meta.url), "utf8"),
  {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2020,
      jsx: ts.JsxEmit.React,
      esModuleInterop: true,
    },
  },
);
const soundComponents = {};
new Function("require", "exports", sound.outputText)(() => React, soundComponents);
const components = {};
new Function("require", "exports", component.outputText)((name) => {
  if (name === "react") return React;
  if (name === "react-router-dom") return Router;
  if (name === "./attentionLevel") return exports;
  if (name === "./AdminAttentionSound") return soundComponents;
  throw new Error(name);
}, components);

test("polling retains warnings on failure, recovers, and cleans up on unmount", async () => {
  const classes = new Set();
  const timers = new Map();
  const listeners = new Map();
  globalThis.document = { body: { classList: { add: (v) => classes.add(v), remove: (v) => classes.delete(v) } } };
  globalThis.window = {
    setInterval: (fn, ms) => {
      timers.set(ms, fn);
      return ms;
    },
    clearInterval: (id) => timers.delete(id),
    addEventListener: (name, fn) => listeners.set(name, fn),
    removeEventListener: (name) => listeners.delete(name),
  };
  let response = { ...empty, unansweredClarificationCount: 1n };
  let fail = false;
  const client = {
    getDashboard: async () => {
      if (fail) throw new Error("offline");
      return response;
    },
  };
  let renderer;
  try {
    await TestRenderer.act(async () => {
      renderer = TestRenderer.create(
        React.createElement(
          Router.MemoryRouter,
          { initialEntries: ["/admin/teams"] },
          React.createElement(components.AdminAttention, { client }),
        ),
      );
    });
    assert.ok(classes.has("isux-admin-attention-warning"));
    assert.deepEqual(
      renderer.root.findAllByType("a").map((a) => a.props.href),
      ["/admin/clarifications?unanswered_only=1", "/admin/benchmark_jobs?status=0"],
    );
    fail = true;
    await TestRenderer.act(async () => {
      await timers.get(10000)();
    });
    assert.ok(classes.has("isux-admin-attention-warning"));
    assert.match(renderer.root.findByProps({ role: "status" }).children.join(""), /更新停止・最終取得/);
    fail = false;
    response = {
      ...empty,
      pendingBenchmarkJobCount: 1n,
      earliestPendingBenchmarkJobAt: { seconds: BigInt(Math.floor(Date.now() / 1000) - 61), nanos: 0 },
    };
    await TestRenderer.act(async () => {
      await timers.get(10000)();
    });
    assert.deepEqual([...classes], ["isux-admin-attention-danger"]);
    response = empty;
    await TestRenderer.act(async () => {
      await listeners.get("focus")();
    });
    assert.equal(classes.size, 0);
    assert.equal(renderer.root.findAllByProps({ role: "status" }).length, 0);
    response = { ...empty, unansweredClarificationCount: 1n };
    await TestRenderer.act(async () => {
      await timers.get(10000)();
    });
    TestRenderer.act(() => renderer.unmount());
    assert.equal(classes.size, 0);
    assert.equal(timers.size, 0);
    assert.equal(listeners.size, 0);
  } finally {
    if (renderer) TestRenderer.act(() => renderer.unmount());
    delete globalThis.window;
    delete globalThis.document;
  }
});

test("attention flashes every half second and respects reduced motion", async () => {
  const { compileString } = await import("sass");
  const source = await readFile(new URL("../app/javascript/application.scss", import.meta.url), "utf8");
  const { css } = compileString(source.slice(source.indexOf("body.isux-admin-attention-warning")));
  assert.match(css, /animation: isux-admin-attention 0\.5s ease-in-out infinite/);
  assert.match(css, /@media \(prefers-reduced-motion: reduce\)[\s\S]*animation: none/);
});
