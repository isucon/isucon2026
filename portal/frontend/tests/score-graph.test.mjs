import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import test from "node:test";
import React from "react";
import TestRenderer from "react-test-renderer";
import ts from "typescript";

const componentURL = new URL("../app/javascript/ScoreGraph.tsx", import.meta.url);
const { outputText } = ts.transpileModule(await readFile(componentURL, "utf8"), {
  compilerOptions: {
    target: ts.ScriptTarget.ES2020,
    module: ts.ModuleKind.CommonJS,
    jsx: ts.JsxEmit.React,
    esModuleInterop: true,
  },
});

function setup() {
  const instances = [];
  class Plot {
    destroyed = 0;
    updates = [];
    constructor(options, data) {
      this.options = options;
      this.data = data;
      instances.push(this);
    }
    destroy() {
      this.destroyed++;
    }
    setData(data) {
      assert.equal(this.destroyed, 0, "must not update a destroyed plot");
      this.data = data;
      this.updates.push(data);
    }
  }
  const exports = {};
  const require = createRequire(componentURL);
  new Function("require", "exports", outputText)((name) => {
    if (name === "uplot") return Plot;
    if (name === "./ScoreGraphColors") return { COLORS: ["red", "blue"] };
    return require(name);
  }, exports);
  const host = {};
  let view;
  const render = (props) => React.createElement(exports.ScoreGraph, props);
  return {
    instances,
    mount(props) {
      TestRenderer.act(() => {
        view = TestRenderer.create(render(props), { createNodeMock: () => host });
      });
    },
    update(props) {
      TestRenderer.act(() => view.update(render(props)));
    },
    unmount() {
      TestRenderer.act(() => view.unmount());
    },
  };
}

const team = (id, scores = [100]) => ({
  team: { id: BigInt(id), name: `Team ${id}` },
  bestScore: { score: scores.at(-1) },
  scoreHistory: { scores: scores.map((score, index) => ({ score, markedAt: { seconds: BigInt(index + 1) } })) },
});
const props = {
  teams: [team(1), team(2), team(3), team(4)],
  contest: { startsAt: { seconds: 0n }, endsAt: { seconds: 100n } },
};

test("each dashboard visit destroys all its plots on unmount", () => {
  const graph = setup();
  for (let visit = 0; visit < 120; visit++) {
    graph.mount(props);
    assert.equal(graph.instances.filter((plot) => plot.destroyed === 0).length, 1);
    graph.unmount();
    assert.ok(
      graph.instances.every((plot) => plot.destroyed === 1),
      `leaked plot after visit ${visit + 1}`,
    );
  }
});

test("score refresh reuses the plot; option changes destroy the previous plot exactly once", () => {
  const graph = setup();
  graph.mount(props);
  const plot = graph.instances.at(-1);
  const count = graph.instances.length;
  const refreshed = { ...props, teams: [team(1, [100, 200]), ...props.teams.slice(1)] };
  graph.update(refreshed);
  assert.equal(graph.instances.length, count);
  assert.deepEqual(plot.data[0], [1, 2]);
  assert.deepEqual(plot.data[1], [100, 200]);
  assert.equal(plot.destroyed, 0);
  graph.update({ ...refreshed, width: 800 });
  assert.equal(plot.destroyed, 1);
  assert.equal(graph.instances.at(-1).options.width, 800);
  assert.deepEqual(graph.instances.at(-1).data, plot.data);
  graph.update({ ...refreshed, width: 800, teams: refreshed.teams.slice(0, 2) });
  assert.equal(graph.instances.at(-1).options.series.length, 3);
  graph.unmount();
  assert.ok(graph.instances.every((instance) => instance.destroyed === 1));
});
