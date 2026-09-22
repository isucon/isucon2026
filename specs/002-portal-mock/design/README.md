# Portal Mock Reference Design

このディレクトリは、`002-portal-mock` の実装時に使用する参照デザインを feature spec と同じ場所で
再現可能に管理するためのものです。元データはローカルの `~/isucon-portal-ui-mockups` から
2026-07-25 に取り込みました。以後、この feature では外部パスではなく、このディレクトリ内のファイルを
正として参照します。

## Bundle contents

- `ISUCON Portal Dashboard.dc.html` — 主となる HTML/CSS/JS プロトタイプ
- `support.js` — プロトタイプのサポートスクリプト
- `thumbnail.webp` — 参照用サムネイル

---

# Original Handoff Instructions

This is a **handoff bundle** from Claude Design (claude.ai/design).

A user mocked up designs in HTML/CSS/JS using an AI design tool, then exported this bundle so a coding agent can implement the designs for real.

## What you should do — IMPORTANT

**Read `ISUCON Portal Dashboard.dc.html` in full.** The user had this file open when they triggered the handoff, so it's almost certainly the primary design they want built. Read it top to bottom — don't skim. Then **follow its imports**: open every file it pulls in (shared components, CSS, scripts) so you understand how the pieces fit together before you start implementing.

**If anything is ambiguous, ask the user to confirm before you start implementing.** It's much cheaper to clarify scope up front than to build the wrong thing.

## About the design files

The design medium is **HTML/CSS/JS** — these are prototypes, not production code. Your job is to **recreate them pixel-perfectly** in whatever technology makes sense for the target codebase (React, Vue, native, whatever fits). Match the visual output; don't copy the prototype's internal structure unless it happens to fit.

**Don't render these files in a browser or take screenshots unless the user asks you to.** Everything you need — dimensions, colors, layout rules — is spelled out in the source. Read the HTML and CSS directly; a screenshot won't tell you anything they don't.
