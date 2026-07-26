# Phase 0 Research: ISUCON2026 開発用モックポータル

**Feature**: `002-portal-mock`
**Date**: 2026-07-26

**参照**: `specs/002-portal-mock/spec.md`, `docs/geometrics-distribution.md`,
`specs/002-portal-mock/design/`

本ドキュメントは技術選択と実装パターンを固定し、Phase 1 設計の前提とする。

## R-01: 実装言語・フレームワーク

- **Decision**: モックサーバーは Go 1.26、フロントエンドは React 19 + TypeScript 5.9
  + Vite 8 とする。API 契約とスキーマは Protobuf で定義し、RPC は ConnectRPC、
  スキーマ管理・コード生成は Buf で行う。ただし BSR remote plugin／remote module は使用せず、
  Buf CLI、protoc、`protoc-gen-go`、`protoc-gen-connect-go`、`protoc-gen-es` を
  リポジトリ直下の `mise.toml` で固定し、ローカル実行する。
  Protobuf の正本はリポジトリ直下 `/proto` とし、本番 Portal・モック Portal・Router の
  共有契約を `proto/portal/v1`、モック固有補助契約を
  `proto/portal_mock/v1` に分離する。
  Go サーバーは `net/http`（server-streaming のため必要に応じ `h2c`）上に Connect
  handler を載せ、Echo など追加の Web フレームワークは使わない。リポジトリ直下の
  `portal-mock/server/` と `portal-mock/web/` に独立した `go.mod` / `package.json` を置く。
- **Rationale**: ポータルは Router と連携し、将来はベンチマーカーとも連携するシステム層である。
  ただし実ベンチマーカーとの通信契約は本フェーズでは定義しない。
  同じ Protobuf 契約から Go/TS の型と client/handler を生成できる Connect + Buf は、
  多言語コンポーネント間の契約整合と後方互換検査（`buf breaking`）に強い。Go は
  goroutine による stand-in、決定的乱数、単一バイナリ配布に適する。Connect は 1 つの
  サービス定義で connect / grpc / grpc-web を提供し、ブラウザからも server-streaming を
  扱える。共有 proto を feature ディレクトリや Portal 実装配下へ閉じ込めないことで、
  本番 Portal/Router が同じ契約を直接利用できる。
- **Alternatives considered**:
  - Echo + REST/OpenAPI + SSE: 当初案。手書き契約と多言語生成の整合コストが高く、
    Router/ベンチとの契約共有で ConnectRPC に劣るためチーム決定で不採用。
  - Node.js サーバー: Go 側のテスト・運用資産を再利用できず採用理由が弱い。
  - 既存 `backend/` / `frontend/` への組み込み: 本体の認証・ルーティングへ混入し
    FR-001 に反するため却下。

## R-02: Router 起点の Streaming Pull

- **Decision**: Connect の **server-streaming RPC** `PortalRouterService.StreamDesired` を採用する。
  Router がこの RPC を呼ぶと、接続直後に最新の desired スナップショットを 1 件受信し、以後は変更の
  たびに最新スナップショットを受信する。未処理の中間スナップショットは最新値へ coalesce してよい。
  applied 報告は unary RPC `ReportApplied` に分離する。
- **Rationale**: 通信は Portal から Router への一方向配信であり、FR-024 の
  「再接続時に最新値を必ず配信」「目標状態なので全スナップショットのリプレイ不要」に server-streaming が
  合う。Connect の server-streaming は HTTP/1.1・HTTP/2 双方で動き、Go 生成 client と
  ブラウザ（Connect-ES）双方から扱える。同一 Protobuf 契約を Router/ベンチと共有できる。
- **Alternatives considered**:
  - SSE + REST（当初案）: 手書き契約で多言語整合コストが高く、チーム決定で不採用。
  - Connect bidi streaming: desired 配信は一方向で十分、applied は unary が明確。
  - WebSocket: Connect の型付き契約・コード生成の利点を失う。
- **Contract versioning**: Router向け定義の版は Protobuf の **package 名** `portal.v1`
  で表す（Connect route は `/portal.v1.PortalRouterService/<Method>`）。メッセージ本文に
  契約版フィールドは持たせない。非互換変更は package `portal.v2` とし、
  未対応版のクライアントは route 不一致で接続を成立させない（FR-028）。モック固有契約は
  `portal_mock.v1` とする。Region、ServerId、TeamKey、Configは`portal.v1`を再利用し、
  PortalStateやCalculatedConfigなどUI固有の型は`portal_mock.v1`に定義する。

## R-03: スナップショット識別と applied 検証

- **Decision**: desired 変更ごとに、順序を持たず過去に再利用しないランダムな
  `snapshotId`（UUID 等）を付ける。Router の成功報告に含まれる snapshotId は、
  どの desired を適用対象としたかを示す相関情報として保持する。Portal は snapshotId の既知性や
  Config の desired との一致を受理条件にしない。構造・必須項目・値域が妥当な成功報告は
  applied へ反映し、不正な成功報告は `INVALID_ARGUMENT` で拒否して診断状態を
  `INVALID_REPORT` とする。
- **Rationale**: applied の SoT は Router なので、Router が報告した状態（以前のスナップショットへの後退を
  含む）へ Portal は忠実に従う。snapshotId が未知、Config が desired と異なるという理由で拒否すると、
  Router が実際に適用している状態を Portal が隠してしまう。snapshotId は順序や受理判定には使わない。
- **Alternatives considered**:
  - version の単調増加と「現在 applied より新しい報告だけ受理」: applied SoT に反し、
    Router の後退を隠して誤った状態を表示するため不採用。
  - snapshotId/Config の一致検証による報告拒否: Router の実状態を隠すため不採用。
  - 報告シーケンス番号による重複排除: 不要。

## R-03a: 成功報告と失敗報告

- **Decision**: `ReportAppliedRequest` は `oneof` で `ApplySuccess` と `ApplyFailure` を表す。
  `ApplySuccess` は snapshotId と実際の Config を持ち、Config が妥当ならAppliedConfigを更新する。
  Configが不正ならAppliedConfigを維持し、`INVALID_ARGUMENT`を返して`INVALID_REPORT`を記録する。
  `ApplyFailure` は snapshotId と理由を持ち、AppliedConfig を更新しない。
  `ReportAppliedResponse` は空とする。
- **Rationale**: 成功と失敗を分ける理由は受理結果を返すためではなく、必要な入力が異なるためである。
  成功時は実状態の Config が必要であり、失敗時は失敗理由が必要になる。response で
  `ACCEPTED_SUCCESS` / `ACCEPTED_FAILURE` を返すのは request の内容を繰り返すだけで利用価値がない。
- **Failure diagnostic**: 現在 desired の snapshotId に対する失敗だけを現在の失敗診断へ反映する。
  過去または未知の snapshotId に対する失敗は applied と現在の診断を変更せず、RPC は正常終了する。

## R-03b: チーム識別とホスト名の random-hash

- **Decision**: RPC の `TeamKey` は `teamNumber` だけとする。`randomHash` は Portal が保持する
  `TeamHostnameConfig` に置き、`CalculatedConfig.hostnames` の生成にだけ使用する。
  `teamNumber=0` は `INVALID_ARGUMENT`、起動時設定と異なる正の値は `NOT_FOUND` とする。
- **Rationale**: `teamNumber` は設定を検索するための識別子だが、`randomHash` の目的は公開ホスト名を
  推測しにくくすることであり、同じキーへ入れると責務が混ざる。Router は TeamKey を指定して設定を取得し、
  接続先には Portal が返す計算済みホスト名を使用する。
- **Security boundary**: random-hash は認証情報ではない。モックの RPC は開発環境内で認証なしとするが、
  本番でホスト名を返す RPC を公開する場合の認証・認可やネットワーク境界はサービス側で設ける。

## R-03c: 逐次報告の前提

- **Decision**: Router（および stand-in）は前の `ReportApplied` の応答を受けてから次を送る
  逐次報告とする。Portal は重複排除・順序付けを行わず、最後の成功報告を applied とする。
- **Rationale**: 単一 Router 前提では transport 上の reorder は起きず、順序管理を持たずに
  「最後の報告が真」で正しい。将来複数報告元が必要になった場合のみ別途検討する。

## R-04: 反映待ち・失敗・遅延

- **Decision**: applied が不明、または desired と applied が不一致なら反映待ち。
  収束判定は Config の一致で行い、snapshotId は判定に使わない。
  失敗報告は applied を更新せず、理由を記録して同じ desired スナップショットの再試行を許す。
  しきい値超過は「未反映／遅延」の診断表示だけに使い、desired を revert せず、
  時間経過だけで成功・失敗を確定しない。
  不正な成功報告は `INVALID_REPORT` と理由を保持する。後続の妥当な成功報告を受理したら
  理由をクリアし、収束なら `CONVERGED`、未収束なら経過時間に応じて `PENDING` / `DELAYED` とする。
- **Rationale**: FR-007、FR-016 と Clarifications の SoT 分離に一致する。
- **Alternatives considered**: タイムアウト時の自動 revert は仕様で禁止される。

## R-04a: desired 更新の冪等性とイベント発行

- **Decision**: `UpdateDesired` は、patch適用後のConfigが現在のdesiredと完全一致する場合、
  `changed=false` と現在のdesired／診断を返す正常なno-opとする。snapshot ID、desired、
  診断状態、pending開始時刻を変更せず、`StreamDesired`／`StreamState` にイベントを発行しない。
  空patchは同値更新とは区別し、`INVALID_ARGUMENT` とする。
- **Rationale**: 同じ目標値の再送で新しい版や反映待ち時間を作ると、Routerの不要な再適用と
  UI上の偽の状態変化を発生させる。FR-030の観測可能な冪等性をstore境界で保証すれば、
  unary応答と両streamの振る舞いを一貫させられる。
- **Alternatives considered**:
  - 同値でも新しいsnapshot IDを発行する: 目標状態に変化がないのに再適用を誘発するため不採用。
  - streamへ現在値を再配信する: SC-009のイベント発行0件に反するため不採用。

## R-05: stand-in と external-router

- **Decision**: 起動時に `stand-in`（既定）または `external-router` を排他選択する。
  `stand-in` は Router と同じ desired 購読・applied 検証経路を通し、次の決定的
  プロファイルを起動時固定で提供する。
  - `success`（既定）: 各 desired スナップショットを成功報告。
  - `fail-once-then-success`: 各スナップショットの初回を失敗し、同一スナップショットの再試行で成功。
  - `no-report`: 成否を報告せず反映待ちを維持。
  `external-router` では内蔵 stand-in を完全停止する。実行中のモード変更・UI 制御は
  提供しない。
- **Rationale**: FR-026、FR-029 を満たし、次フェーズで Portal 側ロジックを変更せず
  実 Router に差し替えられる。内部 stand-in も生成 Connect client 相当の
  `StreamDesired` / `ReportApplied` 契約と同じ検証を通す。
- **Configuration example**:
  `PORTAL_ROUTER_MODE`, `PORTAL_STANDIN_PROFILE`, `PORTAL_APPLY_TIMEOUT_MS`。

## R-06: reset と既知初期状態

- **Decision**: reset は本番共有定義へ含めず、モック固有の認証なし unary RPC
  `portal_mock.v1.PortalMockService.Reset` として常時提供する。最優先で
  進行中のベンチと stand-in を cancelし、メモリ状態とPRNGを初期化して初期desiredへ新しい
  snapshot IDを発行する。接続中の `StreamDesired` / `StreamState` は維持し、新しい初期状態を配信する。
  reset 前のタイマー・完了処理は context cancel で停止する。reset 前の snapshot ID を含む
  成功報告が届いた場合も、Router の実状態として applied へ反映する。
- **Initial state**: server1/2/3=AP、メイン=server1、デフォルト国=JP、
  benchmark=idle、進捗=0、履歴=空、実行回数=0、最高/直近スコア=未設定。
  起動時の TeamKey と TeamHostnameConfig は reset で変更しない。stand-in は desired=applied、
  external-router は applied=不明／未同期とする。
- **Rationale**: FR-022、FR-023、FR-027 と SC-004 の決定的再現性を満たす。

## R-07: ベンチマークシミュレーション

- **Decision**: 専用の固定シード PRNG から PASS/FAIL・スコア・ログの決定的な結果列を
  生成し、reset / 再起動で同じ先頭へ戻す。PASS と FAIL の双方を含め、結果注入専用
  API は設けない。状態は idle → pending → running → idle と遷移し、完了時に履歴先頭、
  最高/直近スコア、実行回数を更新する。
- **Rationale**: FR-025 と SC-005 の CI 再現性を保証する。
- **Alternatives considered**: グローバル乱数や時刻由来 seed はテスト間干渉と非決定性を
  生むため不採用。

## R-08: ロックと同時実行

- **Decision**: 反映待ち中またはベンチ実行中はベンチ開始を拒否する。実行中は
  リージョン・メイン・デフォルト国の全変更を拒否する。状態変更はサーバー内の単一
  ストア境界で同期し、UI と `GetState` / `StreamState` に同じロック理由を返す。
  Connect error code は設定ロック・開始不可に `FAILED_PRECONDITION`、入力不正に
  `INVALID_ARGUMENT` を用いる。
- **Rationale**: FR-012、FR-013 と geometrics 6 章を、競合時にも一貫して満たす。

## R-09: 状態取得と国一覧

- **Decision**: 認証なしの unary RPC `GetState` が desired、Router 報告済み applied、
  収束状態、pending/失敗/遅延診断、チーム識別、派生ホスト名、ロック、ベンチ状態、
  履歴、スコアを一括返却する。これらのPortal UI向けRPCは`PortalMockService`に置く。
  React SPA のライブ更新にはserver-streaming RPC `StreamState`を提供し、
  接続時と状態変化時に最新スナップショットを返す。`ListSupportedCountries`は
  10か国のコード・日本語名・US/AP/EU対応を返し、`UpdateDesired`はUIの設定変更を受け付ける。
  PortalState からも同じ一覧を参照可能にする。
- **Rationale**: FR-009、FR-010、FR-021 と SC-002 を満たす。applied へ未報告 desired
  を混入させないことで UI と Router の実状態を一致させる。実ベンチマーカー向けの
  client/service 契約は後続仕様で定義する。

## R-10: 国コード検証

- **Decision**: Protobuf 上の国コードは enum にせず、ISO 3166-1 alpha-2 の大文字2文字を
  表す `string` とする。初期データは仕様の10か国
  （US={US,CA,MX}、AP={JP,KR,CN,AU}、EU={GB,DE,FR}）だが、対応国追加は
  `SupportedCountry` データと仕様の更新だけで行い、proto の変更を要求しない。
  現在の SupportedCountry 一覧外、形式不正、国コードではない AP/EU は拒否し、
  既存 desired/applied を変化させない。
- **Rationale**: FR-018〜FR-021 と SC-006 を満たしつつ、国追加のたびに enum 追加・
  多言語コード再生成・互換性レビューが必要になる拡張阻害を避ける。許可集合は
  `ListSupportedCountries` とサーバー検証を正とする。
- **UI**: 参照デザインのデフォルト国 US/AP/EU セレクトを 10 か国コードへ置換し、
  対応表はツールチップまたは展開パネルでオンデマンド表示する。

## R-11: 参照デザインの再現

- **Decision**: `design/ISUCON Portal Dashboard.dc.html` のレイアウト、oklch 配色、
  Noto Sans JP / IBM Plex Mono、スコアカード、ベンチ実行、履歴、設定カード、
  Team 表示、モック注記を React コンポーネントでピクセル基準再現する。
  プロトタイプ内部のテンプレート構造はコピーせず、視覚結果だけを基準とする。
  クライアントタイマー式 pending は spec の desired/applied モデルへ置換する。
- **Rationale**: FR-002、FR-015 と design/README のハンドオフ指示を両立する。

## R-12: テスト戦略

- **Decision**:
  - Buf contract: `buf lint`、前版/公開済みモジュールに対する `buf breaking`、
    mise で固定したローカル plugin による決定的コード生成。比較元はローカルに展開した
    Git ref の `proto/` とし、BSR を利用しない。
  - Go unit/Connect contract/integration: TeamKey・snapshotId・Config の受理/棄却、後退報告の反映、
    StreamDesired の初回/更新/coalesce、fail/retry、設定変更→確定、ロック、
    reset cancel、決定的結果列、同値更新no-opとstream非配信、
    INVALID_REPORT後の収束／未収束診断回復。
  - React/Vitest: 状態表示、入力拒否、pending/applied 表示、国一覧 UI。
  - Playwright E2E: 設定変更→反映待ち→確定、ベンチ実行→履歴更新、実行中ロック、
    reset 後の既知初期状態。
- **Rationale**: 憲章 I/V、SC-001〜SC-010 に必要な正しさと CI 再現性を満たす。
  永続化、認証、レート制限、実負荷、実採点は明示的に後続成果物へ委任する。

## 解決済み Technical Context

- Language/Version: Go 1.26、TypeScript 5.9
- Primary Dependencies: Connect-Go、Protobuf-Go、Connect-ES、Protobuf-ES、
  Buf CLI、protoc、ローカル protoc plugin、mise、React 19、Vite 8、Go `net/http`
- Storage: プロセスメモリのみ（永続化なし）
- Testing: `buf lint` / `buf breaking`、Go test（Connect handler/client contract を含む）、
  Vitest、Playwright
- Target Platform: Linux 開発環境、モダンブラウザ
- Project Type: 独立 Web application（server + SPA）
- Performance Goals: 1 チーム開発用途。状態 API と UI 操作はローカル環境で即応し、
  Connect server-streaming 接続と単一 Router の更新を安定処理する。競技負荷性能は対象外。
- Constraints: 認証なし、1 チーム、メモリ保持、reset/再起動で決定的初期化、
  desired/applied SoT 分離、共有 `portal/v1` とモック固有 `portal_mock/v1` の責務分離、
  Protobuf v1 package 互換性、BSR remote機能不使用、ツール版は `mise.toml` で固定、
  実 Router/実ベンチ通信なし。
- Scale/Scope: 1 チーム、3 サーバー、10 か国、1 Router 接続、単一ポータル画面。

未解決の `NEEDS CLARIFICATION` はない。
