# ISUReal frontend

Node.js 24.14.1 / npm 11.11.0 を使用する。`npm ci` 後に `npm run dev` で起動する。

## Browser bootstrap pin

実装・テスト開始用（リリース対象ではない）の bootstrap は次のとおり。

- Chrome for Testing: `149.0.7827.55`
- Playwright: `1.61.1`
- image: `mcr.microsoft.com/playwright:v1.61.1-noble@sha256:5b8f294aff9041b7191c34a4bab3ac270157a28774d4b0660e9743297b697e48`

`PLAYWRIGHT_CHROME_VERSION` は4要素の完全版、`PLAYWRIGHT_IMAGE` は `@sha256:` 付きの不変参照だけを受理する。
`npm run check:browser` と Playwright global setup がパッケージ内および実行時の版を検査する。

T133 のリリース判定開始時に、その日の最新安定版デスクトップ Chrome を一度だけ外部の公式情報から再解決する。
完全版、確認日、対応する Playwright イメージの multi-platform digest を CI、Quickstart 結果、版マニフェストへ同時に記録し、
判定中は同じ digest を使う。更新は別の明示的な版更新として全 Playwright シナリオを再実行した場合だけ行う。
