# ポータルの Pulumi 基盤

webapp の `provisioning/aws` と同じ
TypeScript、`@pulumi/aws`、`@pulumi/pulumi`、Vitest を使います。
依存バージョンは `package.json` と `package-lock.json` で固定します。 project は
`isucon2026-portal` として分離し、開発と本番には別 stack を作成します。
構成とリリース手順は [deploy README](../README.md) を参照してください。

## 初期設定

Node.js 24 LTS、Pulumi CLI v3、AWS CLI v2、構築用の AWS 権限を用意します。
`pulumi login` で webapp と同じ backend にログインしてください。 S3 backend
を使う場合は `pulumi login s3://<state-bucket>/<portal-prefix>` とします。 state
バケットは別管理で作成し、versioning、暗号化、公開アクセス遮断を有効にしてください。
Pulumi の backend が state と更新ロックを管理します。 既存の Terraform state
は読み込みません。 今回の Terraform 資材は AWS に未適用のため、state
の移行は不要です。 別環境で既に適用した場合は、既存リソースを `pulumi import`
する計画を先に作成し、重複作成を避けてください。

GitHub OIDC provider はアカウント共通のものを指定します。 存在しない場合は
`https://token.actions.githubusercontent.com`、audience `sts.amazonaws.com`
で作成してください。 ECS の service-linked role `AWSServiceRoleForECS`
も事前に用意します。 これらの作成権限はデプロイ用 CI role に付与しません。

```sh
cd portal/deploy/pulumi
npm ci
pulumi login
pulumi stack init develop
cp Pulumi.develop.yaml.example Pulumi.develop.yaml
# stack 設定の REPLACE 値を対象環境に合わせて編集
aws rds describe-orderable-db-instance-options \
  --region ap-northeast-1 --engine aurora-postgresql \
  --engine-version 17.7 --db-instance-class db.serverless \
  --query 'OrderableDBInstanceOptions[].AvailabilityZones[].Name'
pulumi preview --diff
pulumi up
pulumi stack output
```

`domainName` は `route53ZoneId` で指定した公開ゾーン配下の名前を使います。 ACM
の DNS 検証と ALB alias を Pulumi が作成します。
初回構築にアプリケーションイメージは不要です。 秘密値の登録と ECR への push
後、ecspresso がサービスを作成します。

本番は `production` stack を別途作り、`name`、ドメイン、GitHub Environment を変更します。
異なる AWS アカウントにも同じ定義を適用できます。
stack 設定は Git 管理外で、設定例だけをコミットします。
アプリケーションの秘密値は Secrets Manager に別途登録し、Pulumi に渡しません。
`environment` には文字列の非秘密値、`secretKeys` には application secret の JSON キー名だけを指定します。
開発環境の設定例は Slack 通知用の `ISUXPORTAL_SLACK_WEBHOOK_URL` を含みます。
起動前に application secret の同名キーへ Webhook URL を登録してください。
Slack 通知を利用しない環境では、このキーを `secretKeys` から除いてください。
Webhook URL を `environment` や Git 管理下のファイルへ書かないでください。

```sh
pulumi config set --path 'environment.ISUXPORTAL_ADMIN_ONLY' '1'
pulumi config set auroraMinAcu 2
pulumi config set auroraMaxAcu 16
```

bot を有効化する場合は `secretKeys` に既定の認証キーと
`ISUXPORTAL_DISCORD_BOT_TOKEN` を列挙し、`enableBot` を true にします。
配列指定は既定値の置き換えになるため、既存の必須キーを省略しないでください。
設定の検証は `config.ts` が行います。

## ecspresso との境界

Pulumi は VPC、ALB、Aurora、Valkey、ECR、IAM、Secrets Manager
の入れ物、S3、CloudWatch を管理します。 ECS は cluster
だけを作成し、サービスとタスク定義は ecspresso の管理対象です。
`ecspressoConfigUri` output が示す S3 object は、ARN と接続情報だけを含みます。
タスクとサービスの定義はリポジトリの `../ecspresso/*.jsonnet` が持ちます。
Pulumi 設定を変更した後は、`pulumi up` と ecspresso
による再デプロイの両方が必要です。

各 AZ に NAT Gateway を配置します。 Aurora 2 台、Valkey 2 台、NAT 2 台、ALB
は検証環境でも常時課金されます。 既定の Aurora 最小容量は各 2 ACU、合計 4 ACU
です。 `vpcCidr` は IPv4 の /16 を指定し、その中に public /24 を 2 つ、private
/24 を 2 つ作成します。

アバター画像はアプリケーションが S3 の直接 URL を返すため、`avatars/` だけを公開
GET にしています。 アカウントの Block Public Access が公開 bucket policy
を禁止している場合は作成できません。 その場合はアプリケーション側の画像配信を
CloudFront 等へ変更する必要があります。 DB 接続には Aurora 管理の `isuxportal`
ユーザーを使います。
スキーマ所有者と実行ユーザーを分離する場合は、実行用ユーザーと別 secret
を用意し、ecspresso 定義の参照も変更します。

## 検証と破棄

```sh
npm run typecheck
npm test
npm run format:check # Deno が必要
```

テストは Pulumi runtime mocks を使い、AWS への接続やリソース作成は行いません。
実アカウントでの IAM、クォータ、疎通、フェイルオーバーは staging で確認します。

破棄時は先に ecspresso で各サービスを 0 タスクにし、サービスを削除します。
その後にバックアップと S3 のバージョンを保存します。 Aurora の Pulumi
`protect`、Aurora と ALB
の削除保護は、対象環境に限って明示的に解除してください。 S3
は強制削除を使わないため、内容が残っていれば `pulumi destroy` は停止します。
最終 DB スナップショット `<name>-final` が存在する場合は別名を指定します。

## Trial environment outbound access

`natGatewayCount` accepts `1` or `2` and defaults to `2`. The develop stack uses
`1`: both private AZs share the NAT in the first public subnet. The stack
exports `vpcId`, `privateSubnetIds`, and `natPublicIps` for consumers such as
the trial Supervisor. A single NAT reduces cost and EIP allocation at the
expense of outbound AZ redundancy.

## Route 53 以外の DNS を使用する場合

`dnsManagement=external` を指定すると、Route 53 のレコードを作成せず、ACM の DNS 検証と HTTPS リスナーは引き続き Pulumi で管理します。
`route53ZoneId` はこのモードでは使用しません。
外部 DNS 側には `certificateValidationRecords` の CNAME と、`loadBalancerDnsName` を向くサービスのレコードを設定してください。
証明書検証 CNAME は自動更新にも使用するため、発行後も保持してください。

既存ドメインからの切替では、まず `portal-external` ACM リソースだけを対象に作成し、外部 DNS へ検証 CNAME を追加します。
証明書が発行された後に通常の `pulumi preview` / `pulumi up` を実行し、ecspresso でデプロイします。
証明書とリクエストの応答を確認してから、外部 DNS のサービスレコードを切り替えます。
`external` への変更は従来の Route 53 レコードと証明書を削除するため、旧ホスト名を引き続き使う場合は別途維持する構成が必要です。
具体的なレコード、変更前の値、切り戻し先は非公開の環境別資料へ記録してください。

CloudWatch ログの保存期間は `logRetentionDays`（既定値: `0`、無期限）で設定する。
既存スタックに有限の日数が明示されている場合、その値は維持されるため、全ログを保持するには `0` に変更して適用する。
ロググループには `skipDestroy` を設定し、スタック削除時にもログを残す。

待機環境で Bot だけを起動する場合は、`standby: true` を維持し、
`enableBot: true` と `enableBotInStandby: true` を設定します。
Worker と通知ループは停止したままです。Bot のイベントが積んだジョブは
Worker 起動後に処理されるため、本番切替時に旧環境との同期状態を確認してください。
旧環境が同じトークンで Gateway Bot を実行していないことも確認します。

### envcheck AMIの公開

本番スタックで `publishEnvcheckAmi: true` を明示すると、環境変数
`ISUXPORTAL_TEST_AMI_ID` のAMIに限り、Pulumiが `group=all` の起動許可を管理します。
既定値はfalseです。競技用AMIは公開しません。同じAMIの公開許可を複数スタックで管理しないでください。

リージョンのAMI公開ブロックが `block-new-sharing` の場合、適用操作の間だけ
解除し、成功・失敗にかかわらず元へ戻します。これにより公開済みAMIは公開を
維持し、新たな意図しない公開を防ぎます。AMIを変更する場合も同じ手順が必要です。
公開後は `Public=true` と `LaunchPermissions` の `Group=all` を読み戻し、
`pulumi preview` に差分がないことを確認してください。
