# Provisioning

AWS 上に、Router 1 台、app 3 台（app1=asia、app2=europe、app3=america）、db 1 台の環境（チーム）を作ります。
同じアカウントに複数のチームを作れます。作業は AMI の作成と、Pulumi でのデプロイの 2 つです。

| ディレクトリ | 内容 |
|---|---|
| `ansible/` | AMI の中身（webapp、PostgreSQL、nginx、Router、Envoy）。詳細は [ansible/README.md](ansible/README.md) |
| `packer/` | Ubuntu 24.04 の公式 AMI から、Ansible で `problem-server` と `router` の 2 種類の AMI を作る |
| `aws/` | Pulumi。共有の VPC に、チームごとのサブネット、Router、EC2 を作り、user-data で各ノードの役割とアドレスを設定する |

## AMI の作成

AWS の認証を済ませてから実行します。成果物のビルド（`ansible/prepare.yml`）のあと、両方の AMI を作ります。

```sh
make ami                 # 両方
make ami ROLE=router     # 片方だけ（problem-server または router）
```

AMI には `Project=isucon2026` と `Role=<role>` のタグが付きます。problem-server には本番の規模の初期データ
（datagen の `-scale large`、DB で約 18 GB）を入れるので、ビルドに 45 分ほどかかります（ビルド用インスタンスは
c6i.2xlarge、ディスクは 60 GB。インスタンスのディスクも同じ大きさになる）。古い AMI は自動では消えないので、
不要になったらスナップショットごと削除してください。

## envcheck AMI の作成

envcheck は競技用 AMI とは別に作成します。
Go チェッカーは起動時に `test-boot`、`isucon` ユーザーの SSH ログイン時に `test-ssh` を JSON POST でポータルへ送信します。
同じ VPC にある 5 台の名前、インスタンスタイプ、AMI、AZ、起動状態、ディスクとネットワークインターフェースの数を検査します。

```sh
go -C portal test ./cmd/envcheck
python3 -m unittest discover -s provisioning/packer/envcheck -p 'test_*.py'
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go -C portal build -o bin/isucon-env-checker ./cmd/envcheck
packer init provisioning/packer/envcheck
packer build provisioning/packer/envcheck
```

Packer が参照できる AWS 認証情報を設定してください。
`aws login` を使う場合は、既存の `provisioning/aws-sdk.config` と同様に、利用する profile の `aws configure export-credentials --format process` を `credential_process` に指定します。
既定 VPC がない場合は `-var 'vpc_id=...' -var 'subnet_id=...'` でビルド用の public subnet を指定します。
一時的なビルド用 EC2 は Packer が削除し、AMI と EBS スナップショットが残ります。
起動時のサービスは `cloud-init.target` に登録し、認証情報と SSH 公開鍵の配置を行う `cloud-final.service` の終了を待ちます。
Ubuntu の `cloud-final.service` は `multi-user.target` より後に動くため、チェッカーを `multi-user.target` に登録すると起動順序が循環します。

作成した AMI ID は、ポータルの Pulumi stack の `environment.ISUXPORTAL_TEST_AMI_ID` に設定します。
`pulumi up` と ecspresso によるポータルの再デプロイ後、検証用チームに AZ と SSH 公開鍵を設定してテンプレートを取得します。
ポータルには JSON POST を受け付けるバージョンが必要です。
CloudFormation での 5 台の作成完了、各 IP の `test-boot` 成功、各台への SSH ログイン後の `test-ssh` 成功をポータルのチェック履歴で確認し、検証後は CloudFormation stack を削除します。
進捗表示は各インスタンスの最新の起動結果と、その後の SSH 結果で判定します。
すべて成功した場合だけ完了になり、インスタンスを作り直すと以前の成功は引き継ぎません。

## デプロイ

指定がなければ、タグが一致する最新の AMI を使います。

```sh
make aws-infra-install

cd provisioning/aws
pulumi stack init dev
pulumi config set aws:region ap-northeast-1
pulumi config set teamCount 3            # 任意。既定は 1
pulumi config set --path 'clientCidrs[0]' 203.0.113.10/32
# 任意。指定しなければ自己署名の証明書を生成する
pulumi config set --secret tlsCertificate < server.crt
pulumi config set --secret tlsPrivateKey < server.key
cd ../..

make aws-infra-preview STACK=dev
make aws-infra-up STACK=dev
make aws-benchmark STACK=dev TEAM=2   # 手元からチーム 2 へベンチ（-scale large、TEAM の既定は 1）
make aws-infra-destroy STACK=dev
```

サーバーのルートボリュームには EBS の初期化レート（300 MiB/s）を指定しているので、起動から数分でスナップショット
からの読み込みが終わり、すぐにベンチを回せます（指定しないと遅延ロードで DB の初回読み込みが 10 MB/s 台まで落ちる）。
Router の 443/8080 は `clientCidrs`（複数指定可）からしか受け付けないので、手元の IP が変わったら設定し直してください。

任意の設定は `teamCount`（既定 1、最大 15）、`serviceAmiId` / `routerAmiId`（AMI の固定）、
`serviceInstanceType`（既定 c7i.large）/ `routerInstanceType`（既定 c6in.large）、`availabilityZone` です。
チームごとの Router の IP とホスト名は `pulumi stack output teamOutputs` で確認します。

1 チームは 10 vCPU（c7i.large × 4 + c6in.large）を使います。オンデマンドの標準インスタンスの vCPU 上限
（`L-1216C47A`）に注意してください。EIP は使いません（Router もサーバーも、起動時に割り当てられるパブリック IPv4）。

## ネットワーク

- VPC、IGW、EC2 Instance Connect Endpoint、IAM ロールは全チームで共有する（アカウントの上限があるため増やさない）
- チーム n は `192.168.(16(n-1)+k).0/24` を使う。k = 1〜4 がサーバー、10 が Router のデータ用、11 が Router のパブリック用。
  ホスト名は `team<n>-aws.127.0.0.1.nip.io`
- 同じ VPC の他のチームのサーバーとは経路がつながるが、各サーバーの SG は自分のチームのサブネットと Router しか許可しない
- サーバーごとにサブネットを分ける。各サーバーのサブネットのルートテーブルは、他のサーバーの
  サブネット宛てを Router のデータ用 ENI に向ける。Router は NAT せず、同じ ENI から宛先へ折り返す
- 地域間の遅延と帯域は、Router がデータ用 ENI に置く tc（送信元と宛先の IP の組で分類）で再現する
- Router のパブリック IP が bench の入口。サーバーの NACL で、サーバーからこの IP とパブリック用サブネットへの
  通信を拒否する
- サーバーのパブリック IPv4 は外向き通信専用で、インターネットからの着信は SG で受けない
- SSH は公開しない。SSM Session Manager か EC2 Instance Connect Endpoint で入る

```sh
aws ec2-instance-connect ssh --instance-id i-xxxxxxxx --connection-type eice
```

Terminal Bでhealthを確認し、UIを起動します。

```bash
curl -fsS http://127.0.0.1:28082/healthz
npm --prefix portal-mock/web ci
PORTAL_API_TARGET=http://127.0.0.1:28082 \
  npm --prefix portal-mock/web run dev -- --host 127.0.0.1 --port 15174
```

ブラウザで`http://127.0.0.1:15174/`を開きます。SSHを有効化したstackでは、同じloopback宛ての
local port forwardingをSSHで作ることもできます。`portal-mock/compose.yaml`は別のローカルPortal APIを
起動するため、AWS Routerの状態確認には使いません。
検証環境はAMIから独立したPulumi stackなので、`aws-infra-destroy`はEC2、ENI、
VPC、route、SGを削除しますがAMIを削除しません。

AMIを昇格する前に、4台のservice health、5台それぞれのEIP送信元、Global IPによる20方向の通信禁止、
12方向のPrivate peer通信、Routerのnft/tc counter、Router停止中の新規peer通信失敗、復旧後の再接続、
clone identityの一意性を確認します。合格レポートは
`provisioning/.state/aws/<stack>/reports/`へ保存され、検証した2つのAMI artifactを`validated`へ遷移させます。

## 試し解き用Supervisor

実ポータルからベンチジョブを受け取るチーム専用Fargate環境は、[Supervisor構築手順](aws/supervisor/README.md)を参照してください。
`provisioning/aws/supervisor`の独立Pulumi stackで管理します。

### 既存環境を保持した追加構築

既存チームが稼働している場合は、`serviceAmiId` と `routerAmiId` を稼働中のAMIに固定してから新しいAMIを作成する。
未指定の場合は最新AMIを自動選択するため、新規AMIの作成後に既存インスタンスも置換対象になる。

`teamImages` はチーム番号をキーとし、`serviceAmiId` と `routerAmiId` を持つオブジェクトを指定する。
新しいチームだけに別のAMIを割り当てられる。
`independentTlsTeams` に追加チームの番号を指定すると、そのチーム専用の自己署名証明書を作成し、共通証明書のSANを変更しない。
外部の証明書・秘密鍵を指定している場合は、その証明書が優先される。
`teamSshPublicKeys` にはチーム番号ごとの公開鍵配列を指定でき、初回起動時に回答用サーバーの `isucon` ユーザーへ設定する。
`teamAdminSshPublicKeys` はポータルのコマンド実行に使う `isuadmin` ユーザーと運営公開鍵を設定する。
秘密鍵はSecrets Managerで管理し、スタック設定には含めない。
未指定のチームのSSH設定には変更を加えない。
`portalTeamIds` でネットワーク番号とポータルのチームIDを対応させると、回答用インスタンスにポータル同期用の `IsuconTeamID` / `IsuconInstanceNum` / `Role=contestant` タグを付与する。
Routerは参加者の操作対象に含めない。

変更前に全体のプレビューと追加チームに限定したプレビューを確認し、既存リソースの置換・削除・設定更新を適用しない。
実際のAMI ID、チームと環境の対応、接続情報は[privateの運用記録](https://github.com/isucon/isucon2026-private/pull/2)で管理する。
