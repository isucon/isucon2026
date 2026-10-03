# Implementation Plan: ISUReal Social Application

**Branch**: `001-isureal-social-app` | **Date**: 2026-07-19 | **Spec**: [spec.md](spec.md) v1.0.5 (Approved 2026-07-19)

**Input**: Feature specification from `specs/001-isureal-social-app/spec.md`

## Summary

3地域の日次投稿期間を軸に、JPEG画像一枚の投稿、3段階の公開範囲、友達・フォロー・ブロック、
RealMoji・コメント、Memories、アプリ内通知とWeb Pushを提供するデスクトップChrome向けSNSを実装する。
共通のGo/EchoコードベースからAPIとワーカーの2プロセスを構築し、PostgreSQLの制約付きトランザクション、
メタデータ除去済み画像の不変ファイル、React/TypeScript SPAを採用する。認可判定と投稿期間判定は
ドメインサービスへ集約する。

## Technical Context

**Language/Version**: Go 1.26.5（API・ワーカー）、TypeScript/React（Webクライアント）、SQL

**Primary Dependencies**: Echo v5 (`github.com/labstack/echo/v5`)、pgx、Go標準`image/jpeg`、Web Pushライブラリ、
React、Vite、Service Worker / Push API

**Storage**: PostgreSQL 16以降（正規データ・セッション・ジョブ状態）、ローカル永続ファイル領域
（メタデータ除去済みJPEG。DBには不変オブジェクトキーとハッシュを保存）

**Testing**: `go test`（単体・DB統合・契約・競合）、Vitest、Playwright（実装中はT004で記録したbootstrap版を固定し、
リリース判定開始時にT133で最新安定版として再解決したデスクトップChromeを正確な版とコンテナイメージdigestで固定）、OpenAPI検証、forward-only
マイグレーションのトランザクション/失敗時rollbackテスト、完全reset/初期化テスト

**Target Platform**: Linuxサーバー、リリース判定時に版固定する最新安定版デスクトップChrome

**Project Type**: Webアプリケーション（SPA + JSON/multipart API + バックグラウンドワーカー）

**Performance Goals**: 本フィーチャーでは数値目標を定義しない。別の競技仕様が負荷モデル、環境、
測定区間、スコア帯とともに定義する。

**Constraints**: 内部ワーカーを除く全状態変更HTTP操作は24時間の冪等性、投稿・関係・通知の競合時も一意性を維持、単一有効
セッション/Push購読、論理Push通知ごとに初回+最大3再試行とクライアント重複排除、JPEG 10 MiBまたは2 MiB、
画像寸法による拒否禁止と隔離サニタイザの資源障害時503・無変更、
固定UTCオフセット、1ページ50件のキーセットページング、アプリ内レート制限なし、機微情報をログに残さない、
FR-044のBearer限定・同一Origin・非キャッシュ・SPAセキュリティポリシー・IndexedDBトークン境界

**Scale/Scope**: 44機能要件、33成功基準、5ユーザーストーリー、3地域、ユーザー/管理者API、
約15の中核エンティティ。利用者数・投稿数・同時実行数は競技仕様で確定する。

## Constitution Check

*GATE: Phase 0前に評価し、Phase 1後に再評価する。*

- **Specification stage — PASS**: 2026-07-19承認済みの`spec.md` v1.0.5を固定した、Constitution 2.0.0の
  「アプリ基準仕様」段階である。正しい基準実装、
  公開契約、自動テスト、決定的初期化、版固定、後続競技仕様への引渡しを成果物とする。
- **Competition correctness — PASS**: `spec.md`を公開機能、認証・認可、およびデータ不変条件の規範的な正本とする。
  `contracts/openapi.yaml`と`data-model.md`は、それぞれ公開API境界と制約・状態遷移を実行可能な形へ具体化する
  派生成果物であり、`spec.md`と不一致がある場合は実装前に`spec.md`へ合わせて同期する。初期実装と将来の
  最適化へ同じ単体・契約・DB統合・E2E・競合テストを適用する。
  特に投稿の一意性、相互申請、ブロック優先、RealMoji置換、単一セッション、通知重複防止をDB制約と
  トランザクション競合テストで検証する。
- **Bottleneck architecture and competition handoff — PASS**: 本フィーチャーは意図的な性能問題を導入しない。
  将来の競技仕様が、フィード検索、友達距離2探索、通知ファンアウト、画像配信、ユーザー検索、
  Memories、完全削除を、リポジトリ/サービス/ワーカー境界で個別に差し替え・設定可能な挿入点として利用する。
  問題ID、原因、強度、無効化方法は非公開の競技課題台帳だけに置く。
- **Stage-appropriate quality boundary — PASS**: 認証・認可、入力検証、データ整合性、機微情報のログ除外、
  正しさの診断に必要な構造化ログを必須とする。レート制限、WAF、可用性目標、障害対応、ログ保持基盤、
  メトリクス/トレース、保存時暗号化の鍵管理は本番運用要件ではなく、デプロイ環境または競技仕様へ明示的に委ねる。
- **Benchmark reproducibility handoff — PASS**: アプリ側は再現可能なforward-only DBマイグレーション、初期化、
  注入可能な時計、決定的カーソル、構造化ログ、固定オフセットを提供する。負荷、初期データ量、乱数種、採点式、
  実行環境、資源指標、記録コマンドは参照するアプリ版を固定した別の競技仕様で確定する。
- **Quality and information separation — PASS**: ドメイン単体、OpenAPI契約、実DB統合、競合、
  Playwright E2E、マイグレーション/初期化、Push部分障害のテストを分離する。公開アプリ成果物には
  ボトルネック台帳・想定解・較正結果を含めず、配布監査は競技featureで実施する。

### Post-design re-check

Phase 1設計後も上記判定を維持する。`spec.md`を規範的な正本とし、API契約は公開境界を、データモデルは
制約とトランザクション境界を、Quickstartは同じ機能判定の再実行方法を具体化する。派生成果物に不一致が
見つかった場合は`spec.md`へ合わせて同期してから実装する。後続段階へ引き渡した競技事項は
Constitution 2.0.0上の例外ではない。本フィーチャー内に数値目標や非公開課題を混入させず、別競技仕様の
承認なしに競技配布へ進めない。

## Architecture and Transaction Boundaries

1. Echo v5のHTTPハンドラー/ミドルウェアは認証、入力形式、`Idempotency-Key`を検証し、ユースケースサービスを呼ぶ。
   `GET`等の読み取り専用操作と内部ワーカーを除く全`POST`/`PUT`/`PATCH`/`DELETE`でキーを必須とする。
   認証済み要求は主体種別・主体ID・操作種別、未認証の自己登録と一般/管理者ログインは認証種別・
   正規化済みユーザー名・操作種別をスコープとする。OpenAPIの各変更操作に必須ヘッダーと409応答を明示し、
   契約テストが例外のないことを検査する。503では冪等性レコードを確定せず、変更をロールバックして
   同じキーの再実行を許可し、最初の503以外の確定結果から24時間を計測する。
2. ドメインサービスは現在時刻を注入されたClockから取得し、認可ポリシーと状態遷移を一度だけ判定する。
3. PostgreSQLトランザクションは対象ユーザー、投稿期間、関係ペア等を一貫した順序でロックし、
   一意制約と条件付き更新で並行操作を直列化する。
4. 画像は一時領域で用途別の圧縮ファイル容量を上限として読み取り、JPEG検証後、APIプロセスとは別の
   一回限りの画像サニタイザプロセスでデコード・再エンコードする。API側はサニタイザの同時実行数を制限し、
   子プロセスには設定可能なメモリ、CPU、処理時間上限をOS境界で適用する。この読み取り上限は圧縮ファイルの
   バイト数だけに適用し、展開後の画素数または縦横寸法を固定的な拒否条件にしてはならない。実行枠不足、
   資源上限到達、異常終了、またはタイムアウトは再試行可能な503へ変換し、子プロセスと一時ファイルを回収して
   DB行および公開ファイルを残さない。テスト用プロファイルは仕様が許す大寸法JPEGを処理できる資源を与える一方、
   障害注入プロファイルでAPI生存、503、原子性、再試行を検証する。
   処理済み画像はアカウント単位の不変ファイルとして
   原子的renameで確定してからDB参照を記録する。DB確定前には配信せず、完全削除時は参照行と画像を
   同期的な削除手順で処理し、失敗時は削除確定を再試行可能状態に保つ。
5. 通知開始、Push配信、期限到来アカウント検出はDB上のジョブ状態を持つワーカーが`FOR UPDATE SKIP LOCKED`で取得する。
   アプリ内通知を先に一意確定し、その通知IDを論理Push通知IDとして初回送信と最大3回の再試行で維持する。
   Service Workerは同じ論理通知IDを通知タグとして使用し、外部送信結果が不明な再試行でも表示を一件へ収束させる。仕様上`Notification`に含まれる
   Push配信状態、試行回数、および最終試行時刻は、実装データモデルでは通知単位の`PushAttempt`へ正規化し、
   `Notification`との参照整合性と通知ごとの重複防止単位をトランザクションで維持する。地域ごとの期限処理は
   ロック対象とジョブ状態を分離し、ある地域の保留・失敗・再試行を別地域の通知開始トランザクションへ伝播させない。
6. フィード・関係・検索・Memoriesは複合ソートキーを署名付き不透明カーソルへ符号化し、常に50件以下を返す。
7. ユーザーと管理者のセッショントークンは256-bitの不透明Bearer値とし、`Authorization: Bearer`ヘッダーだけから
   受理する。Cookie、URL query、form bodyから資格情報を受理せず、DBにはトークンのハッシュだけを保存する。
   Webクライアントはブラウザ終了後もセッションを継続できるようユーザートークンを当該OriginのIndexedDBへ保存するが、
   URL、DOM、ログ、エラー、解析イベントへ複製しない。ログアウト、セッション置換、アカウント削除では永続トークンも削除する。
   SPA、API、認可画像は同一Originで配信し、CORS許可ヘッダーを返さない。状態変更要求に`Origin`がある場合は設定済みの
   公開Originとの完全一致を必須とし、`Sec-Fetch-Site`がある場合は`same-origin`だけを許可する。両ヘッダーがない
   非ブラウザAPIクライアントはBearer認証と通常の認可条件で判定する。これによりCookie依存のCSRF状態を作らず、
   cross-originのブラウザ要求を拒否する。
   `/api/v1`の全応答と認可画像応答には`Cache-Control: no-store`、`Pragma: no-cache`、
   `Referrer-Policy: no-referrer`、`X-Content-Type-Options: nosniff`を付与する。SPA応答には
   `Content-Security-Policy: default-src 'self'; img-src 'self' blob: data:; media-src 'self' blob:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'; worker-src 'self'`、
   `Permissions-Policy: camera=(self)`を付与する。これらの規則は成功・エラーの両応答で回帰テストする。

## Project Structure

### Documentation (this feature)

```text
specs/001-isureal-social-app/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── bereal-differences.md
├── contracts/
│   └── openapi.yaml
├── validation/              # 実装タスクで検証記録を追加
└── tasks.md
```

### Source Code (repository root)

```text
backend/
├── cmd/
│   ├── api/
│   ├── worker/
│   ├── image-sanitizer/
│   ├── migrate/
│   ├── reset/
│   └── seed/
├── internal/
│   ├── domain/
│   ├── service/
│   ├── repository/
│   ├── httpapi/
│   ├── image/
│   ├── push/
│   ├── config/
│   ├── worker/
│   ├── clock/
│   └── observability/
├── migrations/
└── tests/
    ├── contract/
    ├── integration/
    ├── concurrency/
    └── testkit/

frontend/
├── src/
│   ├── app/
│   ├── features/
│   ├── api/
│   └── service-worker/
├── tests/
│   ├── unit/
│   └── e2e/
└── scripts/

deploy/
├── compose.yaml
└── config/

.github/
└── workflows/

Makefile
```

**Structure Decision**: APIとワーカーは同じGoドメイン/リポジトリを共有し、実行バイナリだけを分ける。
SPAは公開契約から生成した型を利用する。競技仕様が後からデータアクセスやワーカー実装を差し替えられるよう、
HTTP、サービス、リポジトリ、外部Push、画像保存の境界を保つ。

T004では実装・テスト開始用のbootstrap Chrome完全版と不変イメージdigest、および外部入力された版を検証して
固定する仕組みを記録する。このbootstrap版をリリース対応版とはみなさない。Chromeの「最新安定版」は
T133によるリリース判定開始時に一度だけ再解決する。解決したブラウザの完全な版、
Playwright/Chrome実行イメージの不変digest、確認日をCI、Quickstart検証結果、版マニフェストへ記録し、
同じリリース判定中および将来の競技仕様による再検証ではそのdigestを使用する。新しい安定版への更新は、
別の明示的な版更新として全Playwrightシナリオを再実行した場合だけ行う。

## Testing Strategy

- ドメイン単体: 公開範囲、地域固定時刻、Unicodeコードポイント、入力境界、通知遷移先。
- DB統合: 全一意制約、キーセットページング、削除猶予/復旧/完全削除、全状態変更HTTP操作の冪等性24時間。
- 競合: 同時投稿、2回目削除対再投稿、相互申請、承認対取消、ブロック対交流、同時ログイン、
  RealMoji置換、通知開始、購読更新。
- 契約: OpenAPIに対する全成功/エラー応答、一般/管理者認証の分離、multipart上限。
- エラー契約: FR-027のHTTP状態、RFC 9457 `type`、機械判定用`code`、相関ID、503だけの再試行可能性を
  OpenAPIと契約テストで固定する。
- E2E: 5ユーザーストーリーとキーボード操作、Service Workerを含む通知開封。
- セキュリティ境界: FR-044/SC-033に基づくBearer以外の資格情報源の拒否、同一Origin/Fetch Metadata判定、CORS非許可、
  API・認可画像の非キャッシュ、SPAのCSP/Permissions-Policy、IndexedDBトークンの削除と非露出。
- 障害注入: Push一時/恒久/結果不明と論理通知重複排除、ワーカー再起動、画像サニタイザの枠不足・資源超過・
  異常終了・タイムアウト、画像確定後DB失敗、完全削除の再実行。

## Complexity Tracking

現在段階で必須のConstitution Check違反はない。競技較正・再現条件はConstitution 2.0.0で定義された
後続の競技仕様段階へ引き渡すため、例外として扱わない。
