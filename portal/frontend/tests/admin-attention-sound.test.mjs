import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import ts from "typescript";
import React from "react";
import TestRenderer from "react-test-renderer";

const { outputText } = ts.transpileModule(
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
const components = {};
new Function("require", "exports", outputText)((name) => {
  if (name === "react") return React;
  throw new Error(name);
}, components);

function setup() {
  const contexts = [];
  const timers = new Map();
  class AudioContext {
    state = "suspended";
    currentTime = 10;
    destination = {};
    tones = [];
    gains = [];
    closeCount = 0;
    constructor() {
      contexts.push(this);
    }
    async resume() {
      this.state = "running";
      this.onstatechange?.();
    }
    async close() {
      this.closeCount++;
      this.state = "closed";
      this.onstatechange?.();
    }
    createOscillator() {
      const tone = {
        frequency: { value: 0 },
        connect() {},
        disconnect() {},
        start(at) {
          this.startAt = at;
        },
        stop(at) {
          this.stopAt = at;
          this.stopped = true;
        },
      };
      this.tones.push(tone);
      return tone;
    }
    createGain() {
      const node = {
        connect() {},
        disconnect() {},
        gain: {
          setValueAtTime() {},
          linearRampToValueAtTime(value) {
            node.peak = Math.max(node.peak || 0, value);
          },
        },
      };
      this.gains.push(node);
      return node;
    }
  }
  globalThis.window = {
    AudioContext,
    setInterval(fn, ms) {
      timers.set(ms, fn);
      return ms;
    },
    clearInterval(id) {
      timers.delete(id);
    },
  };
  let renderer;
  const render = (level) => {
    TestRenderer.act(() => {
      const element = React.createElement(components.AdminAttentionSound, { level });
      if (renderer) renderer.update(element);
      else renderer = TestRenderer.create(element);
    });
    return renderer;
  };
  const click = async () =>
    TestRenderer.act(async () => {
      await renderer.root.findByType("button").props.onClick();
    });
  return {
    contexts,
    timers,
    render,
    click,
    cleanup() {
      if (renderer) TestRenderer.act(() => renderer.unmount());
      delete globalThis.window;
    },
  };
}

test("sound requires a gesture, repeats continuously every half second and stops when resolved", async () => {
  const f = setup();
  try {
    const view = f.render("warning");
    assert.equal(f.contexts.length, 0);
    assert.equal(f.timers.size, 0);
    await f.click();
    const ctx = f.contexts[0];
    assert.equal(view.root.findByType("button").props["aria-pressed"], true);
    assert.equal(ctx.tones.length, 1);
    assert.equal(ctx.tones[0].startAt, 10);
    assert.ok(ctx.tones[0].stopAt > 10 && ctx.tones[0].stopAt < 10.3);
    assert.ok(ctx.gains[0].peak > 0 && ctx.gains[0].peak <= 0.1);
    for (let i = 1; i <= 3; i++) {
      ctx.currentTime += 0.5;
      TestRenderer.act(() => f.timers.get(500)());
      assert.equal(ctx.tones.length, i + 1);
      assert.equal(ctx.tones[i].startAt - ctx.tones[i - 1].startAt, 0.5);
    }
    f.render("danger");
    assert.equal(ctx.tones.length, 6);
    assert.ok(ctx.tones[5].startAt > ctx.tones[4].startAt);
    f.render("normal");
    assert.equal(f.timers.size, 0);
    assert.equal(ctx.tones.length, 6);
    f.render("warning");
    assert.equal(ctx.tones.length, 7);
    await f.click();
    assert.equal(f.timers.size, 0);
    assert.equal(view.root.findByType("button").props["aria-pressed"], false);
  } finally {
    f.cleanup();
  }
  assert.equal(f.contexts[0].closeCount, 1);
});

test("enabling sound without a warning waits for a warning", async () => {
  const f = setup();
  try {
    f.render("normal");
    await f.click();
    assert.equal(f.contexts[0].tones.length, 0);
    assert.equal(f.timers.size, 0);
    f.render("danger");
    assert.equal(f.contexts[0].tones.length, 2);
  } finally {
    f.cleanup();
  }
  assert.equal(f.timers.size, 0);
});

test("unsupported or blocked audio reports failure without claiming sound is enabled", async () => {
  for (const mode of ["unsupported", "blocked"]) {
    const f = setup();
    try {
      if (mode === "unsupported") delete window.AudioContext;
      else
        window.AudioContext.prototype.resume = async () => {
          throw new Error("blocked");
        };
      const view = f.render("danger");
      await f.click();
      assert.equal(view.root.findByType("button").props["aria-pressed"], false);
      assert.equal(f.timers.size, 0);
      assert.match(view.root.findByProps({ role: "status" }).children.join(""), /通知音/);
    } finally {
      f.cleanup();
    }
  }
});

test("unmount during resume does not start an alarm", async () => {
  const f = setup();
  let resume;
  window.AudioContext.prototype.resume = () =>
    new Promise((resolve) => {
      resume = resolve;
    });
  f.render("warning");
  let pending;
  TestRenderer.act(() => {
    pending = f.render("warning").root.findByType("button").props.onClick();
  });
  f.cleanup();
  await TestRenderer.act(async () => {
    resume();
    await pending;
  });
  assert.equal(f.contexts[0].tones.length, 0);
  assert.equal(f.contexts[0].closeCount, 1);
  assert.equal(f.timers.size, 0);
});
