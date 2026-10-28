import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import test from "node:test";
import { renderToStaticMarkup } from "react-dom/server.js";
import ts from "typescript";

async function loadComponent(file, mocks) {
  const url = new URL(`../app/javascript/${file}.tsx`, import.meta.url);
  const { outputText } = ts.transpileModule(await readFile(url, "utf8"), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.React, esModuleInterop: true },
  });
  const exports = {};
  const require = createRequire(url);
  new Function("require", "exports", outputText)((name) => mocks[name] ?? require(name), exports);
  return exports;
}

const errorMessage = await loadComponent("ErrorMessage", { "./ApiClient": { ApiError: class extends Error {} } });
const { RegistrationForm } = await loadComponent("RegistrationForm", {
  "./ErrorMessage": errorMessage,
  "./RegistrationConsentFields": { RegistrationConsentFields: () => null },
  "@bufbuild/protobuf": { create: (_schema, fields) => fields },
  "../../proto/isuxportal/services/registration/records_pb": {},
  "../../proto/isuxportal/services/registration/create_team_pb": {},
  "../../proto/isuxportal/services/registration/join_pb": {},
  "../../proto/isuxportal/services/registration/session_pb": {
    GetRegistrationSessionResponse_Status: { JOINED: 5 },
  },
});

function setup(mode) {
  const saved = [];
  let refreshed = 0;
  const form = new RegistrationForm({
    session: { contestant: { detail: { avatarUrl: "https://example.com/old.png" } } },
    registrationSession: {
      status: mode === "edit" ? 5 : 3,
      team: mode === "create" ? undefined : { id: 1n },
    },
    inviteToken: "invite",
    client: {
      getAvatarUrl: async () => ({ url: "https://example.com/new.png", uploadPresigned: "https://example.com/upload" }),
      ...Object.fromEntries(
        ["createTeam", "joinTeam", "updateRegistration"].map((method) => [
          method,
          async (payload) => saved.push({ method, payload }),
        ]),
      ),
    },
    updateRegistrationSession: () => refreshed++,
  });
  form.setState = (state) => Object.assign(form.state, state);
  const file = new File(["image"], "avatar.png", { type: "image/png" });
  form.state.avatarfile = file;
  const blob = new Blob(["cropped"], { type: "image/png" });
  form.cropImage = async () => blob;
  return { form, saved, file, blob, refreshed: () => refreshed };
}

for (const mode of ["create", "join", "edit"]) {
  for (const failure of [403, 500, "network"]) {
    test(`${mode}: upload ${failure} stops saving and allows retry`, async (t) => {
      const { form, saved, file, blob, refreshed } = setup(mode);
      const fetchMock = t.mock.method(globalThis, "fetch", async (url, options) => {
        assert.equal(url, "https://example.com/upload");
        assert.equal(options.method, "PUT");
        assert.equal(options.body, blob);
        assert.equal(options.headers["Content-Type"], "image/png");
        if (failure === "network") throw new TypeError("Failed to fetch");
        return new Response(null, { status: failure });
      });
      await form.onSubmit({ preventDefault() {} });
      assert.deepEqual(saved, []);
      assert.equal(refreshed(), 0);
      assert.equal(form.state.avatarurl, "https://example.com/old.png");
      assert.equal(form.state.avatarfile, file);
      assert.equal(form.state.requesting, false);
      assert.ok(form.state.requestError instanceof Error);
      const html = renderToStaticMarkup(form.renderError());
      assert.match(html, /message is-danger/);
      if (failure !== "network") assert.match(html, /アイコンのアップロードに失敗しました.*再度/);

      fetchMock.mock.mockImplementation(async () => new Response(null, { status: 204 }));
      await form.onSubmit({ preventDefault() {} });
      assert.equal(saved.length, 1);
      assert.equal(saved[0].method, { create: "createTeam", join: "joinTeam", edit: "updateRegistration" }[mode]);
      assert.equal(saved[0].payload.avatarUrl, "https://example.com/new.png");
      assert.equal(refreshed(), 1);
      assert.equal(form.state.requestError, null);
      assert.equal(form.state.requesting, false);
    });
  }

  test(`${mode}: unchanged avatar saves without uploading`, async (t) => {
    const { form, saved } = setup(mode);
    form.state.avatarfile = null;
    const fetchMock = t.mock.method(globalThis, "fetch", async () => assert.fail("unexpected upload"));
    await form.onSubmit({ preventDefault() {} });
    assert.equal(fetchMock.mock.callCount(), 0);
    assert.equal(saved.length, 1);
    assert.equal(saved[0].payload.avatarUrl, "https://example.com/old.png");
  });
}
