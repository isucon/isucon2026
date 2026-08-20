# Local VM provisioning

Linux x86_64 上に Ubuntu 24.04 amd64 の problem server 3 台と Router 1 台を構築する、Packer・Ansible・Vagrant・VirtualBox 統合環境です。macOS と arm64 は対象外で、別 provider への自動 fallback はしません。

## 構成

| Node | data address | 配備内容 |
|---|---:|---|
| `server1` | `192.168.60.6/28` | PostgreSQL 16、backend、worker、frontend、nginx |
| `server2` | `192.168.60.22/28` | PostgreSQL 16、backend、worker、frontend、nginx |
| `server3` | `192.168.60.38/28` | PostgreSQL 16、backend、worker、frontend、nginx |
| `router` | `.5` / `.21` / `.37`、control `.69` | Router、Envoy 1.32.2 |

3 台の server は別々の VirtualBox internal network に接続し、相互向け route は各 segment の Router だけを next hop にします。Portal は VM に含めず、host の Docker Compose project `isureal-isureal-local-portal` で起動します。

host の入口は次の 3 つです。

- HTTP Client（ベンチマーク用）: `127.0.0.1:18080` → Router/Envoy `:8080`
- HTTPS Client（ローカル開発用）: `127.0.0.1:18443` → Router/Envoy TLS `:443`
- Portal UI: `127.0.0.1:15174`

Portal API は Router 専用 control path `192.168.60.65:18082` にだけ publish されます。problem server の application/database port は host へ forward しません。
Portalの`teamNumber=1`、`teamHash=local`、`domain=127.0.0.1.nip.io`が公開hostnameの正本で、Router/Envoyも`app.team1-local.127.0.0.1.nip.io`と`server1..3.team1-local.127.0.0.1.nip.io`を受理します。既定domainは`nip.io`なので、これらのhostnameはDNSで自動的に`127.0.0.1`へ解決され、`/etc/hosts`を編集せずローカルからアクセスできます。environment IDはVM・Compose・生成stateの名前空間にだけ使います。

## 前提と確認

`provisioning/config/release.json` が release 入力で、Python/ansible-core/Packer は `mise.toml` で固定します。対象 host は Linux x86_64 + VirtualBox で、Vagrant・VirtualBox・Docker Engine/Compose は exact version を要求しません。

```bash
make vm-preflight
make image-validate
```

preflight は VM を変更せず、host/architecture、hardware virtualization、driver、CPU/RAM/disk、必要コマンドの存在、Docker daemon/Compose 接続、schema、CIDR/port、ローカル artifact の存在、Portal の specific-address publish を検査します。

## 起動と確認

repository root から次を実行します。初回は ISO、box、OCI image の取得を含みます。

```bash
make vm-up
```

`vm-up` は preflight、application artifact build、ローカルTLS証明書生成、必要な Packer image build、4 VM 起動、Portal 起動、inventory 生成、runtime Ansible、conformance の順に実行します。host の `/etc/hosts` は変更しません。

```bash
make vm-tls-ca

curl --fail --show-error \
  --cacert provisioning/.state/tls/local-ca.crt \
  -H 'X-Country-Code: JP' \
  https://app.team1-local.127.0.0.1.nip.io:18443/

make vm-status
make vm-validate
make vm-logs
```

ブラウザでカメラ、Service Worker、`crypto.randomUUID()`などsecure-context限定APIを使うには、`provisioning/.state/tls/local-ca.crt`をOSまたはブラウザの信頼済みCAへ手動登録する。CAはenvironment destroy後も保持される。HTTP `:18080` はベンチマーク互換経路として残し、ローカルブラウザ開発ではHTTPS `:18443`を使う。

通常の再適用は PostgreSQL data を消しません。

```bash
make vm-provision
```

data reset と environment destroy は、意図が明確な専用 target から直接実行します。

```bash
make vm-reset-data
make vm-destroy
```

## Image build と変更境界

通常は `vm-up` が不足 image を作ります。image 担当者が個別に作る場合は次を使います。

```bash
make provisioning-artifacts
make image-build-base ARCH=amd64
make image-build-role ROLE=problem-server ARCH=amd64
make image-build-role ROLE=router ARCH=amd64
```

- application/frontend/Router の source 変更: artifact を再生成し、新しい release ID で role box を再buildする。
- Ansible の problem/Router role 変更: 対応する role box を新しい release ID で再buildする。
- Ubuntu ISO、共通 package、Autoinstall 変更: base と両 role box を新しい release ID で再buildする。
- IP、hostname、runtime secret、Portal port: image に焼き込まず runtime provisioning だけで適用する。

box は release ID、role、provider、architecture、SHA-256 とともに `provisioning/.state/images/image-artifact-index.json` へ記録されます。既定では同じ ID を別内容で上書きしませんが、ローカル開発で作り直したいときは `FORCE=1 make image-build-base` / `FORCE=1 make image-build-role ROLE=...` で既存 box を再生成できます（`promoted` 済み artifact は保護されます）。

## AWS AMI

AWSではVirtualBox boxを変換せず、release manifestへ固定したCanonical Ubuntu 24.04 amd64 AMIから、同じ
`provisioning/ansible/playbooks/image.yml`を使って次の2種類を作ります。

- `problem-server`: 3台のService VMで共用
- `router`: 中央Router VM

```bash
make provisioning-artifacts
make aws-image-validate
make aws-image-build-role ROLE=problem-server
make aws-image-build-role ROLE=router
```

既定regionは`ap-northeast-1`です。`AWS_REGION`、`AMI_BUILD_INSTANCE_TYPE`を明示的に上書きできます。
Packerの一時instanceへ到達できるpublic subnetを使う場合は、同じVPCに属する
`AMI_BUILD_VPC_ID`と`AMI_BUILD_SUBNET_ID`を両方指定します。AWS資格情報、Private IP、node identity、
runtime secret、Portal endpointはAMIへ保存しません。

生成AMIはregion、owner、source AMI、role、releaseとともに
`provisioning/.state/images/image-artifact-index.json`へ記録されます。同じrelease/role/regionを異なるAMIで
上書きしません。作り直す場合は新release IDを使用します。

## AWS検証トポロジー

`provisioning/aws`はPulumi TypeScriptで一時的な検証環境を作ります。3つのService VPCへ各1台の
problem serverを置き、中央Router EC2へ各VPCのdata ENIをattachします。Routerのprimary ENIは
server1 VPC内の専用control subnetに置くため、作成するVPCは合計3つです。既定Router instance typeは
4 ENIを収容できる`c8in.large`、Service VMは検証用の`t3.small`です。競技用の正式なinstance typeは
後続の競技環境仕様で上書きします。全VPC/ENIは同一Availability Zoneに作成し、Private IPは固定します。

各Service subnetのroute tableは、他の2 Service VPC CIDRを自身のVPC内にあるRouter ENIへ向けます。
VPC peering、Transit Gateway、Service VMのPublic IPは作りません。Router data ENIとRouter instanceの
source/destination checkは無効化し、guest内ではIP forwardingを有効化します。Service VMの管理経路は
各VPC内のSSM Interface Endpointへ閉じ、Routerのcontrol経路やインターネットを迂回路として使いません。

```bash
make aws-infra-install

cd provisioning/aws
pulumi stack init dev
pulumi config set aws:region ap-northeast-1
pulumi config set releaseId r2026.08.20.2
pulumi config set serviceAmiId ami-...
pulumi config set routerAmiId ami-...
pulumi config set clientCidr 203.0.113.10/32
pulumi config set --secret databasePassword '...'
pulumi config set --secret cursorHmacKey '...'
pulumi config set --secret tlsCertificate "$(cat server.crt)"
pulumi config set --secret tlsPrivateKey "$(cat server.key)"

make -C ../.. aws-infra-preview STACK=dev
make -C ../.. aws-infra-up STACK=dev
make -C ../.. aws-infra-validate STACK=dev
make -C ../.. aws-infra-destroy STACK=dev
```

検証用Portal APIはRouter AMIへ同梱し、Router VMのloopback `127.0.0.1:18082`だけで起動します。
`clientCidr`を設定した場合だけRouterの443/8080を指定CIDRへ公開します。SSHを許可する場合だけ
`keyName`と`sshCidr`を同時に設定します。通常は全4台のSSM managed-instance profileを使います。
検証環境はAMIから独立したPulumi stackなので、`aws-infra-destroy`はEC2、ENI、
VPC、route、SGを削除しますがAMIを削除しません。

AMIを昇格する前に、3台のservice health、6方向のpeer通信、Routerのnft/tc counter、Router停止中の
新規peer通信失敗、復旧後の再接続、clone identityの一意性を確認します。合格レポートは
`provisioning/.state/aws/<stack>/reports/`へ保存され、検証した2つのAMI artifactを`validated`へ遷移させます。
