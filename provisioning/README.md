# Provisioning

AWS 上に Router 1 台、app 3 台（app1=asia、app2=europe、app3=america）、db 1 台の環境を作ります。
作業は AMI の作成と、Pulumi でのデプロイの 2 つです。

| ディレクトリ | 内容 |
|---|---|
| `ansible/` | AMI の中身（webapp、PostgreSQL、nginx、Router、Envoy）。詳細は [ansible/README.md](ansible/README.md) |
| `packer/` | Ubuntu 24.04 の公式 AMI から、Ansible で `problem-server` と `router` の 2 種類の AMI を作る |
| `aws/` | Pulumi。1 つの VPC にサーバーごとのサブネット、Router、EC2 を作り、user-data で各ノードの役割を設定する |

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
pulumi config set --path 'clientCidrs[0]' 203.0.113.10/32
# 任意。指定しなければ自己署名の証明書を生成する
pulumi config set --secret tlsCertificate < server.crt
pulumi config set --secret tlsPrivateKey < server.key
cd ../..

make aws-infra-preview STACK=dev
make aws-infra-up STACK=dev
make aws-benchmark STACK=dev     # 手元からベンチ（-scale large）
make aws-infra-destroy STACK=dev
```

サーバーのルートボリュームには EBS の初期化レート（300 MiB/s）を指定しているので、起動から数分でスナップショット
からの読み込みが終わり、すぐにベンチを回せます（指定しないと遅延ロードで DB の初回読み込みが 10 MB/s 台まで落ちる）。
Router の 443/8080 は `clientCidrs`（複数指定可）からしか受け付けないので、手元の IP が変わったら設定し直してください。

任意の設定は `serviceAmiId` / `routerAmiId`（AMI の固定）、`serviceInstanceType`（既定 c7i.large）/
`routerInstanceType`（既定 c6in.large）、`availabilityZone` です。

## ネットワーク

- 1 つの VPC で、サーバーごとにサブネットを分ける。各サーバーのサブネットのルートテーブルは、他のサーバーの
  サブネット宛てを Router のデータ用 ENI に向ける。Router は NAT せず、同じ ENI から宛先へ折り返す
- 地域間の遅延と帯域は、Router がデータ用 ENI に置く tc（送信元と宛先の IP の組で分類）で再現する
- Router のパブリック用 ENI（EIP）が bench の入口。サーバーの NACL で、サーバーからこの EIP とパブリック用
  サブネットへの通信を拒否する
- サーバーのパブリック IPv4 は外向き通信専用で、インターネットからの着信は SG で受けない
- SSH は公開しない。SSM Session Manager か EC2 Instance Connect Endpoint で入る

```sh
aws ec2-instance-connect ssh --instance-id i-xxxxxxxx --connection-type eice
```

IP アドレスは `aws/lib/topology.ts` と `ansible/group_vars/all.yml` の両方に書いてあるので、変えるときは両方を直してください。
