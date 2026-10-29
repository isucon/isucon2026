# 開発ポータルの共有 Supervisor プール

開発ポータル（`https://portal-dev.isucon.net`）のベンチを実行する Supervisor の共有プールです。
ECS service `isucon2026-shared-supervisor`（cluster `supervisor-cluster-1090218`）に 3 タスクを置き、
チームも AZ も絞らずにポータルのキューをポーリングします。各タスクは 4 vCPU / 8 GiB です。
`render.py` は Linux ARM64 のタスク定義を生成します。
cluster、実行 role、ネットワーク、ECR、log group は [試し解き用 Supervisor の stack](../README.md) が持ち、
このディレクトリはサービスとタスク定義だけを扱います。

2026-10-28 に `provisioning/aws/shared-supervisor/` から `provisioning/aws/supervisor/pool/` に移しました。
Pulumi project、stack、backend と配備設定は維持しています。
このディレクトリの `mise.toml` は親の基盤用 backend を上書きします。
旧ディレクトリを指定する手元のスクリプトは新しいパスに変更してください。

| 管理 | 対象 |
|---|---|
| `pool.json` | 定義の元。接続先、イメージ、タスク数、ベンチの上限時間、Supervisor の上限時間、写真のアーカイブの場所 |
| Pulumi（project `isucon2026-shared-supervisor`、stack `portal-dev`） | `render.py` が作った配備の定義（タスク定義、サービス定義、ecspresso の設定）を stack の設定 `deployments` に保持して出力する。リソースは作らない |
| ecspresso（`generated/`、Git 管理外） | stack の出力から書いた JSON でサービスとタスク定義を配備する |

Pulumi の backend は `s3://isucon2026-pulumi-state-124502390568/shared-supervisor`、stack は `portal-dev`、
passphrase は空です（`mise.toml` が設定します）。state を別に作らないでください。
依存関係は`provisioning/aws` の `node_modules` を使います（`npm --prefix provisioning/aws ci`）。

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
`supervisor-service-1〜6` は [試し解き用 Supervisor の stack](../README.md) が Pulumi で
`desiredCount: 0` のまま管理しているので、二重管理を避けるためにここには入れていません。
`isucon2026-practice-supervisor-30001` は移行時点では private の `ops/practice-supervisor` が管理していましたが、2026-10-28 にサービスと専用 Pulumi stack を廃止しました。
private の過去の設定からこの stack を作り直したり、旧サービスを再配備したりしないでください。

## CloudFormation instance registration

New ISUCON2026 contest stacks register their Router with the portal. Jobs from
these environments contain the Router IP and a portal-generated `--host` as the
first extra argument. The Supervisor uses that target directly, so recreation
does not require updating `pool.json`. Existing Pulumi environments also register
Router endpoints and their physical team hostnames through the
[instance sync](../../../../portal/deploy/ecspresso/INSTANCE_SYNC.md).
The static `routes` table and fallback have been removed. Jobs without a Host
are rejected with `Router registration required`; register the environment
before running benchmarks. See the [contest template and registration
workflow](../../../../portal/internal/cloudformation/README.md).

## 更新の手順

リポジトリルートで実行します。AWS の認証は `provisioning/aws-sdk.config` を通します
（`AWS_CONFIG_FILE=provisioning/aws-sdk.config AWS_PROFILE=default AWS_SDK_LOAD_CONFIG=1`）。

1. 新しいベンチのイメージを作って push する（[Supervisor 構築手順](../README.md) の `make supervisor-image SUPERVISOR_PLATFORM=linux/arm64`）。
   写真のアーカイブを同梱したイメージなら、`benchImages` に同梱した場所（`/opt/isucon/photos/merged.zip`）を指定します。
2. `pool.json` のイメージや共通設定を更新する。`image` は digest で固定します。
   ARM64 への初回移行では、既存の amd64 用 digest を ARM64 用の新しい digest に置き換えてから次の手順を実行します。
   IP の変更やチーム追加はポータルのインスタンス登録・同期で反映し、固定IPの転送表は管理しません。
3. 配備の定義を作って stack の設定に書き、適用する。雛形には稼働中のタスク定義（family の最新の ACTIVE）と
   サービスを AWS から取り、CPU、メモリ、実行 role、ログ、secrets、ulimit、ネットワーク設定を写します。

   ```bash
   cd provisioning/aws/supervisor/pool
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

## 適用記録

### 2026-10-29 初期データの最終日を 10-27 にしたベンチへの更新

- #325 で `Large()` の最終日を 2026-10-27 にしたので、ベンチを入れ替えました。
  問題サーバーの AMI `ami-07743933837cd476f` と組で使います。古い AMI のチームへのジョブは、初期データの検証で落ちます。
- イメージを `sha256:3dfe2f60…`（上の ARM64 への移行、ソース `51f63c0b`）から
  `sha256:3de628855b0ba84a1b2e013c528fd67f94ccfaad3c9947ea5e8686e715b5fd98`（タグ `arm64-bench-e5efafe2ac0f`）にしました。
- `51f63c0b` から変わったのは `dataset/world.go`（最終日）だけで、supervisor、bench、portal は同じです。
  手元が x86_64 で ARM64 の Rust をビルドできないので、`3dfe2f60…` を土台に `/usr/local/bin/isureal-bench` だけを差し替えました。
  bench は Dockerfile と同じ `golang:1.26.5-alpine@sha256:0178a641…` で、
  `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -buildvcs=false -trimpath` でクロスコンパイルしました。
  同じ方法で `51f63c0b` から作った bench は、`3dfe2f60…` の bench と SHA-256 が一致しました。
  写真のアーカイブも `3dfe2f60…` のものと SHA-256 が一致することを確かめました。
- `pulumi up` は出力 `deployments` のイメージの変更だけです（リソース 1 件、変更なし）。
  `ecspresso diff` はタスク定義のイメージだけで、走行中のジョブがないことを確かめてから revision 11 を配備しました。
- 3 タスクが `ecs.cpu-architecture=arm64` と新しい digest で動き、ポータルをポーリングしていることを確認しました。
  配備後の Pulumi preview と ecspresso diff に差分はありません。

### 2026-10-29 ARM64 への移行

- 写真入り ARM64 イメージ `sha256:3dfe2f60499d8fdf662b72a1aab2f971f2b3761ae4cc6762289ef4f64717da2b` を ECR に push しました。
  ソースは `51f63c0b`、写真は `assets/merged.zip` を使い、イメージ内の写真の SHA-256 が元のアーカイブと一致することを確認しました。
- `pool.json` と Pulumi の `deployments` を更新して `pulumi up` を実行し、ecspresso で revision 10 を配備しました。
  差分はイメージと `runtimePlatform.cpuArchitecture`（`X86_64` → `ARM64`）だけです。
  4 vCPU / 8 GiB、desiredCount 3、ネットワークと Secrets Manager の参照は維持しました。
- 3 タスクの `ecs.cpu-architecture=arm64` とイメージ digest を確認しました。
  配備後の Pulumi preview と ecspresso diff に差分はありません。
- `trial-team8` の target 1 で [ベンチジョブ #446](https://portal-dev.isucon.net/admin/benchmark_jobs/446) を実行しました。
  2026-10-29 15:51:39〜15:53:10 JST に `pending → running → finished` と進み、`passed=true`、score 6540 で完了しました。
  これは動作確認の結果であり、アーキテクチャ間の性能比較ではありません。

### 2026-10-28 Practice Supervisor の廃止

- `isucon2026-practice-supervisor-30001` の desired / running / pending が全て 0 であることを確認し、`ecspresso delete --force` で削除しました。
- 専用タスク定義 `isucon2026-practice-supervisor-30001:1`、`:2`、`:3` を登録解除しました。
  ecspresso の deregister は削除済みサービスを INACTIVE として拒否したため、登録解除には AWS ECS API を使用しました。
  定義は INACTIVE の履歴として残ります。
- backend `s3://isucon2026-pulumi-state-124502390568/practice-supervisor` の project `isucon2026-practice-supervisor`、stack `portal-dev` は、配備設定を出力する root stack だけを持ち、AWS リソースを管理していないことを export と destroy preview で確認しました。
  `pulumi destroy` と `pulumi stack rm` を実行し、stack 一覧が空になったことを確認しました。
- 削除後、サービスが INACTIVE、タスク数が全て 0、専用 family の ACTIVE タスク定義が 0 件であることを検証しました。
  現行 shared-supervisor の Pulumi state に practice サービスへの参照がないことも確認しました。
- 共有プールは revision 9 で desired / running が 3、pending が 0 でした。
  今回、共有プール、旧チーム別サービス 1〜6、共有基盤、Secrets Manager、ログには変更を加えていません。

### 2026-10-27 写真のアーカイブを同梱したベンチへの更新

- イメージを `sha256:aec29420…`（pr46-14cfada5-20261020）から `sha256:a70d4cf10b3d4623a9446016797260108f108e0b4e99c2b388d0303f8f16530e`
  （タグ `photos-a70d4cf10b3d4623`、ベンチ `a0cd2da` と `assets/merged.zip` を同梱）にし、
  環境変数 `ISUXBENCH_IMAGES=/opt/isucon/photos/merged.zip` を加えた。`--timeout 300s` と `HARD_TIMEOUT` 310 は据え置き。
- `pulumi up` は stack の出力 `deployments` の変更だけ（リソース 1 件変更なし）。`ecspresso diff` はタスク定義の
  イメージと環境変数の追加だけで、サービス定義に差分なし。
- 走行中のジョブがないことを 3 ストリームで確かめてから `ecspresso deploy` した。

## Score-based worker sizing

Score routing is opt-in. The portal reads
`ISUXPORTAL_BENCH_LARGE_SCORE_THRESHOLD` (nonnegative integer; unset or `0`
disables routing). A team qualifies when a finished, successful result from a
finished, non-post-validation job has a score **strictly greater** than the
threshold. Later low or failed results do not demote the team while that history
and threshold remain unchanged. The next pending job uses the large pool; the
qualifying run is not repeated automatically. Post-validation jobs use the same
team classification but their results do not qualify a team.

Workers send `X-Isux-Benchmark-Class: standard|large` on `/api/bench/receive`,
configured by `ISUXPORTAL_SUPERVISOR_BENCHMARK_CLASS`. Missing headers mean
`standard` for compatibility; unknown classes are rejected. With routing
disabled, large workers receive no jobs. There is no fallback to standard
workers if the large pool is unavailable; qualified jobs remain pending.
The benchmark arguments, initial dataset (`--scale large`), and scoring stay
the same for both pools. This setting changes the worker's CPU and memory only.

To prepare two pools, add this optional object to `pool.json`:

```json
"largePool": {
  "desiredCount": 1,
  "cpu": "8192",
  "memory": "16384"
}
```

`render.py config pool.json` uses the existing standard service as the template
and stores **both** deployments in the Pulumi stack configuration. The large
service and task family have a `-large` suffix. Both pools share the pinned
image, secrets references, network, and benchmark arguments. Supported CPU/MiB
pairs are `4096/8192`, `8192/16384`, and `16384/32768`; each pair must be specified
together. `largePool` is intentionally absent from the checked-in default.

Deployment order:

1. Build an image containing the worker-class support, update `pool.json`,
   generate the deployments, and apply the pool Pulumi stack as described above.
2. Deploy both generated services through ecspresso (create the large service
   on its first deployment). Check that each starts with the expected class.
3. Deploy the updated portal while its threshold is disabled. Keep the threshold
   in the portal Pulumi stack's `environment` map, never only in ECS overrides.
4. Set `ISUXPORTAL_BENCH_LARGE_SCORE_THRESHOLD` in that map to the chosen value,
   apply the portal Pulumi stack, and redeploy the portal through ecspresso.
   Verify a qualifying team runs on the large service and another team runs on
   the standard service.

For rollback, disable the portal threshold and redeploy it first; standard
workers will then accept all pending jobs. Drain large workers before removing
their service. Changing or deleting historical results can change classification.
Choose the threshold after comparing scores and worker saturation on both sizes;
no production threshold or extra service is enabled by this implementation.
