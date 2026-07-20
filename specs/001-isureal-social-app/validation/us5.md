# US5 検証結果

検証日: 2026-07-19 (UTC)

- `ISUREAL_TEST_DATABASE_URL=... go test ./... -count=1` (`backend/`): PASS（domain、contract、integration、concurrencyを含む）
- `npm run typecheck && npm run lint && npm test && npm run build && npm run test:contract && npm run test:e2e` (`frontend/`): PASS（Vitest 1 test、Playwright 6 tests、OpenAPI lintを含む）
- 地域固定オフセット、地域日一回、2分期限、前Window終了、管理者scheduleの設定・変更・取消し、開始後競合を確認。
- 3地域の対象者snapshot、既存Windowへの登録/地域変更時の非遡及通知、次Windowでの保留地域適用、通知の一意性を確認。
- 通知51件keyset、降順、同時挿入、本人限定、既読の一回遷移、create-post/current-post/Memory遷移を確認。
- 並行scheduler claim、取消し競合、ロック中地域を迂回する別地域commit、購読同時置換、worker crash後のstale claim回収を確認。
- Pushはアプリ内通知IDを4回の試行で維持し、成功、恒久失敗時購読無効化、一時/不明結果の再試行上限、古いsession抑止、アプリ内通知保持を確認。
- Service Workerは通知IDを安定tagとし、重複表示を置換する。通知クリックは既存windowを再利用・focusしてアプリ内open APIへ接続する。
- schedule/Push workerの構造化ログにendpoint、鍵、認証secret、ユーザー文面、座標を含めないことを実装レビューで確認。
