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

アドレスの定義は `aws/lib/topology.ts` だけにあります（AMI には焼き込まない）。
