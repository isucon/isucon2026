# Cross-cutting architecture review

**対象タスク**: T070  
**レビュー日**: 2026-08-04  
**対象**: `spec.md` / `plan.md` / `data-model.md` / `contracts/README.md` と実装・配布境界  
**状態**: 設計レビュー完了、コード最終監査は `final-validation.md` で再確認

| 観点 | 必須不変条件 | 実装/テスト境界 | レビュー結論 |
|---|---|---|---|
| desired/applied SoT | desired=Portal、applied=Router。Portalはdesiredをrevertせず、未報告値をappliedへ混入しない | store の update/report を分離。contract/integrationとUI/API比較で検証 | **適合**。公開資料・compose/nginxは値を合成せずRPCを透過中継する |
| Router報告の受理 | 相関差は拒否しない。構造/必須/値域不正だけINVALID_ARGUMENT | report transitionを単一store境界に置き、unknown/backward/duplicate/invalid/recoveryを試験 | **適合方針**。nginxはroute/body/errorを改変しない |
| lock ordering | update/start/report/resetが同じ同期storeを通り、reset最優先 | concurrency testsで update-vs-start、report-vs-reset、competing starts | **要最終テスト確認**。配布構成はserver replicaを1に固定し、複数process storeを作らない |
| generation cancellation | reset前のstand-in/benchmark timerは後から状態更新しない | generation-scoped context、fake clock、reset tests | **要最終テスト確認**。コンテナ再起動もprocess memoryを破棄する |
| stream backpressure | 接続時に最新、slow consumer時はbounded latest-value coalescing、reset後も接続維持 | desired/stateを独立hub、buffer上限、cancel解放 | **適合方針**。nginx `proxy_buffering off`、長いread timeoutでConnect streamingを阻害しない |
| generated-code boundary | `/proto`がSoT、`gen/go`/`gen/ts`はBuf生成のみ、BSR remoteなし | pinned local plugins、生成差分、breaking、descriptor build | **適合**。Docker buildは生成済み成果物を読取るだけ |
| contract responsibility | `portal.v1`はRouter共有、`portal_mock.v1`はruntime/UI/reset/模擬bench | route/version tests、message field audit | **適合**。nginxは2 service routeだけを明示proxyする |
| stand-in/external exclusivity | 起動時に一方だけ、externalではstand-in停止、実行中変更なし | config validationとexternal integration | **適合**。Composeは単一`PORTAL_ROUTER_MODE`をserverへ渡す |
| deterministic reset | 設定/bench/history/score/seedを既知値へ。snapshot/timeは新規 | reset/restart/replay tests | **適合方針**。volume/databaseを一切持たない |
| ISUReal isolation | 本体backend/frontend/compose/auth/routesに混入しない | 独立project名、専用ports、専用Dockerfiles、path CI | **適合**。root composeを変更せず、`portal-mock/compose.yaml`のみ |
| public artifact safety | secret/個人情報/実bench/採点/ボトルネック/想定解を含めない | gitleaks + `public-artifact-audit.md` | **適合**。`.env.example`はダミー値のみ |

## 解消済み設計リスク

1. Web と Server を別コンテナにしつつ、nginx が URI を変更せず Connect unary/server-streaming を中継する。
2. Server/Web の Docker build context は共有 `gen/**` を必要とするためリポジトリルートだが、
   Dockerfile は各モジュールと生成物だけを明示 `COPY` する。
3. Compose は named volume、DB、本体networkを持たず、単一Server processにメモリ状態を閉じ込める。
4. Container は非root/read-onlyで動かし、一時領域だけtmpfsを使用する。

## 最終ゲート

- Go raceを含むconcurrency suiteが成功すること。
- Playwright経由のstreamingにproxy buffering由来の停止がないこと。
- compose smokeがUI、Server health、Web proxy経由GetStateを成功させること。
- `traceability.md` と `public-artifact-audit.md` に未解決項目がないこと。
