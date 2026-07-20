# 横断アーキテクチャレビュー

## 初回レビュー（T124、2026-07-19 UTC）

対象: `backend/internal/service/`、`backend/internal/repository/postgres/`、`backend/internal/image/`、`frontend/src/`

| 観点 | 初回確認 | 状態 |
|---|---|---|
| 認証・認可 | user/admin は別principalと別middleware。投稿・交流・画像はvisibility/owner検査、通知・Memoriesはuser IDで絞る。 | 継続確認 |
| transaction/lock順序 | 複数userはUUID昇順の`LockUsers`、schedule/push/idempotencyは対象行を先にlock。地域scheduleは`SKIP LOCKED`で分離。 | 継続確認 |
| repository境界 | handler→service→repositoryを基本とし、idempotency transactionはcontext経由でrepositoryへ伝播する。 | 良好 |
| 冪等性 | 状態変更routeはUUID key、caller/operation/request hashで分離し24時間保持。503は保存しない。 | 期限切れ/障害注入をT126で追加 |
| 画像sanitizer隔離 | 専用subprocess、一時dir、同時実行slot、timeout、memory/CPU環境境界、出力JPEG再decodeを使用。 | resource/crash/timeoutをT126で追加 |
| worker再試行 | scheduleは行claim、Pushは`processing` stale回収と4回上限、purgeは再実行可能。 | restart/unknown-resultをT126で追加 |
| frontend共有状態 | tokenはIndexedDBだけに保存しsession eventで同期。APIはBearer、credentials omit。通知は論理IDをtagにする。 | T130で消去・露出境界を強化 |

初回に残したfinding:

1. FR-044のOrigin/Fetch Metadata検査、no-store/referrer/nosniff、SPA CSP/Permissions-Policyが未実装。T130で実装・検証する。
2. 認証情報のCookie/query/form提示を明示拒否する共通middlewareがない。T130で追加する。
3. sanitizerのslot枯渇・limit・crash・timeout、画像公開後DB rollback補償、worker restart、Push不明結果、stale cursor、期限切れ冪等性の横断試験が不足。T126で追加する。
4. 構造化ログの全経路横断secret監査が不足。T127で追加する。
5. production Service Worker成果物は`VITE_VAPID_PUBLIC_KEY`設定時のbuild/registrationをCIで検証する必要がある。T129/T133で固定する。

初回レビューはfindingの列挙であり、最終closureではない。T125〜T130完了後に同じ観点を再検査し、T131で全件を解消する。

## 最終レビュー（T131、2026-07-19 UTC）

初回と同じ対象・観点を、security-hardening後のコードに対して再確認した。

| finding | 解決 |
|---|---|
| 1. FR-044 middleware/header/CSP不足 | `SecurityContract`、nginx production CSP/Permissions-Policy、backend/Playwright security testsを追加。API・認可画像の成功/エラーを検証済み。 |
| 2. 代替資格情報源 | Cookie、資格情報query、form content typeを共通拒否し、Bearer以外をauth middlewareが拒否。exact Origin、`same-origin`、headerless clientも検証済み。 |
| 3. failure recovery不足 | sanitizer slot/limit/crash/timeout/secret環境隔離、画像補償削除、worker restart、unknown Push/stale claim、stale anchor、期限切れ冪等性を自動化。 |
| 4. log secret監査不足 | user/admin認証、認定、schedule、success/input/conflict/unexpectedを実リクエストで通し、全禁止値の非出力を確認。 |
| 5. Service Worker production成果物 | Viteの明示entryで`dist/service-worker.js`を生成し、通知IDtag・local session gate・focus-safe openをunit/E2E/buildで確認。 |

最終レビュー中に、冪等性の外側transaction内でrepositoryの一意制約競合が起きるとtransaction全体がabortedとなり、本来の409が500へ変化するfindingを検出した。nested `DB.InTx`をsavepointで隔離し、登録競合の実リクエストと冪等性回帰試験で409および保存済み応答を確認して解決した。

最終状態:

- authorization: user/admin principal分離、owner/visibility/user-id境界、Bearer-onlyを再確認。未解決なし。
- lock ordering: 複数user UUID昇順、schedule/push job claimは対象行順序と`SKIP LOCKED`、nested transactionはsavepoint。未解決なし。
- repository/idempotency: context transaction伝播、24時間generation、503 rollback、競合savepointを確認。未解決なし。
- image isolation: body streaming境界をsecurity middlewareが読まず、subprocessは親のDB/VAPID環境を継承しない。未解決なし。
- workers: schedule一意commit、Push stale recovery/4回上限、purge再実行安全性を確認。未解決なし。
- frontend state: IndexedDBのみ、transaction完了後に状態通知、401/logout/account deletionで削除、URL/DOM/Cookie/log非露出。未解決なし。

検証結果:

- `ISUREAL_TEST_DATABASE_URL=... go test ./... -count=1 && go vet ./...` (`backend/`): PASS
- `npm run typecheck && npm run lint && npm test && npm run check:openapi && npm run test:contract && VITE_VAPID_PUBLIC_KEY=... npm run build && npm run test:e2e` (`frontend/`): PASS（Playwright 9 tests、production Service Worker成果物を含む）
- source audit: `TODO/FIXME`、console出力、Web Storage/Cookie、CORS/rate-limit、禁止ログ属性を`rg`で再検査。未解決finding 0。

Quickstart実HTTP確認で、session responseの必須`token`に誤って`writeOnly`が付いていたため、OpenAPI response validatorが成功応答を500へ変換する最終findingを検出した。契約から誤指定を除去してclientを再生成し、seed済み`alice`のproxy経由ログイン、全backend/frontend suite、drift/lintを再実行して解決した。最終findingは引き続き0件。
