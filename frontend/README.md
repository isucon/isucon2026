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

## Go 版への対応

現在の接続先は `webapp/go` の `/api`。開発環境は [deploy/dev/README.md](../deploy/dev/README.md) を参照。
登録後は SecretKey を表示し、「はじめる」でログインする。ユーザー名は小文字のみ。
一覧は `hasMore` と末尾の日時・ID（ユーザー検索はユーザー名・ID）でページ送りする。

旧実装から削除・変更した機能:

- 地域変更の予約・取消（`pendingRegion`）：変更は即時反映する。
- 通知履歴の取得、既読・未読管理、通知を開く API：接続後の投稿開始通知だけをストリームで受信する。1件受信後に再接続し、切断中の通知は再取得しない。受信済み通知は画面の再読み込みで消える。
- Web Push の購読・解除と Service Worker：現在の初期実装では使わない。通知はログインして画面を開いている間に受信する。
- 退会の猶予期間・復元：退会は即時反映し、同じユーザー名で再登録できない。
- `Idempotency-Key`、`X-Correlation-ID`：送信しない。エラーは `{ "error": "..." }` を扱う。
- 投稿開始の自動スケジュール・仮想時計：管理者の `POST /api/admin/posting-window` で開始する。
- サーバーによる JPEG の再圧縮・デコード検証：画像はそのまま送受信する。カメラに加え、JPEG ファイルを選択して投稿・RealMoji を送信できる。

登録地域とサーバーの地域が異なる場合は、地域・プロフィールの変更とログアウトが可能。
別の地域のホストに移動した場合は、そのホストで再度ログインする。
Memories のコメント・RealMoji は取得レスポンスに含まれる各50件までを閲覧する。

`src/api/schema.d.ts` と OpenAPI 関連コマンドは旧 backend の契約を対象としており、現在の画面ではその型を使用しない。
