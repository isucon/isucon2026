# Supervisor の構成と配備

開発ポータルの Supervisor の基盤と共有プールをこのディレクトリで管理します。
ベンチのイメージ更新とサービスの配備には、[共有プールの手順](pool/README.md)を使ってください。

| 場所 | 管理対象 | Pulumi project | backend の末尾 |
| --- | --- | --- | --- |
| このディレクトリ | cluster、実行 role、ネットワーク、ECR、ログ、Router の接続設定、停止中の旧チーム別サービス | `isucon2026-trial-supervisor` | `/supervisor` |
| [`pool/`](pool/README.md) | 共有プールの専用サブネット、ルートテーブル、配備設定。ECS サービスとタスク定義の配備は ecspresso が担当 | `isucon2026-shared-supervisor` | `/shared-supervisor` |

両 project の既存 stack は `portal-dev`、backend のバケットは `isucon2026-pulumi-state-124502390568` です。
各ディレクトリの `mise.toml` が backend を選びます。
`mise` を使わない場合は、そのファイルの `PULUMI_BACKEND_URL` と `PULUMI_CONFIG_PASSPHRASE` を明示して実行してください。
同名の stack でも project と backend が異なるため、配備先のディレクトリを確認してください。

2026-10-28 に `provisioning/aws/shared-supervisor/` を `pool/` に移しました。
project 名、backend、stack 名、AWS リソース名は変更していません。
state の移行や stack の新規作成は不要です。
旧チーム別サービス 1〜6 は停止中のまま残り、practice の廃止記録は [共有プールの適用記録](pool/README.md#2026-10-28-practice-supervisor-の廃止)にあります。

移動後の確認はリポジトリルートで実行します。

```bash
python3 -m unittest discover -s provisioning/aws/tests -p test_supervisor_layout.py
python3 -m unittest discover -s provisioning/aws/supervisor/pool
npm --prefix provisioning/aws run typecheck
```

以下は基盤とチーム専用 worker の構築手順です。
既存の `portal-dev` の共有プールを更新するときは、上の共有プールの手順を使用してください。

## チーム専用 worker と基盤

独立したPulumi stackで、SupervisorとISURealベンチを含むコンテナをECS
Fargateへ配置します。
`teamId`で1チーム、`teamIds`で複数チームを指定でき、各チームに1タスクを起動します。
複数チームでVPC、NAT Gateway、ECRを共有します。
Supervisorは実ポータルからジョブを取得し、ベンチを子プロセスとして実行して結果を報告します。
実ポータル、対象チーム、問題サーバーは事前に構築してください。
`portal-mock`には結果報告APIがないため接続できません。

## ISUCON14との対応

参照した版は`isucon/isucon14-portal`の`4c2b99a69b03dc0af56642dcf7ed9ef2015935e4`です。
[task definition](https://github.com/isucon/isucon14-portal/blob/4c2b99a69b03dc0af56642dcf7ed9ef2015935e4/ecs/taskdef/benchmarker.jsonnet)、[service definition](https://github.com/isucon/isucon14-portal/blob/4c2b99a69b03dc0af56642dcf7ed9ef2015935e4/ecs/servicedef/benchmarker.jsonnet)、[entrypoint](https://github.com/isucon/isucon14-portal/blob/4c2b99a69b03dc0af56642dcf7ed9ef2015935e4/supervisor/entrypoint.sh)を確認しています。

| 設定                        | ISUCON14                | この定義                                                       |
| --------------------------- | ----------------------- | -------------------------------------------------------------- |
| CPU / memory                | 4 vCPU / 8 GiB          | 同じ                                                           |
| init process / stop timeout | 有効 / 120秒            | 同じ                                                           |
| nofile / nproc              | 100000                  | 同じ                                                           |
| runtime                     | ARM64 Fargate Spot      | 現行Dockerfileに合わせてX86_64 Fargate                         |
| ログ                        | CloudWatch Logs         | 無期限保存、stack削除時にも保持                                                  |
| AZ識別                      | metadataからAZ IDを取得 | ポータルのavailability_zoneと一致する値を明示指定              |
| network                     | public IP、3 AZのsubnet | 指定AZのprivate subnet、NAT Gatewayで固定送信元IP              |
| 認証設定                    | S3 env file             | SSM SecureStringまたは既存のSecrets ManagerをECS secretsで注入 |

固定送信元IPは、既存AWS検証環境のRouterが`clientCidr`を単一の`/32`に限定しているため使用します。
Fargate、NAT Gateway、Elastic IP、CloudWatch Logsに利用料金が発生します。
各stackは専用VPCを作るため、同一CIDRを使うstack間で接続を作らない構成です。

## 初期設定

Node.js、Pulumi CLI、AWS CLIとAWS構築権限が必要です。
依存関係は親の`package-lock.json`を使います。 AWSアカウントにECS service-linked
roleが存在しない場合は、構築者が`aws iam create-service-linked-role --aws-service-name ecs.amazonaws.com`で作成してください。

Supervisorイメージはリポジトリルートをcontextに`supervisor/Dockerfile`でlinux/amd64向けにビルドし、同一アカウントのECRまたは認証不要のregistryへpushします。
`image`にはpushした成果物のdigestを指定します。
ビルドとpushは下の「イメージのビルドとpush」の`make supervisor-image`で行えます。
トークンはポータルの`ISUXPORTAL_BENCH_TOKEN`と一致させます。 同一regionのSSM
SecureStringを使う場合は`tokenParameterArn`、ポータルの既存application JSON
secretを使う場合は`tokenSecretArn`を設定します。
後者は`ISUXPORTAL_BENCH_TOKEN`キーを直接参照するため、トークンのコピーは不要です。
2種類のARNは同時に指定できません。
カスタマー管理KMSキーで暗号化した場合は`tokenKmsKeyArn`も指定し、キー側のpolicyで実行roleの復号を許可してください。

```bash
npm --prefix provisioning/aws ci
cd provisioning/aws/supervisor
pulumi login
pulumi stack init trial-team42
pulumi config set aws:region ap-northeast-1
pulumi config set availabilityZone ap-northeast-1a
pulumi config set instanceName apne1-az4
pulumi config set teamId 42
pulumi config set portalEndpoint https://portal.example.com
pulumi config set image '123456789012.dkr.ecr.ap-northeast-1.amazonaws.com/supervisor@sha256:<64桁のdigest>'
pulumi config set tokenParameterArn arn:aws:ssm:ap-northeast-1:123456789012:parameter/trial/bench-token
pulumi preview --diff
pulumi up
pulumi stack output
```

`instanceName`は例をそのまま使わず、対象チームの`availability_zone`に一致させてください。
AZ名とAZ IDは別の識別子です。 ISUCON14はAZ
IDを使用していましたが、現行Supervisorは渡された文字列をそのままポータルへ送ります。

## イメージのビルドとpush

ベンチは投稿に使う写真のアーカイブ（`-images`、環境変数`ISUXBENCH_IMAGES`）がないと生成した小さな画像を投稿し、本番の条件と変わります。
bench-runnerは走行ごとに署名付きURLを渡しますが、ポータルから走るSupervisorにはその経路がないため、アーカイブをイメージに同梱し、タスク定義の環境変数で場所を渡します。

```bash
# repository root
make supervisor-photos                  # S3 の assets/merged.zip を var/photos/merged.zip に取る（あれば省く）
make supervisor-image                   # linux/amd64 でビルドし、stack の ECR へ push して repository@digest を表示する
make supervisor-image SUPERVISOR_STACK=portal-dev TAG=photos-20261027   # stack とタグを指定する例
```

共有プール向けには `make supervisor-image SUPERVISOR_PLATFORM=linux/arm64` を使います。
チーム別の旧タスクは X86_64 のため、既定の linux/amd64 を使います。

`make supervisor-image`は`supervisor/Dockerfile`を`PHOTOS=archive`でビルドし、`var/photos/merged.zip`を`/opt/isucon/photos/merged.zip`に入れます。
push先はstackの出力`repositoryUrl`（`REPOSITORY`で上書き可）、タグは省略するとイメージの内容から決め、同じタグがECRにあればpushを省きます。
表示されたdigestを`image`（必要なら`teamImages`）に設定し、`benchmarkImages`に同梱したパスを設定します。
`benchmarkImages`は各workerの環境変数`ISUXBENCH_IMAGES`になり、省略すると生成画像のままです。
アーカイブを同梱しないイメージ（`PHOTOS=none`、bench-runnerと同じ）でもこのstackは動きます。

```bash
cd provisioning/aws/supervisor
pulumi config set image "$(cd ../../.. && make -s supervisor-image)"
pulumi config set benchmarkImages /opt/isucon/photos/merged.zip
```

## Router接続と動作確認

`egressPublicAddress`をRouter側stackの`clientCidr`へ設定します。
`routerSecurityGroupIds`を設定すると、既存RouterのSecurity
Groupへ固定送信元IPからTCP/8080を許可するルールを追加します。
この場合は別stackの`clientCidr`変更は不要です。
ポータルに実EC2のIPを登録している場合は、`targetOverrides`で各アプリのpublic
IPをRouterのpublic IPとapp番号に対応するHostへ変換できます。
Supervisorが受け取った対象番号を維持し、登録されていないIPはベンチ起動前に拒否します。
設定例は[portal-devの適用済み設定](Pulumi.portal-dev.yaml.example)を参照してください。
EC2のpublic IPやRouterのHost設定を変更した場合は、この対応表も更新します。
このstackは問題サーバーへの直接通信許可やポータルのチーム登録を作りません。

```bash
# repository root
SUPERVISOR_EGRESS="$(pulumi -C provisioning/aws/supervisor stack output egressPublicAddress --stack trial-team42)"
pulumi -C provisioning/aws config set clientCidr "${SUPERVISOR_EGRESS}/32" --stack dev
make aws-infra-preview STACK=dev
make aws-infra-up STACK=dev

LOG_GROUP="$(pulumi -C provisioning/aws/supervisor stack output logGroupName --stack trial-team42)"
aws logs tail "${LOG_GROUP}" --follow --region ap-northeast-1
```

実ポータルからベンチジョブを投入し、`pending → running → finished`と結果のpass、スコアを確認します。
停止時にはFargateの120秒上限で強制終了される可能性があるため、実行中ジョブのない時点で更新または削除します。
更新時は旧タスクを停止してから新タスクを起動し、同じチームとAZ向けのworkerの重複を抑えます。
実行中ジョブの自動回収は現行Supervisorの対象外です。

標準出力と標準エラーはCloudWatch Logsへ送ります。
ベンチのファイルログはタスク内の一時領域にあり、タスク終了時に消えます。
ポータルへ確定報告できなかったファイルログの永続保存は、この定義に含めていません。
SSMトークンを更新した場合はECSサービスを再デプロイして、新しいタスクへ値を反映してください。

```bash
pulumi -C provisioning/aws/supervisor destroy --stack trial-team42
```

削除はSupervisorのstackだけを対象にします。
SSMパラメータ、ECRイメージ、ポータル、問題サーバーは別管理です。

## 検証

```bash
npm --prefix provisioning/aws test
npm --prefix provisioning/aws run typecheck
```

Pulumi
mocksでFargateリソース、チーム限定設定、秘密情報の参照権限、固定送信元経路を検証します。
AWS上の起動とポータルとの疎通は適用後に確認してください。 ECS
secretsの権限は[AWSの実行role仕様](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task_execution_IAM_role.html)に従います。

## portal-devへの適用記録

2026-10-03にteam1〜3のSupervisorを適用しました。
実行結果と既存アプリ側の未解決事項は[適用記録](deployment-20261003.md)に記録しています。

### 現在の配備状況（2026-10-27時点）

portal-devのベンチは、このstackのチーム別workerではなく、共有プール`isucon2026-shared-supervisor`（3タスク、チームもAZも絞らずポーリング、接続先`https://portal-dev.isucon.net`）が実行しています。
共有プールはこのstackのcluster、実行role、ネットワーク、ECR、log groupを使いますが、サービスとタスク定義は[共有プールの定義](pool/README.md)（Pulumiで設定を保持し、ecspressoで配備）が管理します。
このstackのチーム別service 1〜6は`desiredCount: 0`で退役しており、適用済みの設定は[Pulumi.portal-dev.yaml.example](Pulumi.portal-dev.yaml.example)です。
このstackを適用するときはその設定を使い、`desiredCount`を戻さないでください。
旧ホスト名`portal-dev.isucon.whywrite.it`は解決できなくなっています。
このstackの`image`と`benchmarkImages`を更新してもポータルで走るベンチは変わらないので、ベンチを更新するときは共有プールの`pool.json`も更新して配備します。

## Benchmark dataset and regional routing

`teamImages` optionally maps team IDs to digest-pinned images. Workers without an
override use `image`. Preserve these overrides when updating the Supervisor so a
team's benchmark fixes are not replaced by the default benchmark binary.

Set `benchmarkScale: large` when the application database uses
`datagen -scale large`. The Supervisor passes `--scale large` to the benchmark
for both direct and Router-mapped execution. The portal-dev stack uses the
`app.teamN-aws.127.0.0.1.nip.io` Host to route requests by region. Fixed
`app1`–`app3` Hosts force one regional backend and cannot validate all regions
in the current initial application configuration.

## Shared portal NAT

The portal-dev stack now sets `sharedNetwork` with the portal VPC, its
`ap-northeast-1a` private subnet, and NAT egress IP `52.68.140.39`. It creates
no dedicated VPC or NAT in this mode; ECS tasks remain private and Router
ingress is restricted to the shared NAT `/32`. When moving existing workers,
temporarily set `retainDedicatedNetwork: true`, deploy the new task network,
verify the tasks, then remove that flag and deploy again to release the old VPC,
NAT and EIP.

## Routerの固定IPとネットワーク復旧

`routerNetworkInterfaces`にRouter Hostとprimary ENI IDを指定すると、
stackがEIPと関連付けを管理し、SupervisorのRouter接続先へ反映します。
出力`routerPublicIps`から割り当て済みIPを確認できます。

停止・起動後にRouterのdata NIC経路が消える場合は、cloud-initと静的netplanの
NIC定義の競合を確認します。`provisioning/scripts/router_network.py`は既存の
`90-isucon.yaml`を検証し、cloud-init側の重複定義を除去します。
Router上でrootとして実行し、再起動後もアプリ・DBへの経路が残ることを確認します。

### 共有Router Security Groupの管理

Supervisor stackは、`routerSecurityGroupIds`で指定した既存Security Groupへ、
共有NATの公開IP `/32` からTCP 8080へのingressを追加します。
参加者環境側のスタックで同じSecurity Groupのinline `ingress`を管理すると、
その適用時にSupervisor側のルールが削除される場合があります。
両スタックで同じingressの集合を管理しないでください。
参加者環境側でも個別ルールを管理するか、Supervisorの許可ルールを含めて
単一のスタックへ管理を集約する必要があります。

Routerの公開IPを参加者環境側で変更した場合は、`targetOverrides`も更新してください。
新規チームは`teamIds`への追加だけでなく、Portalに登録したappの公開IPから
Routerの公開IPと`app.teamN-aws.127.0.0.1.nip.io`への対応が必要です。

チームごとに接続先を切り替える場合は、`teamEndpoints` にチームIDとポータルURLの対応を設定します。
未指定のチームは `portalEndpoint` を使います。対象チームの task / service に適用範囲を限定し、ほかのチームの接続先を維持できます。
