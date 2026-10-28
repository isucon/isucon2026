import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import React from "react";
import ts from "typescript";

const source = await readFile(new URL("../app/javascript/ScoreGraph.tsx", import.meta.url), "utf8");
const { outputText } = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.React, esModuleInterop: true },
});

// Preserve memoized values across prop updates without requiring a browser canvas.
function graphRenderer() {
  const memos = [];
  let index = 0;
  const hooks = {
    ...React,
    useState: (initial) => [initial, () => {}],
    useRef: () => ({ current: null }),
    useEffect: () => {},
    useMemo: (calculate, dependencies) => {
      const slot = index++;
      const previous = memos[slot];
      if (!previous || dependencies.some((value, i) => !Object.is(value, previous.dependencies[i]))) {
        memos[slot] = { dependencies, value: calculate() };
      }
      return memos[slot].value;
    },
  };
  const exports = {};
  new Function("require", "exports", outputText)((name) => {
    if (name === "react") return hooks;
    if (name === "uplot") return class {};
    if (name === "./ScoreGraphColors") return { COLORS: ["red", "blue"] };
    throw new Error(`Unexpected import: ${name}`);
  }, exports);
  return (teams, teamId) => {
    index = 0;
    exports.ScoreGraph({ teams, teamId, contest: { startsAt: { seconds: 0n }, endsAt: { seconds: 100n } } });
    return {
      options: memos.find(({ value }) => value?.series)?.value,
      data: memos.find(({ value }) => Array.isArray(value) && Array.isArray(value[0]))?.value,
    };
  };
}

for (const [role, teamId] of [
  ["admin", undefined],
  ["contestant", 1n],
]) {
  test(`${role} graph updates its legend when only the team name changes`, () => {
    const render = graphRenderer();
    const teams = [
      {
        team: { id: 1n, name: "Original team" },
        scoreHistory: { scores: [{ score: 100n, markedAt: { seconds: 10n } }] },
      },
    ];
    const before = render(teams, teamId);
    assert.equal(before.options.series[1].label, "Original team");
    const renamed = teams.map((item) => ({ ...item, team: { ...item.team, name: "Renamed team" } }));
    const after = render(renamed, teamId);
    assert.equal(after.options.series[1].label, "Renamed team");
    assert.deepEqual(after.data, before.data);
    assert.equal(after.options.series[1].stroke, before.options.series[1].stroke);
    assert.equal(render(renamed, teamId).options, after.options);
  });
}
