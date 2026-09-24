import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

for (const [component, asset] of [
  ["Landing", "isucon2026-logo.svg"],
  ["Navbar", "isucon2026-logo-white.svg"],
]) {
  test(`${component} uses an accessible ISUCON2026 logo with a deployable SVG`, async () => {
    const source = await readFile(new URL(`../app/javascript/${component}.tsx`, import.meta.url), "utf8");
    assert.ok(source.includes(`src="/${asset}"`));
    assert.match(source, /<img[^>]+alt="ISUCON2026"/);
    assert.doesNotMatch(source, /isucon14_logo/);
    const svg = await readFile(new URL(`../public/${asset}`, import.meta.url), "utf8");
    assert.match(svg, /<svg[^>]+viewBox="0 0 1000 240"/);
    assert.match(svg, /<path/);
    assert.doesNotMatch(svg, /<(?:script|image)\b/);
  });
}
