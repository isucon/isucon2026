# ISUCON2026 Portal Mock

`docs/geometrics-distribution.md` の開発確認用に、1チーム分の desired/applied 設定と
決定的なベンチマーク UI を提供する独立モックです。ISUReal 本体の `compose.yaml`、
認証、画面、API ルーティングには組み込みません。

> **境界**: ベンチマークは UI と状態遷移のシミュレーションです。実ベンチマーカー、
> 実負荷、採点、Router 内部の遅延・帯域制御とは通信しません。

## 必要なツール

リポジトリ直下の `mise.toml` が Go、Node.js、Buf、protoc と生成プラグインの版を固定します。

```bash
mise install
```

Docker Compose だけで起動する場合は Docker Engine と Compose v2 が必要です。

## 環境変数

`portal-mock/.env.example` を安全なローカル既定値として用意しています。

| 変数 | 既定値 | 説明 |
|---|---:|---|
| `PORTAL_TEAM_NUMBER` | `1` | 起動対象の正のチーム番号 |
| `PORTAL_TEAM_HASH` | `abc123` | 表示用ホスト名の random-hash。認証には使わない |
| `PORTAL_DOMAIN_SUFFIX` | `isucon2026.westens.tokyo` | app/server ホスト名の suffix |
| `PORTAL_ROUTER_MODE` | `stand-in` | `stand-in` または `external-router`。起動時固定 |
| `PORTAL_STANDIN_PROFILE` | `success` | stand-in の決定的挙動。下表参照 |
| `PORTAL_APPLY_TIMEOUT_MS` | `3000` | 未収束を `DELAYED` と診断するまでの時間 |
| `PORTAL_STANDIN_DELAY_MS` | `100` | stand-in が報告するまでの遅延 |
| `PORTAL_STANDIN_RETRY_MS` | `150` | fail-once profile の同一 snapshot 再試行間隔 |
| `PORTAL_BENCHMARK_PENDING_MS` | `200` | シミュレーションの pending 時間 |
| `PORTAL_BENCHMARK_STEP_MS` | `120` | running 中の決定的な進捗 step 間隔 |
| `PORTAL_LISTEN_ADDRESS` | `0.0.0.0:8080` | Server の listen address |
| `PORTAL_WEB_DIR` | 空 | Server 自身が配信する SPA directory。Compose では未使用 |
| `PORTAL_SERVER_BIND_IP` | `127.0.0.1` | Compose が Server を公開する host address |
| `PORTAL_SERVER_PORT` | `8080` | Compose が Server を公開するホスト側ポート |
| `PORTAL_WEB_BIND_IP` | `127.0.0.1` | Compose が Web を公開する host address |
| `PORTAL_WEB_PORT` | `5174` | Compose が Web を公開するホスト側ポート |

`PORTAL_TEAM_HASH` はローカル例示値から変更できますが、秘密鍵や認証トークンを設定しないでください。
モード、profile、TeamKey、ホスト名設定は実行中に切り替えず、変更後はプロセスを再起動します。

## ローカル起動

### mise / Go / Vite

Terminal A:

```bash
PORTAL_TEAM_NUMBER=1 \
PORTAL_TEAM_HASH=abc123 \
mise exec -- go -C portal-mock/server run ./cmd/portal
```

Terminal B:

```bash
npm --prefix portal-mock/web ci
npm --prefix portal-mock/web run dev -- --host 0.0.0.0 --port 5174
```

ブラウザで `http://localhost:5174` を開きます。Vite の設定は
`portal.v1.PortalRouterService` と `portal_mock.v1.PortalMockService` を
`http://localhost:8080` へ proxy します。

### Docker Compose

```bash
cp portal-mock/.env.example portal-mock/.env
docker compose --env-file portal-mock/.env -f portal-mock/compose.yaml up --build --wait
```

- UI: `http://localhost:5174`
- Server: `http://localhost:8080`
- readiness: `http://localhost:8080/healthz`

停止:

```bash
docker compose --env-file portal-mock/.env -f portal-mock/compose.yaml down --remove-orphans
```

Compose project 名は `portal-mock` で、本体のサービス、network、volume、port routing を再利用しません。
ホスト側で 8080/5174 が使用中なら `.env` の `PORTAL_SERVER_PORT` /
`PORTAL_WEB_PORT` を変更します。bind address の既定値は loopback です。VM provisioning は
Server だけを Router control address に bind し、`0.0.0.0` へは公開しません。

## stand-in profiles

`PORTAL_ROUTER_MODE=stand-in` は内蔵 Router 役を一つだけ起動します。

| profile | 動作 |
|---|---|
| `success` | 各 desired snapshot を成功報告し、applied を収束させる |
| `fail-once-then-success` | 同じ snapshot の初回を失敗、次の試行を成功にする |
| `no-report` | 成否を報告せず、desired を巻き戻さずに pending/delayed を維持する |

例:

```bash
PORTAL_STANDIN_PROFILE=no-report \
docker compose -f portal-mock/compose.yaml up --build --wait
```

## external-router mode

実 Router または Router 開発用クライアントから公開 `portal.v1` 契約を確認する場合:

```bash
PORTAL_ROUTER_MODE=external-router \
docker compose -f portal-mock/compose.yaml up --build --wait
```

このモードでは内蔵 stand-in は停止します。起動/reset 直後の desired は既知ですが、
applied は Router の妥当な成功報告まで「不明／未同期」です。Router は以下を使用します。

- `POST /portal.v1.PortalRouterService/StreamDesired`（server streaming）
- `POST /portal.v1.PortalRouterService/ReportApplied`（unary）

Web コンテナの nginx は同一 origin の Connect 要求を Server へ proxy するため、
コンテナ外からは `http://localhost:5174/portal.v1.PortalRouterService/...` も利用できます。
Server へ直接接続する場合は `http://localhost:8080` を使います。

## Buf curl

以下はリポジトリルートで実行します。

```bash
PORTAL_SCHEMA=proto
PORTAL_BASE_URL=http://localhost:8080
ROUTER_RPC="$PORTAL_BASE_URL/portal.v1.PortalRouterService"
MOCK_RPC="$PORTAL_BASE_URL/portal_mock.v1.PortalMockService"

router_rpc() {
  mise exec -- buf curl --protocol connect --schema "$PORTAL_SCHEMA" \
    --data "${2:-{}}" "$ROUTER_RPC/$1"
}

mock_rpc() {
  mise exec -- buf curl --protocol connect --schema "$PORTAL_SCHEMA" \
    --data "${2:-{}}" "$MOCK_RPC/$1"
}
```

状態と国一覧:

```bash
mock_rpc GetState '{}' | jq
mock_rpc ListSupportedCountries '{}' | jq '.countries'
```

desired stream:

```bash
router_rpc StreamDesired '{"team":{"teamNumber":1}}'
```

設定変更（配置は server1=AP、server2=EU、server3=US に固定。リージョン更新は同値でも `invalid_argument`）:

```bash
mock_rpc UpdateDesired '{
  "patch": {
    "mainServer": "SERVER_ID_SERVER3",
    "defaultCountry": "US"
  }
}' | jq
```

external Router の成功報告:

```bash
cat >/tmp/report-applied.json <<'JSON'
{
  "team": {"teamNumber": 1},
  "success": {
    "snapshotId": "<StreamDesiredから受信した値>",
    "applied": {
      "serverRegions": {
        "server1": "REGION_AP",
        "server2": "REGION_EU",
        "server3": "REGION_US"
      },
      "mainServer": "SERVER_ID_SERVER3",
      "defaultCountry": "US"
    }
  }
}
JSON

mise exec -- buf curl --protocol connect --schema proto \
  --data @/tmp/report-applied.json \
  "$ROUTER_RPC/ReportApplied"
```

成功報告は snapshot ID の既知性や desired との一致では拒否されません。Config の構造・必須項目・
値域が妥当なら Router の実状態として applied になります。不正 Config は
`INVALID_ARGUMENT` となり applied を変更しません。

## reset

reset は認証なしのモック専用 RPC と UI 操作で常時利用できます。

```bash
mock_rpc Reset '{}' | jq
```

reset はベンチマークと stand-in の処理より優先され、処理をキャンセルして次へ戻します。

- server1/server2/server3 = AP
- main = server1
- default country = JP
- benchmark = idle / progress 0
- history = 空、実行回数 = 0、最高/直近スコア = 未設定
- 新しい snapshot ID を発行し、接続中の stream へ配信
- TeamKey と hostname hash/domain は維持
- stand-in は desired=applied、external-router は applied 未同期

永続化はありません。プロセス再起動も同じ設定値・ベンチ結果列へ戻します
（snapshot ID と時刻は毎回異なります）。

## 開発・検証

```bash
mise exec -- buf lint
manifest_dir="$(mktemp -d)"
cp gen/go/go.mod "$manifest_dir/gen-go.mod"
cp gen/ts/package.json gen/ts/package-lock.json "$manifest_dir/"
mise exec -- buf generate
cp "$manifest_dir/gen-go.mod" gen/go/go.mod
cp "$manifest_dir/package.json" gen/ts/package.json
cp "$manifest_dir/package-lock.json" gen/ts/package-lock.json
rm -rf "$manifest_dir"
git diff --exit-code -- gen/go gen/ts

go -C portal-mock/server test ./...
go -C portal-mock/server vet ./...

npm --prefix portal-mock/web ci
npm --prefix portal-mock/web run lint
npm --prefix portal-mock/web run typecheck
npm --prefix portal-mock/web test
npm --prefix portal-mock/web run build
npm --prefix portal-mock/web run test:e2e

docker compose -f portal-mock/compose.yaml config --quiet
```

詳細な E2E 手順は `specs/002-portal-mock/quickstart.md`、要件とテストの対応は
`specs/002-portal-mock/validation/traceability.md` を参照してください。

## 対象外

- 複数チーム、認証・認可、永続化
- 実 Router の内部実装、実際の通信遅延・帯域制御
- 実ベンチマーカーとのジョブ/結果通信
- 実負荷、採点式、競技スコア較正
- 意図的ボトルネック、想定解、非公開課題台帳
- 本番可用性、WAF、レート制限、長期監査ログ

`portal.v1` 契約と本モックの検証版を固定した後、実 Router と競技仕様へ別成果物として引き渡します。
