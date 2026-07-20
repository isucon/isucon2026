# US1 検証結果

検証日: 2026-07-19 (UTC)

## 実行結果

- `ISUREAL_TEST_DATABASE_URL='postgres://isureal:local-development-only@localhost:5432/isureal?sslmode=disable' go test ./... -count=1` (`backend/`): PASS（domain、OpenAPI contract、PostgreSQL integration、競合、冪等性、51件 keyset pagination を含む）
- `go vet ./...` (`backend/`): PASS
- `npm run typecheck`、`npm run lint`、`npm run build` (`frontend/`): PASS
- `npm run test:e2e` (`frontend/`): PASS（1 test）

## 固定ブラウザ

- Playwright 1.61.1、Chrome for Testing 149.0.7827.55
- `mcr.microsoft.com/playwright:v1.61.1-noble@sha256:5b8f294aff9041b7191c34a4bab3ac270157a28774d4b0660e9743297b697e48`

## 確認事項

- 投稿前フィードゲート、地域横断Window、3公開範囲、block/active状態、自己除外を検証。
- friends-of-friends audience は距離1・2を含み、同名feedは距離2のみで直接友達を除外。
- 同時投稿は1件だけ成立し、削除2回後の再投稿を拒否。作成・削除リプレイの副作用は1回。
- 更新時の画像・位置・投稿時刻不変、終了済み投稿の更新拒否と本人削除を検証。
- 51件、同一時刻ID順、ページ間同時挿入、feed束縛署名cursorを検証。
- JPEG形式、10 MiB超過、再エンコード、画像寸法非依存と、投稿から4フィードへのUI導線を検証。
- 構造化ログはID・操作・結果のみで、画像、caption、正確な位置を含まない。
