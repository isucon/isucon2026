# ベンチの単発実行（Fargate）

ベンチの開発中に、手元の変更を AWS 上の環境へ流して結果を確かめるための仕組みです。
1 つのコマンドで、ビルド、ECR への push、Fargate での実行、結果の取得と要約の表示まで進みます。
ポータルとジョブキューは通しません。

```sh
make aws-bench-run TEAM=5
make aws-bench-run TEAM=5 BENCH_ARGS='-duration 25s -cpuprofile /results/cpu.pprof'
make aws-bench-run TEAM=8,9,10           # 複数チームへ同時に流す
make aws-bench-show                      # 最新の結果をもう一度表示
make aws-bench-show RUN=20261026T040000Z-team5
```

`TEAM` は `provisioning/aws` の dev stack のチーム番号です。接続先は `teamOutputs` の Router の公開 IP と
`app.teamN-aws.127.0.0.1.nip.io` から決め、`-scale large` を付けます。ベンチは initialize で
対象のデータを消すので、ほかの人が使っていないチームを指定してください。
このコマンドから同じチームへ実行中のタスクがあれば、起動せずに止まります。

`TEAM` にチーム番号をカンマ区切りで並べると、各チームへ同時に流します。ビルド、push、Pulumi の出力の読み出し、
タスク定義の登録は 1 回だけ行い、チームごとのタスクとサーバーの記録を並行して動かします。途中の出力には `[teamN]` を付け、
最後に全チームのスコアと、使用率の要約の `!` の行をまとめて表示します。チームごとの詳しい要約は
`make aws-bench-show RUN=<RUN_ID>` で見ます。どれかのチームが失敗すると、終了コードは 1 になります。
ポータルの Supervisor から同じチームへ流れるジョブは検出できません。

## 流れ

1. `supervisor/Dockerfile` を linux/amd64 でビルドする。本番の Supervisor と同じ bench のバイナリになる。
   タグはイメージの内容から決めるので、bench、dataset、portal、supervisor に変更がなければ push を省く。
2. 対象チームの Router、app1〜3、db1 で、SSM から使用率の記録を始める（下の「使用率」を参照）。
3. `ecspresso run` がタスク定義を登録して 1 タスクを起動し、bench の進行ログを表示しながら終了を待つ。
   タスクは Supervisor と同じ 4 vCPU / 8 GiB で、同じ private subnet と共有 NAT（`52.68.140.39`）から接続する。
4. bench の終了後、同じタスクの uploader コンテナが `/results` を S3 の `runs/<RUN_ID>/` に置く。
   サーバーの記録も止めて回収する。
5. 手元の `var/aws-bench/<RUN_ID>/` に取得して、結果と使用率の要約を表示する。
   終了コードは bench と同じ（pass=0、fail=1、設定の誤りなど=2）。

`var/aws-bench/<RUN_ID>/` には次のファイルが入ります。各回の要約は `var/aws-bench/runs.tsv` に 1 行ずつ追記します。

| ファイル | 内容 |
|---|---|
| `result.json` | bench の標準出力（結果 JSON） |
| `bench.log` | bench の標準エラー出力（進行ログ） |
| `exit-code` | bench の終了コード |
| `meta.json` | チーム、接続先、イメージ、コミット（未コミットの変更があれば `+dirty`）、bench の引数 |
| `stats.tsv` | ベンチのタスクの CPU、メモリ、ネットワーク（下の「使用率」を参照） |
| `hosts/<役割>/` | Router、app1〜3、db1 の `stats.tsv` と EBS の上限（`ebs.json`）。Router には `tc -s qdisc` と HAProxy の統計（`show stat`）の前後の値も入る |
| `report.txt`、`timeline.csv` | 使用率の要約と、1 秒ごとの値（CPU、メモリ、帯域、ディスク、PSI） |

`BENCH_ARGS` で `/results` の下に書いたファイル（`-cpuprofile`、`-memprofile`）も一緒に取得します。
`IMAGE=<repository>@sha256:...` を指定するとビルドと push を省き、そのイメージで実行します。
`make aws-bench-image` はビルドと push だけ行い、イメージを表示します。

投稿に使う写真のアーカイブ（ISU の写真、`manifest.json` 付きの zip）は、結果のバケットの `assets/merged.zip` に置きます。
実行のたびに `run.sh` が署名付き URL（1 時間）を作り、bench の `-images` に渡します。一時的な認証情報で署名した URL は
認証情報の期限で失効するので、URL を作り置きしません。`meta.json` には URL ではなく S3 の場所を残します。
`PHOTOS=<キー>` で別のアーカイブを、`PHOTOS=none` で生成画像を使います。`BENCH_ARGS` に `-images` があればそちらを使います。

初期データの画像のダイジェストの表（`make bench-image-digests` で作る）は `assets/image-digests-large.bin` に置きます。
`run.sh` は同じく署名付き URL を作り、bench の環境変数 `ISUXBENCH_IMAGE_DIGESTS` で渡します（`IMAGE` で指定した
古いベンチは読まずにそのまま走ります）。`DIGESTS=<キー>` で別の表を、`DIGESTS=none` で表なし（画像を描き直す）にします。

## 使用率

走行のたびに、ベンチと対象チームのサーバーの使用率を記録し、結果のあとに要約を表示します。
ベンチが原因でスコアが伸びないのか、サーバーが原因なのかを見分けるためのものです。

- `sampler.sh` が 1 秒ごとに `/proc` の CPU、メモリ、TCP、ネットワーク、ディスク（`/proc/diskstats`）、PSI（`/proc/pressure`）の
  累積値を、5 秒ごとにプロセスごとの CPU 時間を記録する。
  ベンチのタスクではコンテナの中で、Router、app1〜3、db1 では SSM（`AWS-RunShellScript`、root）で動かす。
- サーバーの記録はベンチの起動の直前に始め、終了後に SSM で止めて回収する。SSM の出力はバケットの `ssm/<RUN_ID>/` に置かれる。
  サーバーのインスタンスロールはメインの stack が管理するので、この stack のバケットポリシーで書き込みを許可している（`instanceRoleArns`）。
- 回収のあと、サーバーの EBS ボリュームとインスタンスタイプの上限（IOPS、スループット）を EC2 API から `hosts/<役割>/ebs.json` に保存する。
- `report.py` が、`bench.log` の `phase: load` から `phase: final check` までの区間で集計する。
  `make aws-bench-show` は保存済みの記録から要約を作り直す。

要約には次の内容が出ます。次のどれかに当たれば、最後に `!` で示します。

- CPU の平均が 85% 以上か p95 が 95% 以上
- ディスクの util の平均が 85% 以上、1 秒ごとの IOPS かスループットがボリュームの上限の 90% 以上、平均がインスタンスの EBS の基準値以上（バーストを使っている）
- PSI で、全タスクが I/O を待った時間（io full）か、メモリを待った時間（memory some）が 10% 以上
- qdisc で破棄したパケットがある

| 項目 | 内容 |
|---|---|
| ホストごとの表 | CPU（平均、p95、最大、user/sys/softirq/steal/iowait の内訳）、メモリ、送受信の帯域、TCP の接続数 |
| 5 秒ごとの CPU | 投稿期間が開いた直後など、平均では見えない詰まり |
| プロセスごとの使用コア数 | `isureal-bench`、`haproxy`、`isureal`、`nginx`、`postgres` など。終了したプロセスの時間は親に入る |
| ディスク | デバイスごとの読み書きの IOPS と MB/s、util、await、キュー長、書き戻し待ちのメモリ（Dirty + Writeback）と、EBS の上限 |
| 5 秒ごとのディスク | 動いているデバイスの util と await |
| PSI | CPU、I/O、メモリで待たされた時間の割合。some は少なくとも 1 つ、full はすべてのタスクが待った時間 |
| インターフェースごとの帯域 | Router は公開側の `ens5` と、地域間の遅延と帯域を再現する `data0` |
| Router | qdisc ごとの送信量、破棄、overlimits の増分と、HAProxy のエラー系カウンター（`ereq`、`econ`、`eresp`、中断、5xx など）の増分 |

ベンチのタスクの CPU は、タスクの microVM 全体（4 vCPU）に対する割合です。メモリは microVM 全体の値で、タスクの上限（8 GiB）とは別です。
NVMe は並列に処理するので、util が 100% でも飽和しているとは限りません。ディスクの詰まりは await、キュー長、PSI の io と、EBS の上限との比で判断してください。
初期実装の webapp では db1 が先に飽和するので、ベンチが詰まっているかは最適化済みの webapp を動かしているチームで確かめてください。
`HOST_STATS=0` を指定すると、サーバーの記録を省きます。

## 構成

| 管理 | 対象 |
|---|---|
| Pulumi（`provisioning/aws/bench-runner`、stack `dev`） | ECR、結果の S3 バケットとそのポリシー、CloudWatch Logs、IAM ロール、Security Group、ECS クラスター |
| ecspresso（`ecspresso/`） | タスク定義と実行。ECS サービスは作らない。`service.jsonnet` はネットワーク設定を渡すためだけに使う |

ecspresso の Jsonnet は、Pulumi の出力 `infra` を環境変数 `BENCH_RUNNER_INFRA` から読みます。
ecspresso は `mise` があれば 2.8.7 を `mise exec` で使い、なければ `PATH` のものを使います。
タスクの役割は S3 の `runs/` への書き込みだけです。ECR のイメージは新しい 30 個、S3 の結果は 90 日、
ログは 30 日で消えます。

Router への TCP/8080 は、Supervisor stack が共有 NAT の `/32` に許可しているルールを使います。
このスタックでは同じルールを作りません。Supervisor の構成を変えて NAT が変わった場合は、
`vpcId` と `subnetId` を合わせて変更してください。

## 初期設定

```sh
make aws-infra-install
make aws-bench-runner-preview
make aws-bench-runner-up
```

写真のアーカイブは stack の外のデータなので、初回に手で置きます。

```sh
BUCKET=$(cd provisioning/aws/bench-runner && pulumi stack output infra --json --stack dev | jq -r .bucket)
aws s3 cp merged.zip "s3://$BUCKET/assets/merged.zip"
make bench-image-digests   # 初期データが変わったら作り直す
aws s3 cp var/image-digests/large.bin "s3://$BUCKET/assets/image-digests-large.bin"
```

stack の state は `s3://isucon2026-pulumi-state`、secret の暗号化は `awskms://alias/isucon2026-pulumi-secrets` です。
削除は `cd provisioning/aws/bench-runner && pulumi destroy --stack dev` で行います。結果のバケットは中身ごと消えます。
