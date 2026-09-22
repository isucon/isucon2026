# US1 validation record

**対象タスク**: T043  
**状態**: PASS

**検証日**: 2026-08-04 (UTC)
**Revision**: 作業ツリー `469944ed5b6e551664b78d294af2a43a27375e71` + 本feature実装
**Toolchain**: Go 1.26.5 / Node 24.14.1 / npm 11.11.0 / Buf 1.72.0 / Playwright 1.62.1 / Chrome 151.0.7922.34

## 実行結果

- `go -C portal-mock/server test ./tests/contract ./tests/integration ./internal/standin -race -count=1`: PASS
- `npm --prefix portal-mock/web test`: PASS（設定を含む4 unit tests）
- `PLAYWRIGHT_BASE_URL=http://127.0.0.1:5174 npm --prefix portal-mock/web run test:e2e`: PASS（全7件）

## 確認済み

- [x] AP→EU の desired 更新、pending、stand-in成功後の収束
- [x] main server と calculated target hostname
- [x] JP→US、10か国一覧、対象外国拒否
- [x] fail-once/no-report、遅延、desired non-reversion
- [x] 同値更新 `changed=false` と stream 無配信
- [x] reset の既知初期状態

未解決 finding: なし。
