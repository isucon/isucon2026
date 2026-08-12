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

host の入口は次の 2 つです。

- Client: `127.0.0.1:18080` → Router/Envoy `:8080`
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

`vm-up` は preflight、application artifact build、必要な Packer image build、4 VM 起動、Portal 起動、inventory 生成、runtime Ansible、conformance の順に実行します。host の `/etc/hosts` は変更しません。

```bash
curl --fail --show-error \
  -H 'X-Country-Code: JP' \
  http://app.team1-local.127.0.0.1.nip.io:18080/

make vm-status
make vm-validate
make vm-logs
```

通常の再適用は PostgreSQL data を消しません。

```bash
make vm-provision
```

明示的な data reset と environment destroy には、対象 environment ID と一致する確認値が必要です。

```bash
CONFIRM_RESET=isureal-local make vm-reset-data
CONFIRM_DESTROY=isureal-local make vm-destroy
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

box は release ID、role、provider、architecture、SHA-256 とともに `provisioning/.state/images/image-artifact-index.json` へ記録され、同じ ID の異なる内容で上書きできません。

## 将来の AMI

guest 構成の中心は `provisioning/ansible/playbooks/image.yml` と provider 非依存 role です。将来の `amazon-ebs` source はこの playbook を再利用し、AWS の source image、network、credential、AMI registration/share を Packer/provider 側へ限定します。現時点の local command は AWS credential を受け取らず、AMI の生成・登録・公開を行いません。
