# ISUCON ポータル実装調査

調査日: 2026-08-21。ISUCON2026 のポータルをどう用意するかを判断するため、
isucon org の既存ポータル実装を調査した。

主目的は **バックエンドのみを Go に rewrite する場合の作業量**の見積り。

## 前提

以下は確定している。

1. **ポータルのホスティングは AWS で構わない**（さくらへの移設は要件ではない）
2. **選手の競技環境は AWS**（EC2 + CloudFormation）。1 チーム 3 台
3. **既存の登録データはマイグレーションする**（移行先の確定は保留）

## 調査対象

| リポジトリ | commit | 日付 | 位置づけ |
|---|---|---|---|
| `isucon/isucon14-portal` | `4c2b99a` | 2024-12-13 | **isuxportal 系の最新。ISUCON14 で実運用。読むべき実装はこれ** |
| `isucon/portal` | `9b9dbaa` | 2022-08-26 | ISUCON11 ポータルの汎用ベース。2022 年で停止 |
| `isucon/isucon2026-portal` | `49105f2` | — | Django 系。**現在稼働中**（登録受付中） |

## 文書

| ファイル | 内容 |
|---|---|
| [`01-lineage.md`](01-lineage.md) | 二系統（isuxportal / Django）の対比と、`isucon/portal` → `isucon14-portal` の差分 |
| [`02-features.md`](02-features.md) | 機能台帳 125 件。実装の有無・Go 移植難度・備考 |
| [`03-routes.md`](03-routes.md) | ページ 25 件とエンドポイント 107 件の全量。ERB の使用範囲、外部プロセスが叩く口 |
| [`04-data-migration.md`](04-data-migration.md) | `isucon2026-portal` → isuxportal の登録データ移行。フィールド単位の対応表 |
| [`05-go-backend-rewrite.md`](05-go-backend-rewrite.md) | **バックエンドのみ Go rewrite の見積り。本調査の主眼** |

実装計画は [`../../plan/portal-go-rewrite.md`](../../plan/portal-go-rewrite.md) に分けて置いた。

## 要約

### バックエンドのみ Go rewrite の見積り

| 項目 | 値 |
|---|---|
| 移植対象の Ruby | **4,858 行** |
| 想定する Go 実装 | **8,200〜11,600 行** |
| 工数 | **48〜77 人日 ≒ 2.5〜4 人月** |
| 1 人体制 | 間に合うがリハーサルの余裕が薄い（競技日まで約 14 週） |
| 2 人体制 | 余裕がある |

書き直さないもの: React SPA 9,957 行、supervisor（Rust）、envcheck（Go）、
benchrun（Go）、protobuf 定義、Terraform 1,634 行、ecspresso 一式。

**API 契約が protobuf なので、サーバを Go に置き換えてもフロントエンドと
ベンチマーカーは実装言語を知らない。**これが見積りが小さく収まる理由。

### ERB はほとんど使われていない

`isucon14-portal` の ERB は 10 ファイル 241 行しかなく、
**サーバレンダの実ページは運営用の 3 画面（合計 173 行）だけ**。
残りは SPA シェル 1 枚と、JS バンドルを選ぶだけの 1〜3 行のファイル、
それに未使用の書きかけ 1 つ。

Go に移す際、**テンプレートエンジンの移植という論点はほぼ発生しない。**

### 最難関は `contest.rb` の 274 行

leaderboard 生成。可視性 3 系統（選手 / 観客 / 運営）× 凍結の組み合わせ、
`teams` の非正規化カラム 3 本とそれを維持するジョブ、生 MySQL のストリーミングクエリ、
protobuf でエンコードした合成 ETag、Redis への事前生成が一つの塊として動いている。

**この 274 行は 2022 年から 2024 年まで diff がゼロ。**
ISUCON12 と 14 を無変更で通しており、仕様が固まっている。Go 移植の仕様凍結点として使える。

### 最大のリスクは実戦検証を失うこと

`isucon14-portal` は ISUCON14 本番を通した実装である。
RSpec は 818 行で 2022 年から増えていないが、**中身は偏っている**。
`contest_spec.rb` 465 行が `.leaderboard` を集中的に検証しており
（凍結 × 可視性 3 系統を含む）、残る model spec 11 本は `pending` の 5 行スタブ。

**最も危険な部分にだけテストがある。**移植ではこの 465 行を HTTP 越しに転用して
受け入れ条件にし、残りは Rails と Go の差分テストで埋める。
ただし `["without history", true]` というバグがあり、
`history: false`（本番のダッシュボード生成経路）は未検証なので新規に書く必要がある。

### rewrite しない選択との比較

AWS 依存の解消が要件から外れ、**supervisor は Rust のまま維持する方針が確定した**ため、
Go rewrite で減る運用言語は **Ruby ひとつだけ**になる（Rust はどちらの道でも残る）。

**したがって動機は「Ruby を運用しない」「Rails 6.1 を抱えない」の 2 点**であり、
実質は「Rails 6.1.6 を抱えたまま当日を迎えるか」の一点に近い。
これに 2.5〜4 人月と実戦検証の喪失を払う価値があるかどうかが判断になる。

`isucon14-portal` をそのまま運用する場合の残作業は、
ISUCON2026 用 AMI / CloudFormation の作成、ISUReal ベンチとの接続層、
登録データの移行、Rails 6.1 の扱い。

### 注意すべき所見

- **envcheck がインスタンス登録の第 2 経路になっている**。`POST /api/env_checks` に
  `name=contest<N>` が来ると副作用で `ContestantInstance` を作り、`cloud_id` はダミー、
  `private_ipv4_address` は捏造値を入れる。EC2 API を叩く同期ジョブとは独立
- **セッション Cookie は Rails と Go で互換しない**。Rails 6.1 + `redis-actionpack` は
  セッション本体を Ruby の Marshal 形式で Redis に置く。
  **段階移行はできず、クリーンカットオーバーになる**
- **非 protobuf のエンドポイントが 3 本ある**（JSON / text/plain / 空ボディ）。
  いずれも Go の envcheck が叩く口で、形式を変えられない
- **登録データの移行で参加方式が選手に見える形で変わる**。
  `isucon2026-portal` はチームパスワード共有、isuxportal は招待 URL。
  登録期間（〜9-30）中の切り替えは避けるべき
