# Local VM provisioning

Linux x86_64 上に Ubuntu 24.04 amd64 の app1〜3 と db1 の4台、および Router 1 台を構築する、Packer・Ansible・Vagrant・VirtualBox 統合環境です。macOS と arm64 は対象外で、別 provider への自動 fallback はしません。

## 構成

| Node | data address | 配備内容 |
|---|---:|---|
| `app1` | `192.168.60.6/28` | backend、worker、frontend、nginx（DB は db1 を共有） |
| `app2` | `192.168.60.22/28` | backend、worker、frontend、nginx（DB は db1 を共有） |
| `app3` | `192.168.60.38/28` | backend、worker、frontend、nginx（DB は db1 を共有） |
| `db1` | `192.168.60.54/28` | PostgreSQL 16（AP 固定、全 app 共有） |
| `router` | `.5` / `.21` / `.37` / `.53`、control `.69` | Router、Envoy 1.32.2 |

4 台の server は別々の VirtualBox internal network に接続し、相互向け route は各 segment の Router だけを next hop にします。Portal は VM に含めず、host の Docker Compose project `isureal-isureal-local-portal` で起動します。

host の入口は次の 3 つです。

- HTTP Client（ベンチマーク用）: `127.0.0.1:18080` → Router/Envoy `:8080`
- HTTPS Client（ローカル開発用）: `127.0.0.1:18443` → Router/Envoy TLS `:443`
- Portal UI: `127.0.0.1:15174`

Portal API は Router 専用 control path `192.168.60.65:18082` にだけ publish されます。problem server の application/database port は host へ forward しません。
Portalの`teamNumber=1`、`teamHash=local`、`domain=127.0.0.1.nip.io`が公開hostnameの正本で、Router/Envoyも`app.team1-local.127.0.0.1.nip.io`と`app1..3.team1-local.127.0.0.1.nip.io`を受理します。既定domainは`nip.io`なので、これらのhostnameはDNSで自動的に`127.0.0.1`へ解決され、`/etc/hosts`を編集せずローカルからアクセスできます。environment IDはVM・Compose・生成stateの名前空間にだけ使います。

## 前提と確認

`provisioning/config/release.json` はOS source、artifact参照、runtime compatibilityだけを持つrelease入力です。version制約は実際に導入へ使う`mise.toml`、Packer HCL、`requirements.yml`へ置き、Ubuntu packageは生成boxのSHA-256またはAMI IDで配備物を固定します。対象hostはLinux x86_64 + VirtualBoxで、host toolはexact versionを要求しません。

```bash
make vm-preflight
make image-validate
```

preflight は VM を変更せず、host/architecture、hardware virtualization、driver、CPU/RAM/disk、必要コマンドの存在、Docker daemon/Compose 接続、schema、CIDR/port、ローカル artifact の存在、Portal の specific-address publish を検査します。

app1=AP、app2=EU、app3=US、db1=AP は固定です。db1 は公開 HTTP の対象外で、PostgreSQL は private address で app1〜3 からのみ認証接続を受け付けます。初期化は db1 で一度だけ行い、reset 時は全 app の API/worker を停止してから共有 DB を初期化し、成功・失敗にかかわらずサービスを復旧します。

旧 server1〜3 構成の既存データは自動移行しません。新しい `VM_ENV_ID` と再ビルドした role image を使用してください。

## 起動と確認

repository root から次を実行します。初回は ISO、box、OCI image の取得を含みます。

```bash
make vm-up
```

`vm-up` は preflight、application artifact build、ローカルTLS証明書生成、必要な Packer image build、5 VM 起動、Portal 起動、inventory 生成、runtime Ansible、conformance の順に実行します。host の `/etc/hosts` は変更しません。

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

Vagrant の HTTP Client forward を通してベンチマーカーを実行する。接続先と HTTP Host
ヘッダーは topology から導出するため、port や team hostname をコマンドへ重複記載しない。
実行時はベンチマーカーの initialize phase により対象アプリのデータが初期化される。

```bash
make vm-benchmark

# 開発用の固定 1 サイクル
make vm-benchmark BENCH_ARGS='-users 2 -cycles 1'
```

ブラウザでカメラ、Service Worker、`crypto.randomUUID()`などsecure-context限定APIを使うには、`provisioning/.state/tls/local-ca.crt`をOSまたはブラウザの信頼済みCAへ手動登録する。CAはenvironment destroy後も保持される。HTTP `:18080` はベンチマーク互換経路として残し、ローカルブラウザ開発ではHTTPS `:18443`を使う。

## 日常のアプリ更新

起動・provision済みのapp1〜3へ、ホストでビルドした成果物だけを反映します。
既定のcompact設定（サービス各2 vCPU / 1536 MiB、Router 2 vCPU / 1024 MiB）と
ネットワーク経路は維持します。Routerを含むVMは計5台、割当メモリは合計7 GiBです。

| 用途 | コマンド |
|---|---|
| 初回構築・起動 | `make vm-up` |
| backend/frontendの変更反映 | `make vm-deploy` |
| backendのみ反映 | `make vm-deploy COMPONENT=backend` |
| frontendのみ反映 | `make vm-deploy COMPONENT=frontend` |
| データ初期化を伴う短いベンチ | `make vm-benchmark BENCH_ARGS='-users 2 -cycles 1'` |
| runtime設定の再適用 | `make vm-provision` |
| 構成全体の検証 | `make vm-validate` |

`vm-deploy`は全サービスVMの稼働状態・配備済みマイグレーションとRouterの稼働を確認し、
対象コンポーネントだけをビルドします。DBとRouterへファイルを書き込まず、DB再起動・初期化、
パッケージ更新、ネットワーク再設定、box作成を行いません。backendに変更があるappだけで
API・workerを再起動し、frontendのみの変更ではサービスを再起動しません。
各appのreadinessとRouter経由の各appのHTML応答を確認します。ベンチマークは別途実行します。

ソース・ツールチェーン・ビルド設定が同じ場合は、SHA-256を検証したキャッシュを再利用します。
frontendの`npm ci`は初回、依存定義・ツールバージョン変更時、依存ディレクトリ欠落時に行います。
配布はAnsibleの内容比較で変更ファイルだけを更新し、古いfrontendファイルを削除します。
ビルドキャッシュは環境別・コンポーネント別に直近2世代まで保持します。
同じ環境への`vm-deploy`の同時実行は拒否し、共有frontend作業領域のビルドは排他します。
`vm-provision`やベンチと同時に実行しないでください。

マイグレーション差分がある場合は配布前に停止します。通常の更新手順で
`make provisioning-artifacts`、`make vm-provision`を実行してスキーマを反映してください。
適用済みマイグレーションの削除・書き換えによるDBの巻き戻しは対象外です。
OSパッケージ・systemd unit・nginx設定・Router・Portalの変更も通常のprovision/image経路を使います。
`vm-provision`自体は成果物をビルドしないため、ソース変更時は先に成果物生成が必要です。
通常の再適用はPostgreSQL dataを消しません。

失敗時は非ゼロ終了し、Ansibleのrecapで変更・失敗・到達不能のノードを確認できます。
部分更新を自動でロールバックしません。backendの一部を更新した後に失敗した場合も、
到達可能なノードではAPI・workerの再起動を試みます。原因解消後に同じコマンドで再適用できます。
稼働前提を満たせない場合は`make vm-up`または`make vm-provision`で復旧してください。

実行ごとの記録は`provisioning/.state/<environment>/deploy/<UTC timestamp>/report.json`です。
ビルド・全体の秒数、Ansibleの配布・再起動・疎通確認の秒数、ノードごとの結果、成果物ハッシュを
記録し、boxのrelease IDやimage indexは更新しません。Ansibleの工程時間は並行処理を含む経過時間です。
ローカル検証記録と実VMでの比較手順は
[アプリ更新の検証記録](../specs/004-local-vm-provisioning/validation/app-deploy-results.md)を参照してください。

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

- backend/frontendの通常のsource変更: `vm-deploy`で既存VMへ反映する。
- 再現・配布用imageへapplication/frontend/Routerの変更を取り込む場合: artifactを再生成し、新しいrelease IDでrole boxを再buildする。
- Ansible の problem/Router role 変更: 対応する role box を新しい release ID で再buildする。
- Ubuntu ISO、共通 package、Autoinstall 変更: base と両 role box を新しい release ID で再buildする。
- IP、hostname、runtime secret、Portal port: image に焼き込まず runtime provisioning だけで適用する。

box は release ID、role、provider、architecture、SHA-256 とともに `provisioning/.state/images/image-artifact-index.json` へ記録されます。既定では同じ ID を別内容で上書きしませんが、ローカル開発で作り直したいときは `FORCE=1 make image-build-base` / `FORCE=1 make image-build-role ROLE=...` で既存 box を再生成できます（`promoted` 済み artifact は保護されます）。

## AWS AMI

AWSではVirtualBox boxを変換せず、release manifestへ固定したCanonical Ubuntu 24.04 amd64 AMIから、同じ
`provisioning/ansible/playbooks/image.yml`を使って次の2種類を作ります。

- `problem-server`: 4台のService VMで共用
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
app1 VPC内の専用control subnetに置くため、作成するVPCは合計3つです。既定Router instance typeは
5 ENI（control 1 + data 4）を収容できる`c8in.4xlarge`、Service VMは検証用の`t3.small`です。競技用の正式なinstance typeは
後続の競技環境仕様で上書きします。全VPC/ENIは同一Availability Zoneに作成し、Private IPは固定します。

各Service subnetのroute tableは、他の2 Service VPC CIDRを自身のVPC内にあるRouter ENIへ向けます。
VPC peeringとTransit Gatewayは作りません。Router data ENIとRouter instanceのsource/destination checkは
無効化し、guest内ではIP forwardingを有効化します。Service VMの管理経路には各VPC内のSSM Interface
Endpointも用意します。
Service VMのSecurity Groupは、他の2 Service VPC CIDRと自身のRouter data IP `/32`からの全IPv4
プロトコルを許可します。peer VPCからの通信は送信元Service IPを維持したまま必ずRouterを通過します。
自身のVPC CIDR全体は許可しないため、control subnetからService VMへの直接バイパスは作りません。

Routerと4台のService VMには固定Elastic IPを1個ずつ割り当てます。各Service VPCへInternet Gatewayを
attachし、Service subnetの既定経路はInternet Gatewayへ向けますが、peerのPrivate CIDRにはより具体的な
Router ENI経路が優先されます。各Service subnetとRouter control subnetのNetwork ACLは、他の全nodeの
Elastic IP `/32`を送受信とも先行denyしてから、それ以外のIPv4通信を許可します。このためserver間通信は
Global IPでは成立せず、Private IPとRouterを使用します。Public SSHは既定で無効です。
`enablePublicSsh=true`に加えて`sshPublicKey`と`sshCidr`を設定した場合だけ、Routerと全Service VMのTCP/22を
指定CIDRへ許可します。アプリ用ポートはService VMで公開しません。Elastic IPは利用時間に応じて課金されます。

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

割り当てられたGlobal IPはstack outputで確認できます。

```bash
pulumi -C provisioning/aws stack output routerPublicAddress --stack dev
pulumi -C provisioning/aws stack output servicePublicAddresses --stack dev
```

Public SSHを明示的に有効化する場合は、手元の公開鍵と接続元のGlobal CIDRを同時に指定します。
`enablePublicSsh`を省略した場合は`false`です。秘密鍵はPulumiやAWSへ渡しません。

```bash
pulumi -C provisioning/aws config set enablePublicSsh true --stack dev
pulumi -C provisioning/aws config set sshPublicKey "$(cat ~/.ssh/id_ed25519.pub)" --stack dev
pulumi -C provisioning/aws config set sshCidr 203.0.113.10/32 --stack dev
make aws-infra-up STACK=dev
```

無効化は`pulumi -C provisioning/aws config set enablePublicSsh false --stack dev`です。`sshCidr`には通常、
管理端末のGlobal IP `/32`を指定し、`0.0.0.0/0`は使用しません。`sshPublicKey`はPulumi管理のEC2 Key Pair
としてAWSへそのまま登録し、鍵の形式と種類はAWS EC2が検証します。AWSは起動時に公開鍵をVMへ配置する
ため、既存stackへ初めて鍵を設定する場合や鍵を変更する場合は5台のinstanceが置換対象になります。必ず
`aws-infra-preview`で確認してから適用します。

検証用Portal APIはRouter AMIへ同梱し、Router VMのloopback `127.0.0.1:18082`だけで起動します。
`clientCidr`を設定した場合だけRouterの443/8080を指定CIDRへ公開します。SSHを許可する場合だけ
`enablePublicSsh=true`と`sshPublicKey`、`sshCidr`を同時に設定します。通常は全5台のSSM
managed-instance profileを使います。
Router AMIにPortalのブラウザUIは同梱しません。AWS上のPortal APIへはSSM port forwardingで接続し、
ローカルのVite UIをそのトンネルへ向けます。Terminal Aで次を実行してsessionを維持します。

```bash
ROUTER_ID="$(pulumi -C provisioning/aws stack output routerInstanceId --stack dev)"
aws ssm start-session \
  --region ap-northeast-1 \
  --target "${ROUTER_ID}" \
  --document-name AWS-StartPortForwardingSession \
  --parameters '{"portNumber":["18082"],"localPortNumber":["28082"]}'
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
