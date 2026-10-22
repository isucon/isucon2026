import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import test from "node:test";
import { renderToStaticMarkup } from "react-dom/server.js";
import ts from "typescript";

const componentURL = new URL("../app/javascript/DiscordJoinGuide.tsx", import.meta.url);
const { outputText } = ts.transpileModule(await readFile(componentURL, "utf8"), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.React, esModuleInterop: true },
});
const exports = {};
new Function("require", "exports", outputText)(createRequire(componentURL), exports);

for (const scenario of ["configured", "widget", "null", "network", "http-error"]) {
  test(`Discord join instructions: ${scenario}`, async () => {
    const configured = scenario === "configured" ? "https://discord.gg/configured" : "";
    globalThis.document = { querySelector: () => ({ content: configured }) };
    let requests = 0;
    globalThis.fetch = async () => {
      requests++;
      if (scenario === "network") throw new Error("offline");
      return {
        ok: scenario !== "http-error",
        json: async () => ({ instant_invite: scenario === "widget" ? "https://discord.gg/widget" : null }),
      };
    };
    const component = new exports.DiscordJoinGuide({ serverId: "guild" });
    component.setState = (state) => Object.assign(component.state, state);
    await component.componentDidMount();
    const html = renderToStaticMarkup(component.render());
    assert.match(html, /連携だけではDiscordサーバーに参加しません/);
    if (scenario === "configured" || scenario === "widget") {
      assert.match(html, new RegExp(`href="https://discord.gg/${scenario}"`));
      assert.match(html, /Discordサーバーに参加/);
    } else {
      assert.match(html, /招待リンクを取得できませんでした。運営にお問い合わせください/);
      assert.doesNotMatch(html, /href=/);
    }
    if (scenario === "configured") assert.equal(requests, 0);
    delete globalThis.document;
    delete globalThis.fetch;
  });
}
