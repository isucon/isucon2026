# ポータルバックエンド Go rewrite 実装計画

作成日: 2026-08-23。更新日: 2026-09-24。
要件は [要件定義](portal-go-requirements.md)、移植元の調査記録は [portal 調査](../survey/portal/README.md) を参照する。
本書は現在の構成と、開催までに必要な検証を記載する。

## 0. 前提と方針

| 項目 | 現在の方針 |
|---|---|
| 開催日 | **2026年10月31日（土）**。単一開催で、予選は行わない |
| 開催時刻 | 開始、凍結、終了の時刻は未設定。確定後に `portal_overrides.timing` へ RFC3339 で設定する |
| 登録締切 | 既存要件の 2026-09-30 を維持する。締切時刻は公開前に設定する |
| ポータルの配置 | さくらのクラウド is1c の単一 VM。Ansible と Docker Swarm で配置する |
| DB / Redis | PostgreSQL と Redis を VM に同居させる。外部には公開しない |
| 非同期ジョブ | PostgreSQL の `jobs` テーブル。SQS は使わない |
| 選手環境 | 現行実装は AWS EC2 + CloudFormation、1 チーム 3 台。ポータルの配置とは別 |
| 外部連携 | GitHub、Discord、Slack、Web Push、EC2、アイコン保存先 S3 |
| データ移行 | `isucon2026-portal` の登録データを PostgreSQL に移行する。移行と公開切り替えは別日に行う |
| Rails への復帰 | 行わない。Rails と Go の同一 DB での並走比較も行わない |

protobuf の wire format、ルート、マスク値、スコアの意味を維持する。
旧大会の案内や感想戦用の例外は、通常開催の要件に合わせて見直す。

## 1. 技術選定

| 領域 | 実装 |
|---|---|
| HTTP | Go、chi、`html/template` |
| DB | PostgreSQL、`database/sql`、pgx、手書き SQL |
| スキーマ | `internal/db/schema.sql`。`PORTAL_AUTO_MIGRATE=1` で起動時に適用する |
| セッション | Redis に JSON を保存し、Cookie のセッション ID を HMAC-SHA256 で署名する |
| キュー | `FOR UPDATE SKIP LOCKED` による取得。失敗時は指数バックオフで再試行し、上限到達後は `dead` に残す |
| フロントエンド | React SPA、TypeScript、webpack、pnpm |
| protobuf | buf で Go と TypeScript を生成する。受け入れテスト用 Ruby は別テンプレートで生成する |
| 外部連携 | discordgo、AWS SDK for Go v2、webpush-go、`golang.org/x/crypto/ssh` |
| 観測性 | slog、Sentry、Prometheus `/metrics`、`/site/sha` |
| デプロイ | さくらの LB → nginx → Docker Swarm。Ansible で設定と更新を行う |

Rails のセッションとは互換しないため、切り替え後は全員の再ログインが必要になる。
現行のセッションは認証情報に加えて CSRF トークンと OAuth state を保存する。
`schema.sql` は初期スキーマを冪等に作成するものであり、既存テーブルの変更を自動適用する仕組みではない。

## 2. リポジトリ構成

```text
portal/
├── cmd/{portal,worker,bot,notifyloop}/
├── internal/
│   ├── app/             # 共有の依存初期化
│   ├── config/          # 環境変数
│   ├── contest/         # 時刻判定と leaderboard
│   ├── model/           # DB 操作と protobuf 変換
│   ├── handler/         # HTTP、認証、CSRF、ページ
│   ├── db/              # 接続と schema.sql
│   ├── session/         # Redis セッション
│   ├── cache/           # ダッシュボードキャッシュ
│   ├── job/             # PostgreSQL キューとジョブ
│   ├── cloudformation/  # 選手環境のテンプレート
│   └── external/        # 外部サービス
├── frontend/            # SPA、素材、静的アセット
├── proto/               # API 定義
├── gen/pb/              # Go バインディング
├── tests/               # contract、integration、concurrency
├── rspec/               # HTTP 受け入れテスト
└── deploy/              # Dockerfile、Ansible、Terraform
```

`portal/` で `buf generate` を実行すると `gen/pb` と `frontend/proto` を更新する。
Ruby の生成には `buf generate --template buf.gen.ruby.yaml` を使う。

## 3. 守る契約

[要件定義の CT-1〜11](portal-go-requirements.md) を基準とする。
特に protobuf のエラー形式、CSRF、manifest によるアセット解決、Web Push のペイロード、304 応答、非 admin へのマスク、スコアのセンチネルを回帰テストで守る。
DB は移植元の列の意味を保ちつつ PostgreSQL の型に移し、重複 enqueue とチーム単位の survey に一意制約を持つ。
Redis キーの round セグメントは単一開催に合わせて除去している。

## 4. 実装と残作業

Go への移植範囲は [portal README](../../portal/README.md#実装範囲) に記載している。
ルートの実装済みという状態と、本番環境での受け入れ完了は区別する。

| 対象 | 残作業 |
|---|---|
| 通常開催への復旧 | 環境確認状態、登録画面、CheckerToken の期限検証を修正する（[Issue #16](https://github.com/isucon/isucon2026/issues/16)） |
| 外部連携 | OAuth、Discord、Slack、S3、Web Push を実接続で確認する |
| ベンチ | 実 supervisor で enqueue から結果反映まで一巡させる |
| 選手環境 | AMI と CloudFormation の開催向け確認。テンプレートの更新は今回保留 |
| データ移行 | 移行リハーサル、件数と関連の照合、復元手順の確認 |
| 負荷 | 900 チーム以上でダッシュボードと leaderboard を実測する |
| 運用 | バックアップ、復元、更新失敗時の復旧、運営限定モード解除を確認する |

## 5. スケジュール

開催日は **2026年10月31日（土）**。
旧計画の 11 月中の切り替えや 11 月の予備期間は使わない。
登録締切後、開催前にデータ移行と公開切り替えを別日に実施し、リハーサルの時間を確保する。
具体的な作業日と担当者は未確定であり、本書で完了日を仮定しない。

## 6. テスト戦略

| 検証 | コマンド / 対象 |
|---|---|
| 単体と契約 | `make test`。署名、マスク、スコアなどの意味を検証する |
| 結合 | `make test-integration`。PostgreSQL と Redis に接続して API とキューを検証する |
| 並行性 | `make test-concurrency`。重複 enqueue、払い出し、結果確定、キュー取得の排他を検証する |
| HTTP 受け入れ | `make rspec`。Go サーバを対象とし、leaderboard、登録、bench、envcheck などを検証する |
| フロントエンド | `cd frontend && pnpm test && pnpm typecheck`、`make frontend` |
| 実接続 | supervisor、envcheck、OAuth、Web Push などを対象環境で確認する |

旧 `contest_spec.rb` の `history: false` 欠落は移植時に修正済み。
RSpec は PostgreSQL にテストデータを作成し、Go の HTTP API を検証する。
Rails と Go を同一 MySQL に接続する差分テストは廃止した。
テスト用のセッション注入と時刻上書きは `PORTAL_TEST_MODE=1` に限定する。
修正時は期待する挙動の失敗テストを先に書き、実装とリファクタリングを行う。

## 7. カットオーバー

1. PostgreSQL への登録データ移行をリハーサルし、件数、関連、同意記録を照合する。
2. ステージングで HTTP 受け入れ、実接続、負荷、復元を検証する。
3. 登録締切後の切り替え日を決め、再ログインが必要であることを事前に告知する。
4. バックアップを取得し、[Ansible の手順](../../portal/deploy/ansible/README.md)で Go の各サービスを配置する。
5. 開催日時、公開 URL、外部連携を確認してから運営限定モードを解除する。
6. 公開後のログ、ジョブ、登録状況、ダッシュボードを確認する。

Rails 版へのロールバックは行わない。
障害時は運営限定モードで影響を抑え、Go 版の前リビジョンへの更新またはバックアップ復元を行う。
DB 変更との互換性と復元後のデータ損失範囲は、事前に復旧手順で確認する。

## 8. リスクと打ち手

| リスク | 対応 |
|---|---|
| 順位計算や可視性の誤り | 凍結前後、自チームと他チーム、競技終了後を HTTP 受け入れで検証する |
| 感想戦用の挙動が残る | Issue #16 の修正と回帰テストを通常開催の条件で確認する |
| 通知や OAuth の接続不備 | 実アカウントと実ブラウザで確認する |
| 単一 VM 障害 | 別障害領域にバックアップし、復元を試験する。現行構成は HA を提供しない |
| 本番規模での性能不足 | 900 チーム以上のデータで測定する |
| 移行と公開の同時実施 | 別日に分け、復元点を用意する |

## 9. 旧計画からの変更

2026-09-14 に Rails への復帰を行わない方針とし、DB を PostgreSQL、キューを PostgreSQL の `jobs` テーブルに変更した。
ポータルの配置はさくらのクラウドと Ansible / Docker Swarm に移行している。
ECS / ecspresso、SQS、同一 DB 上の Rails 並走を前提にした旧手順は適用しない。
旧システムの調査結果は `docs/survey/portal/` に履歴として残す。

## 10. 公開前の確認先

- [portal README](../../portal/README.md): 開発、テスト、実装範囲。
- [デプロイ README](../../portal/deploy/README.md): 構成と本番投入前の条件。
- [Ansible README](../../portal/deploy/ansible/README.md): 設定、秘密情報、更新、バックアップ。
- [外部連携一覧](../../portal/deploy/external-integrations.md): アカウントと設定値。

## 11. 実装メモ（コードを読んで確定させた仕様）

仕様書が無いためコードからしか読めない事項のうち、**調査で確定できたもの**。
Ruby のコード例は移植元の調査記録であり、現行 Go 実装のコードではない。
現行実装の受け入れ条件は要件定義とテストを参照する。

### 11.1 `score == -1` はセンチネル ★最重要

`BenchmarkResult#update_from_pb!` の実装。

```ruby
score: pb.score == -1 ?
         (pb.finished && !pb.passed ? 0 : (raw || 0) - (deduction || 0))
       : pb.score
```

ベンチマーカーが `score: -1` を送ると、**`score_breakdown.raw - deduction` で計算する**。
`-1` 以外ならその値をそのまま採用する。加えて `finished && !passed` なら 0 に落とす
（`set_score_zero_when_failed` でも二重に落としている）。

**proto ファイルにコメントすら無い暗黙の契約。**落とすとスコアが壊れる。

### 11.2 可視性スコープの正確な定義

```ruby
scope :visible_not_frozen, ->(team) {
  where('(team_id = ? OR marked_at < ?)', team&.id, contest_freeze)
}
scope :marked_before_contest_ended, -> { where('marked_at < ?', contest_end) }
```

| visibility | 適用されるスコープ | 意味 |
|---|---|---|
| `audience` | `marked_before_contest_ended` + `visible_not_frozen(nil)` | 凍結後は誰のスコアも見えない（`team_id = NULL` は決してマッチしない） |
| `contestant` | `marked_before_contest_ended` + `visible_not_frozen(team)` | **自チームだけ凍結後も見える。他チームは凍結前まで** |
| `admin` | なし | 無制限 |

`contest_freeze` / `contest_end` が未設定なら `where('1=1')` で素通し。

### 11.3 `teams.student` の導出規則はコードにある

```ruby
def members_all_student? = members.size > 0 && members.all?(&:student?)
before_validation :generate_student_status   # student が nil のときだけ計算
```

**「メンバーが 1 人以上いて、全員が学生」なら true。**
`Team#student?` はカラムが nil のとき動的に計算する。
`04-data-migration.md` で「導出規則を決める必要がある」としたが、答えは既存実装にある。

### 11.4 予選 / 本選の分岐は実装しない（単一開催）

isucon14-portal では `ISUXPORTAL_FINAL=1` が以下すべてを切り替えていた。

```ruby
scope :promoted, -> { config.x.contest.final ? where(final_participation: true) : where('1=1') }
def promoted? = config.x.contest.final ? final_participation : true
```

**ISUCON2026 は予選を行わず単一開催なので、この分岐を Go に持ち込まない。**
消える／固定になるものは以下。

| 対象 | isucon14-portal | Go 実装 |
|---|---|---|
| `ISUXPORTAL_FINAL` | 環境変数で分岐 | **廃止** |
| `Team.promoted` scope | `final ? where(final_participation: true) : where('1=1')` | **撤廃**。`Team.active` のみで判定する |
| `Team#promoted?` | 同上 | **撤廃**。`require_promoted_contestant` は `active?` だけ見る |
| Redis キャッシュキー | `dashboard-v2:{qualify\|final}:...` | `round` セグメントを落とす（§3 参照） |
| `extra_time_assignments.final` | `where(final: config.x.contest.final)` で引く | **常に `false` で読み書き**（理由は後述） |
| CloudFormation テンプレート | `test` / `qualify` の 2 種 | 環境確認用 `test` と競技用 `contest` の 2 種。AMI も別設定 |
| Discord ロール | `contestant_qualify_role*` / `contestant_final_role*` の 2 系統 | **1 系統に統合** |
| `dump_leaderboard` の `when` | `qualify-end` を特別扱い | `contest-end` にリネーム |

`teams.final_participation` カラムは **DB と proto に残す**。
フロント（`AdminTeamList` / `TeamList`）が表示に使っているため。
チーム作成時に `true` を入れ、**絞り込みには使わない**。

#### `extra_time_assignments.final` を `false` 固定にする理由

このテーブルは `[team_id, final]` に unique index を持つ。
既存データの意味を維持し、単一開催では `final=false` の行を読み書きする。
Rails との並走比較は行わない。

### 11.5 非 admin へのマスク値

`BenchmarkResult#to_pb(admin:)` と `BenchmarkJob#to_pb(admin:)` のマスク。

| フィールド | admin | 非 admin |
|---|---|---|
| `execution.stderr` | 実値 | `""` |
| `execution.exit_status` | 実値（nil なら -1） | **`-2`** |
| `execution.exit_signal` | 実値（nil なら -1） | **`-2`** |
| `execution.signaled` | 実値 | `false` |
| `instance_name` | 実値 | `""` |
| `post_validation` | 実値 | `nil` |

`-2` は「非開示」を表す埋め草。フロントは `{admin ? ... : null}` で
表示自体をガードしているので **`-2` を読んでいない**。
意味的な依存は無いが、API のマスク契約（CT-6）を維持するために Go も `-2` を入れる。

### 11.6 proto の `*Query` メッセージは使われていない

```ruby
def pb
  klass.decode(request.raw_post)   # ボディのみ
end
```

`ProtobufData#pb` はリクエストボディを decode するだけ。
**GET のクエリパラメータは通常の `params[:team_id]` で読んでいる。**
proto に定義された `GetRegistrationSessionQuery` / `ListBenchmarkJobsQuery` などは
**定義があるだけで実際には使われていない**。routes.rb のコメントに釣られないこと。

### 11.7 重複 enqueue 防止はアプリ層のバリデーション

```ruby
def validate_no_concurrent_jobs
  if (pending? || running?) && where(team_id:, status: %i(pending running)).where.not(id:).count > 0
    errors.add :team_id, "must have no other concurrent job"
  end
end
```

`COUNT` を見ているだけで **DB 制約ではない**。同時リクエストで抜けうる。
Go では `SELECT ... FOR UPDATE` か部分ユニークインデックスで明示的に塞ぐこと。

### 11.8 ジョブ状態遷移

```
pending --start!(instance_name)--> running --submit_result_from_pb!--> finished / errored
   |                                  |
   +--cancel!--> cancelled            +--error!--> errored
```

- `closed?` = `errored? || cancelled? || finished?`
- `handle` は `instance_name` がセットされたときだけ生成（`SecureRandom.urlsafe_base64(64)`）
- `errored` の判定は `result.errored?` = `finished && (signaled? || exit_status != 0)`
- **`error!` は自分で `BenchmarkCompletionJob` を enqueue するが、
  `submit_result_from_pb!` は呼び出し側（コントローラ）が enqueue する。** 非対称なので注意

### 11.9 `BenchmarkResult` の不変条件

| バリデーション | 内容 |
|---|---|
| `validate_immutable_after_finish` | finished 後の変更を拒否（**development 環境では無効**） |
| `validate_marked_at_linear` | `marked_at` は単調増加のみ |
| `validate_team_must_not_change` | `team_id` の変更を拒否 |
| `validate_finished_and_passed` | `finished` なら `passed` が nil でないこと |

### 11.10 その他

- **チーム上限 3 人はハードコード**。`joinable?` が `members.count < 3`
- **`benchmark_executions` はデッドテーブル**。`schema.rb` にあるだけでモデルも参照も無い。移植不要
- **`config.x` の全量は `config/environments/development.rb` の 70〜137 行にある**。
  Go の config 構造体はこれを写せばよい。`production.rb` は同じキーを
  `ENV.fetch`（必須化）にしているだけ
- **AZ は `['apne1-az1', 'apne1-az2', 'apne1-az4']` がコードに直書き**。
  チーム作成時に `team.id % zones.size` で割り当てる

---

## 12. 未確定事項

開催日の開始、凍結、終了時刻、移行と公開切り替えの日程、運営限定モードの解除時期を確定する必要がある。
外部連携と選手環境の確認事項は要件定義 §7 とデプロイ文書で管理する。
