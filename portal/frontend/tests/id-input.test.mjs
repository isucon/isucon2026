import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import test from "node:test";
import React from "react";
import Renderer from "react-test-renderer";
import ts from "typescript";

function load(path, router = {}) {
  const url = new URL(`../app/javascript/${path}`, import.meta.url);
  const require = createRequire(url);
  const exports = {};
  const { outputText } = ts.transpileModule(readFileSync(url, "utf8"), {
    compilerOptions: {
      target: ts.ScriptTarget.ES2020,
      module: ts.ModuleKind.CommonJS,
      jsx: ts.JsxEmit.React,
      esModuleInterop: true,
    },
  });
  new Function("require", "exports", outputText)((name) => {
    if (name.endsWith("/IdInput")) return load("IdInput.ts");
    if (name === "react-router-dom") return router;
    if (name === "@bufbuild/protobuf") return { create: (_, value) => value };
    if (name.includes("/proto/"))
      return {
        BenchmarkJob_Status: { PENDING: 0, RUNNING: 1, ERRORED: 2, CANCELLED: 3, FINISHED: 4 },
        GetRegistrationSessionResponse_Status: {
          NOT_LOGGED_IN: 1,
          CLOSED: 2,
          NOT_JOINABLE: 3,
          CREATABLE: 4,
          JOINABLE: 5,
          JOINED: 6,
        },
      };
    if (name.endsWith("/ErrorMessage"))
      return { ErrorMessage: ({ error }) => React.createElement("p", { role: "alert" }, error.message) };
    if (name.startsWith(".")) return new Proxy({}, { get: () => () => null });
    return require(name);
  }, exports);
  return exports;
}
const invalid = ["a", " ", "1.2", "1,", "0", "-1", "0x10", "9223372036854775808"];

test("last validations keeps invalid input visible and blocks every trigger, then recovers", async () => {
  const { AdminLastValidations } = load("admin/AdminLastValidations.tsx");
  const calls = [];
  const client = { listInstanceCommandExecuteRequests: async () => ({ requests: [] }) };
  for (const method of ["triggerEnvCheck", "triggerInstanceRestart", "triggerBenchmarks"])
    client[method] = async (request) => calls.push(request);
  let view;
  await Renderer.act(async () => {
    view = Renderer.create(React.createElement(AdminLastValidations, { client }));
  });
  for (const value of ["1,a", "1,", ",1", "1,,2", "1.2", "0", "-1", "0x10", "9223372036854775808"]) {
    await Renderer.act(async () => view.root.findByType("input").props.onChange({ target: { value } }));
    assert.equal(view.root.findByType("input").props.value, value);
    assert.ok(view.root.findAllByProps({ role: "alert" }).length);
    for (const button of view.root.findAllByType("button")) {
      assert.equal(button.props.disabled, true);
      await Renderer.act(async () => button.props.onClick());
    }
    assert.equal(calls.length, 0);
  }
  for (const [value, expected] of [
    ["1, 2", [1n, 2n]],
    ["", []],
    ["   ", []],
  ]) {
    globalThis.confirm = () => true;
    await Renderer.act(async () => view.root.findByType("input").props.onChange({ target: { value } }));
    const button = view.root.findAllByType("button")[0];
    assert.equal(button.props.disabled, false);
    await Renderer.act(async () => button.props.onClick());
    assert.deepEqual(calls.at(-1).teamIds, expected);
  }
  view.unmount();
});

for (const value of [...invalid, ""])
  test(`registration rejects malformed team_id ${JSON.stringify(value)}`, async () => {
    globalThis.document = { location: { search: `?team_id=${encodeURIComponent(value)}&invite_token=bad` } };
    const { Registration } = load("Registration.tsx");
    let view;
    await Renderer.act(async () => {
      view = Renderer.create(
        React.createElement(Registration, {
          session: {},
          client: { getRegistrationSession: () => assert.fail("invalid invitation must not call API") },
        }),
      );
    });
    assert.match(JSON.stringify(view.toJSON()), /無効な招待 URL/);
    assert.match(JSON.stringify(view.toJSON()), /参加登録/);
    view.unmount();
  });

for (const [path, component] of [
  ["admin/AdminTeamDetail.tsx", "AdminTeamDetail"],
  ["admin/AdminBenchmarkJobDetail.tsx", "AdminBenchmarkJobDetail"],
  ["contestant/ContestantBenchmarkJobDetail.tsx", "ContestantBenchmarkJobDetail"],
  ["admin/AdminClarificationDetail.tsx", "AdminClarificationDetail"],
  ["admin/AdminLastValidationsCommandResult.tsx", "AdminLastValidationsCommandResult"],
])
  test(`${component} rejects malformed route IDs`, async () => {
    for (const id of [...invalid, undefined]) {
      const Component = load(path, { useParams: () => ({ id }) })[component];
      let view;
      await Renderer.act(async () => {
        view = Renderer.create(
          React.createElement(Component, {
            session: {},
            client: new Proxy({}, { get: () => () => assert.fail("invalid ID must not call API") }),
          }),
        );
      });
      assert.ok(view.root.findAllByProps({ role: "alert" }).length);
      view.unmount();
    }
  });

for (const component of ["AdminBenchmarkJobList", "AdminContestantInstanceList", "AdminClarificationList"])
  test(`${component} validates query IDs and recovers with its filter intact`, async () => {
    let teamId = "a";
    const Component = load(`admin/${component}.tsx`, {
      useSearchParams: () => [new URLSearchParams({ team_id: teamId })],
      useNavigate: () => () => {},
    })[component];
    const calls = [];
    const client = new Proxy(
      {},
      {
        get: () => async (id) => {
          calls.push(id);
          return { maxPage: 0n, jobs: [], contestantInstances: [], clarifications: [] };
        },
      },
    );
    let view;
    const render = () => React.createElement(Component, { session: {}, client });
    await Renderer.act(async () => {
      view = Renderer.create(render());
    });
    for (const value of invalid) {
      teamId = value;
      await Renderer.act(async () => view.update(render()));
      assert.ok(view.root.findAllByProps({ role: "alert" }).length);
      assert.ok(view.root.findAllByType("input").length);
      assert.equal(calls.length, 0);
    }
    for (const value of ["42", ""]) {
      teamId = value;
      await Renderer.act(async () => view.update(render()));
      assert.equal(view.root.findAllByProps({ role: "alert" }).length, 0);
      assert.equal(calls.at(-1) ?? null, value ? 42n : null);
    }
    view.unmount();
  });

test("resource ID parsing preserves int64 precision and rejects non-decimal input", () => {
  const { parseResourceId } = load("IdInput.ts");
  for (const value of [...invalid, "", null, undefined]) assert.equal(parseResourceId(value), null);
  for (const value of ["1", "0001", "9007199254740993", "9223372036854775807"])
    assert.equal(parseResourceId(value), BigInt(value));
});

for (const search of ["", "?team_id=9007199254740993&invite_token=token"])
  test(`registration preserves valid query ${search}`, async () => {
    globalThis.document = { location: { search } };
    const { Registration } = load("Registration.tsx");
    let query;
    let view;
    await Renderer.act(async () => {
      view = Renderer.create(
        React.createElement(Registration, {
          session: {},
          client: {
            getRegistrationSession: async (value) => {
              query = value;
              return {};
            },
          },
        }),
      );
    });
    assert.equal(query.teamId, search ? 9007199254740993n : 0n);
    assert.equal(query.inviteToken, search ? "token" : undefined);
    view.unmount();
  });
