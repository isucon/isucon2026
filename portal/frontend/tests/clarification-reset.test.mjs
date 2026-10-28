import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import test from "node:test";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server.js";
import { useForm } from "react-hook-form";
import ts from "typescript";

async function renderForm(role, client) {
  const name = role === "admin" ? "Admin" : "Contestant";
  const url = new URL(`../app/javascript/${role}/${name}ClarificationList.tsx`, import.meta.url);
  const { outputText } = ts.transpileModule(`${await readFile(url, "utf8")}\nexport { ClarForm };`, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.React, esModuleInterop: true },
  });
  let methods, tree;
  const exports = {};
  const require = createRequire(url);
  new Function("require", "exports", outputText)((name) => {
    if (name === "react-hook-form") return { useForm: (options) => (methods = useForm(options)) };
    if (name === "../Clarification") return { Clarification: () => null };
    if (name === "../ErrorMessage") return { ErrorMessage: () => null };
    if (name === "@bufbuild/protobuf") return { create: (_schema, fields) => fields };
    if (name.endsWith("/clarifications_pb")) return {};
    return require(name);
  }, exports);
  const submitted = [];
  renderToStaticMarkup(
    React.createElement(() => {
      tree = exports.ClarForm({ session: {}, client, onSubmit: (clar) => submitted.push(clar) });
      return tree;
    }),
  );
  function findForm(element) {
    if (element?.type === "form") return element;
    for (const child of React.Children.toArray(element?.props?.children)) {
      const found = findForm(child);
      if (found) return found;
    }
  }
  return {
    methods,
    submitted,
    submit: () => findForm(tree).props.onSubmit({ preventDefault() {}, persist() {}, target: { reset() {} } }),
  };
}

for (const [role, values] of [
  ["contestant", { question: "unique question" }],
  ["admin", { question: "announcement", answer: "answer", teamId: "", disclose: true }],
  ["admin", { question: "private question", answer: "private answer", teamId: "42", disclose: false }],
]) {
  test(`${role} resets submitted values after success: ${JSON.stringify(values)}`, async () => {
    const requests = [];
    const send = async (payload) => {
      requests.push(payload);
      return { clarification: { id: 1n } };
    };
    const view = await renderForm(role, { requestClarification: send, createClarification: send });
    for (const [key, value] of Object.entries(values)) view.methods.setValue(key, value);
    await view.submit();
    assert.equal(requests.length, 1);
    assert.equal(requests[0].question, values.question);
    if (role === "admin") {
      assert.equal(requests[0].answer, values.answer);
      assert.equal(requests[0].teamId, values.disclose ? 0n : 42n);
    }
    assert.equal(view.submitted.length, 1);
    await view.submit();
    if (role === "contestant") assert.deepEqual(requests[1], { question: "" });
    else {
      assert.equal(requests.length, 1);
      view.methods.setValue("disclose", true);
      await view.submit();
      assert.deepEqual(requests[1], { question: "", answer: "", teamId: 0n });
    }
  });

  test(`${role} preserves values when submission fails: ${JSON.stringify(values)}`, async () => {
    const requests = [];
    const send = async (payload) => {
      requests.push(payload);
      throw new Error("Request failed");
    };
    const view = await renderForm(role, { requestClarification: send, createClarification: send });
    for (const [key, value] of Object.entries(values)) view.methods.setValue(key, value);
    await view.submit();
    await view.submit();
    assert.equal(requests.length, 2);
    assert.equal(requests[0].question, values.question);
    assert.deepEqual(requests[1], requests[0]);
    assert.equal(view.submitted.length, 0);
  });
}
