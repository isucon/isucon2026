import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";
import ts from "typescript";

const { outputText } = ts.transpileModule(await readFile(new URL("../sw/src/sw.ts", import.meta.url), "utf8"), {
  compilerOptions: { module: ts.ModuleKind.CommonJS },
});
for (const date of ["2026-10-28T00:00:00Z", "2026-10-31T00:00:00Z", "2027-01-01T00:00:00Z"]) {
  test(`activation retains registration and initializes storage on ${date}`, async () => {
    const handlers = {};
    let unregisters = 0;
    let opens = 0;
    const context = {
      exports: {},
      console: { log() {} },
      Date: class extends Date {
        constructor() {
          super(date);
        }
      },
      self: {
        skipWaiting() {},
        addEventListener: (name, fn) => {
          handlers[name] = fn;
        },
        registration: {
          unregister: () => {
            unregisters++;
          },
        },
      },
      require: (name) => {
        if (name === "workbox-core") return { clientsClaim() {} };
        if (name === "idb")
          return {
            openDB: async () => {
              opens++;
            },
          };
        return {};
      },
    };
    vm.runInNewContext(outputText, context);
    const pending = [];
    handlers.activate({ waitUntil: (p) => pending.push(p) });
    await Promise.all(pending);
    assert.equal(unregisters, 0);
    assert.equal(opens, 1);
    assert.equal(pending.length, 1);
  });
}
