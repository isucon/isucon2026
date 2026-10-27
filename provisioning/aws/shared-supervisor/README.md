# 開発ポータルの共有 Supervisor プール

開発ポータル（`https://portal-dev.isucon.net`）のベンチを実行する Supervisor の共有プールです。
ECS service `isucon2026-shared-supervisor`（cluster `supervisor-cluster-1090218`）に 3 タスクを置き、
チームも AZ も絞らずにポータルのキューをポーリングします。各タスクは 4 vCPU / 8 GiB です。
cluster、実行 role、ネットワーク、ECR、log group は [試し解き用 Supervisor の stack](../supervisor/README.md) が持ち、
このディレクトリはサービスとタスク定義だけを扱います。

| 管理 | 対象 |
|---|---|
| `pool.json` | 定義の元。接続先、イメージ、タスク数、ベンチの上限時間、Supervisor の上限時間、写真のアーカイブの場所、登録済みの app の公開 IP から Router への転送表 |
| Pulumi（project `isucon2026-shared-supervisor`、stack `portal-dev`） | `render.py` が作った配備の定義（タスク定義、サービス定義、ecspresso の設定）を stack の設定 `deployments` に保持して出力する。リソースは作らない |
| ecspresso（`generated/`、Git 管理外） | stack の出力から書いた JSON でサービスとタスク定義を配備する |

Pulumi の backend は `s3://isucon2026-pulumi-state-124502390568/shared-supervisor`、stack は `portal-dev`、
passphrase は空です（`mise.toml` が設定します）。state を別に作らないでください。
依存関係は親の `provisioning/aws` の `node_modules` を使います（`npm --prefix provisioning/aws ci`）。

## 出どころ

`isucon/isucon2026-private` のブランチ `codex/portal-discord-environment` の `ops/shared-supervisor`
（コミット `4a9267b` "feat(ops): share development supervisor pool"、2026-10-16）から移しました。
移したのは Pulumi の project と stack の設定、`render.py` と単体テスト、`pool.json`、README の内容です。
Discord などプールと無関係なものと、作者の環境のパスを含む `package-lock.json` は持ってきていません。
秘密の値は含まれておらず、ポータルのトークンは Secrets Manager の
`portal-develop/application` の `ISUXPORTAL_BENCH_TOKEN` を ECS の secrets で参照するだけです。

private リポジトリの設定は 2026-10-17 の配備（イメージ `sha256:2f35c466…`）のままで、
2026-10-20 に Pulumi の state と live（task definition revision 5: イメージ `sha256:aec29420…`、
`--timeout 300s`、`ISUXPORTAL_SUPERVISOR_HARD_TIMEOUT=310`、team1〜7 の転送）だけが更新されていました。
移すにあたって `pool.json` と `render.py` を live に合わせ（`benchTimeout`、`hardTimeout`、team7 の転送）、
`render.py` が live の定義をそのまま再現することを確かめたうえで、イメージと写真のアーカイブを更新しました。

private の `deployments` には退役したチーム別 service 7 件（`desiredCount: 0`）も入っていましたが、
`supervisor-service-1〜6` は [試し解き用 Supervisor の stack](../supervisor/README.md) が Pulumi で
`desiredCount: 0` のまま管理しているので、二重管理を避けるためにここには入れていません。
`isucon2026-practice-supervisor-30001` も private の `ops/practice-supervisor` の管理のままで、ここでは扱いません。

## 更新の手順

リポジトリルートで実行します。AWS の認証は `provisioning/aws-sdk.config` を通します
（`AWS_CONFIG_FILE=provisioning/aws-sdk.config AWS_PROFILE=default AWS_SDK_LOAD_CONFIG=1`）。

1. 新しいベンチのイメージを作って push する（[Supervisor 構築手順](../supervisor/README.md) の `make supervisor-image`）。
   写真のアーカイブを同梱したイメージなら、`benchImages` に同梱した場所（`/opt/isucon/photos/merged.zip`）を指定します。
2. `pool.json` を更新する。`image` は digest で固定します。app の公開 IP や Router の公開 IP が変わったときや
   チームを足すときは `routes` を更新します（登録されていない IP のジョブは起動前に `exit 2` で拒否されます）。
3. 配備の定義を作って stack の設定に書き、適用する。雛形には稼働中のタスク定義（family の最新の ACTIVE）と
   サービスを AWS から取り、CPU、メモリ、実行 role、ログ、secrets、ulimit、ネットワーク設定を写します。

   ```bash
   cd provisioning/aws/shared-supervisor
   python3 -m unittest discover
   python3 render.py config pool.json          # AWS から雛形を取り、pulumi config set deployments する
   pulumi preview --stack portal-dev --diff    # 出力 deployments の差分だけが出る
   pulumi up --stack portal-dev
   pulumi stack output --stack portal-dev --json | python3 render.py generated
   cd generated/isucon2026-shared-supervisor
   ecspresso --config ecspresso.json diff
   ```

4. 走行中のジョブがないことを確かめて配備する。CloudWatch Logs `supervisor-logs-2e941a9` の 3 ストリームが
   `Received job: None` を繰り返していて、`phase:` の行が出ていなければ空きです。
   サービスは旧タスクを止めてから新タスクを起動する（minimumHealthyPercent 0）ので、短い間ポーリングが止まります。

   ```bash
   ecspresso --config ecspresso.json deploy
   aws ecs describe-services --cluster supervisor-cluster-1090218 --services isucon2026-shared-supervisor \
     --query 'services[0].{td:taskDefinition,running:runningCount,rollout:deployments[0].rolloutState}'
   aws logs tail supervisor-logs-2e941a9 --since 5m
   ```

5. ポータルの運営画面からどれかのチームにベンチを 1 回流し、`pending → running → finished` と結果を確かめる。

ecspresso は `mise exec "aqua:kayac/ecspresso@2.8.7" -- ecspresso` で使えます（bench-runner と同じ版）。
`render.py build pool.json task.json service.json` は雛形をファイルから読んで定義を標準出力に書くので、
AWS に触らずに結果を確かめられます。
