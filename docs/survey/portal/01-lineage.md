# ポータル実装の二系統と isucon14-portal との差分

調査日: 2026-08-21。

## 出典

| リポジトリ | commit | 日付 | 備考 |
|---|---|---|---|
| `isucon/portal` | `9b9dbaa` | 2022-08-26 | README は一行、`# isucon11-portal` |
| `isucon/isucon14-portal` | `4c2b99a` | 2024-12-13 | 同系統の最新。ISUCON14 で実運用 |
| `isucon/isucon2026-portal` | `49105f2` | — | private。現在稼働中 |

## 1. 二系統

isucon org のポータルには血統が二つある。混同してはならない。

| 系統 | 実体 | 言語 |
|---|---|---|
| **isuxportal 系** | isucon10 → 11 → 12 → **14**-portal、および汎用ベースの `isucon/portal` | Rails + React/TS + Rust + Go |
| **Django 系** | isucon9 → 13 → **2026**-portal | Django + Tailwind/daisyUI |

`isucon/portal` は ISUCON11 ポータルを汎用ベースとして切り出したスナップショットで、
2022-08 で更新が止まっている。**isuxportal を採るなら起点は `isucon14-portal`。**

## 2. 三者の対比

| 観点 | isucon/portal | isucon14-portal | isucon2026-portal |
|---|---|---|---|
| サーバ | Rails 6.1.6 / Ruby 3.1 | Rails 6.1.6 / **Ruby 3.3.4** | Django 5.2 / Python 3.12（uv） |
| フロント | React 17 + TS（8,598 行） | React 17 + TS（**9,957 行**） | Django テンプレート 52 枚 + Tailwind v4 / daisyUI |
| アセットビルド | webpack + yarn + **Node 16** | webpack 5.93 + **pnpm 9.15 + Node 20** | Vite + npm |
| protobuf 生成 | 自前 `protoc.sh` | **buf**（Go / TS-es / Ruby / Rust を一括） | なし |
| API 形式 | HTTP REST + **protobuf ボディ** | 同じ | サーバレンダ。API は最小限 |
| ベンチ連携 | **pull 型**。supervisor(Rust) が HTTP+protobuf でポーリング | 同じ。supervisor は**コンテナ化 + ARM 対応** | **push 型**。AWS SQS FIFO（AZ 別キュー） |
| ジョブキュー実体 | MySQL `benchmark_jobs` + 行ロック | 同じ | SQS + `Job` テーブル |
| DB | MySQL（RDS） | MySQL（RDS / Aurora Serverless） | TiDB（さくら）/ ローカルは SQLite |
| 非同期基盤 | ActiveJob + shoryuken + SQS（18 ジョブ） | 同じ（**20 ジョブ**） | management command 常駐 + Redis |
| デプロイ | hako + jsonnet / Terraform **58 ファイル（フラット）** | **ecspresso** + jsonnet + sops(KMS) / Terraform **module 化 24 ファイル 1,634 行** | docker swarm + ansible / さくら |
| 稼働単位 | app / grpc / worker / discordbot | app / worker / bot / notifyloop / **benchmarker**（全 ARM64 Fargate） | api コンテナ（nginx + gunicorn） |
| テスト | RSpec 818 行 | RSpec **818 行（変化なし）** | Django テスト 2 ファイル |
| 現在の状態 | 完成品だが AWS 前提が全体を貫く | 完成品。依存も新しい | **参加登録のみ稼働。当日機能は空洞** |

## 3. isucon2026-portal が「空洞」である根拠

- `settings.py` に `REGISTRATION_ONLY = True`
- `urls.py` の `path('internal/', ...)` が**コメントアウト**されている。
  `docs/INTERNAL.md` に書かれたベンチマーカー連携 API
  （`/internal/job/dequeue/`、`/internal/job/:id/report/`）は到達不能
- `Team.score` が `# FIXME: this is a dummy` で固定値を返す
- `SQS_JOB_URLS` のアカウント ID が `xxxxxxxxxxxx` のまま
- 直近コミットに「環境チェックを非表示に」

ISUCON13 由来の当日コードは残骸として存在するが、配線されていない。

一方で**本番稼働している**。`deploy-prod.yml` が
`app-001.portal.isucon.net` / `app-002.portal.isucon.net` の 2 台に
ローリングアップデートしており、登録開始は 2026-07-28、
段階募集の第 3 期（935 枠）は 8-05 に開いている。

## 4. isucon/portal → isucon14-portal の差分

2 年間の変化量は小さい。

| 指標 | isucon/portal | isucon14-portal | 差 |
|---|---:|---:|---|
| Ruby（app 配下） | 3,148 行 | 3,470 行 | +322 |
| TypeScript | 8,598 行 | 9,957 行 | +1,359 |
| proto | 44 ファイル 1,038 行 | 46 ファイル 1,033 行 | +2 ファイル |
| DB テーブル | 14 | 16 | +2 |
| ジョブ | 18 | 20 | +2 |
| RSpec | 818 行 | 818 行 | **0** |

### `contest.rb` は一行も変わっていない

移植の最難関である `app/models/contest.rb` は **274 行が完全に同一**（diff が空）。
可視性 3 系統・凍結・合成 ETag・Redis 事前生成の設計は
2022 年から 2024 年まで無変更で ISUCON12 と 14 を通した。

**仕様が固まっている証拠であり、Go 移植の仕様凍結点として使える。**
同時に、RSpec が増えていないことから、この安定は
**テストではなく実戦で担保されている**と読める。

### スキーマの差分

```
benchmark_jobs  + enqueued_by_id       (代理 enqueue の実行者)
                + post_validation      (最終検証フラグ)
contestants     + in_person            (現地参加)
teams             availability_zone に既定値 apne1-az2
+ instance_command_execute_requests
+ instance_command_execute_request_results
```

### 追加された機能

| 追加機能 | 実体 |
|---|---|
| インスタンスへのコマンド一括実行 | ポータルから `net-ssh` で `isuadmin` として全チームのサーバに接続し実行 |
| 実行結果の収集 | exit code と出力を蓄積。運営画面で個別に閲覧 |
| 最終検証: envcheck 一括再実行 | `TriggerEnvCheck` → `sudo systemctl restart envcheck --no-block` |
| 最終検証: インスタンス一括再起動 | `TriggerInstanceRestart` → `sudo reboot` |
| 最終検証: ベンチ一括実行 | `TriggerBenchmarks`。直近ジョブの target を引き継いで再 enqueue |
| 未整備チームの統計 API | `GetSSHKeyStats` / `GetDiscordStats` / `GetEnvCheckStats`。`isucon/portal` では暫定 HTML だったものが正式 API + 管理画面に昇格 |
| アイコンのアップロード | S3 presigned PUT（600 秒）+ `react-avatar-editor` |
| 現地参加フラグ | `contestants.in_person` |
| enqueue 実行者の記録 | `benchmark_jobs.enqueued_by_id` + `EnqueuedBy.tsx` |
| 認証失敗画面 | `/auth/failure` + `AuthError.tsx` |
| 運営向け踏み台の OIDC 保護 | ALB + Cognito user pool（招待制）。ポータル本体の admin 認証は従来どおり |

### 変わらなかったもの

- Rails は **6.1.6 に据え置き**（`Gemfile.lock` で確認、セキュリティサポート終了済み）
- ベースイメージは `sorah/ruby:3.3-jammy` で個人メンテ依存が継続
- SQS + shoryuken も不変
- `discordrb` の git 参照も不変
- React も 17 のまま、`protobufjs` と `protobuf-es` が併存しており移行途中
- **AWS 依存の総量は減っていない**。Terraform が整理されただけで対象サービスは同じ

## 5. `sorah/cnw` について

`isucon/portal` の `.gitmodules` は `git@github.com:sorah/cnw` を SSH 形式で参照する。
このリポジトリは **public**（"Common codificated files for conference Wi-Fi networks"、最終更新 2020-07）。
HTTPS で clone できる。

依存しているのは `itamae/` の 3 つの symlink だけ。

```
itamae/cnw     -> ../cnw/itamae
itamae/site    -> ../cnw/itamae/site
itamae/site.rb -> ../cnw/itamae/site.rb
```

ポータルアプリケーション本体は cnw に一切依存しない。
`isucon14-portal` には `itamae/` 自体が存在しない。
