# Quickstart Validation Guide: ローカルVM競技環境

**Feature**: `004-local-vm-provisioning`

**Date**: 2026-08-06

**Status**: 設計時のvalidation contract。`provisioning/`実装後にこの手順をそのままrelease gateとして使う。

## 1. Supported host matrix

| Host | Hypervisor | Guest | Required full validation |
|---|---|---|---|
| Linux x86_64 | VirtualBox 7.2.14 | Ubuntu 24.04 amd64 | Yes |

開始時の固定toolchain:

- Vagrant 2.4.9
- VirtualBox 7.2.14 Base Package。Extension Packは不要。
- Docker Engine 29.7.1 + Docker Compose 5.4.0
- Packer 1.15.4（image build担当者のみ）
- Python 3.12 + ansible-core 2.21.2
- Packer plugins: virtualbox 1.1.5、vagrant 1.1.7、ansible 1.1.5
- Ansible collection: community.postgresql 4.2.0

macOS、Linux arm64、その他のhost OS/architecture、別VirtualBox minor、VMware/QEMU系providerは今回のsupport・artifact・合格matrixに含めない。自動fallbackもしない。

## 2. Prerequisites

1. hostのhardware virtualizationを有効にする。
2. 上記Vagrant/VirtualBoxとDocker Engine/Composeを導入し、Docker daemonを起動する。
3. repository toolchainからPython/Ansibleと、image担当者はPackerを導入する。
4. `provisioning/config/release.json`が参照するrole boxesまたはprivate artifact accessを用意する。
5. VMとPortal container用にresource profileが要求する空きmemory、CPU、diskを確保する。

default workflowはhost `/etc/hosts`を変更せず、Vagrant shared folderも使わない。runtime secretは初回に`provisioning/.state/<environment-id>/`へ生成され、Gitへ追加しない。

### Application/database alignment

現行`backend/`のPostgreSQL URL、pgx、PostgreSQL migrationsをそのまま利用する。release manifestはengine `postgresql`、major version `16`、schema compatibility ID、migration revisionを固定する。preflightはこれらがbackend artifactと一致しないreleaseを拒否し、4 node full readinessを成功扱いしない。

## 3. Static validation

VMを作らず、schema、templates、tool pins、artifact metadataを検証する。

```bash
make image-validate
make vm-preflight
```

Expected:

- host profileが`linux-amd64-virtualbox`として一意に解決される。
- topology/release schema、ISO/artifact checksum、architecture、DB/Router compatibilityがpassする。
- 4 subnet、MAC、SSH port、Client/Portal port、VirtualBox network、host/VPN routeに衝突がない。
- Docker daemonがreadyで、`portal-mock/compose.yaml`の解決済みconfigが有効かつwildcard publishを含まない。
- node別resourceと合計が表示される。
- VM、host-only network、Portal Compose projectをまだ作成していない。

衝突時は提案された別の`/24`をコピーした明示的topology fileへ設定し、次のように再検証する。

```bash
VM_TOPOLOGY=provisioning/config/topology.local.yml make vm-preflight
```

自動的に別CIDRへ切り替わらないことも確認する。

## 4. Image build validation

通常利用者は検証済みboxを取得するため、この節はimage担当者だけがLinux x86_64 hostで実行する。amd64でbuildし、release pathにCPU emulationを使わない。

```bash
make image-build-base
make image-build-role ROLE=problem-server
make image-build-role ROLE=router
```

wrapperはhost architectureをmanifest上の`amd64`へ正規化し、他のarchitectureを拒否する。上記はstable Make interfaceの例であり、provider CLIを直接呼ぶ必要はない。

Expected:

- 公式Ubuntu 24.04 ISOのSHA-256を検証してからAutoinstallする。
- base、problem-server、RouterのPacker output、`packer-manifest.json`、正規化された`image-artifact-index.json`を生成する。
- role box内の`/etc/isucon/release.json`が入力release IDと一致する。
- node hostname、runtime IP、秘密値、初期化済みPostgreSQL cluster/system identifier、SSH host key、machine-idがbakeされていない。
- box filesystem/log scanでsecret findingが0件。

最初のLinux provider spikeでは、amd64 buildから4 VM/Router 5 NIC、Client forward、control host-only IPへのDocker publishまでを3回連続でcreate/reboot/destroyし、結果をrelease validationへ添付する。

## 5. Four-VM startup

default environmentを一つの入口で起動する。

```bash
make vm-up
```

Default behavior:

1. preflightとrole box checksum検証。
2. Vagrantで4 VMをprovisionなしにbootし、Routerのcontrol host-only interfaceを作成。
3. control host IPへのspecific-address Docker publishが利用できることを検証。
4. `isureal-<environment-id>-portal`というCompose projectで`portal-mock/compose.yaml`をexternal-router modeで起動し、API/UI healthを待機。
5. runtime Ansibleを全nodeへ適用し、RouterをPortalへ収束。
6. full conformanceを実行。

local modeは常にrepository内のPortal Compose stackを使用する。control IPへのpublishをengineが拒否する場合も`0.0.0.0`へ広げず、起動を失敗させる。

Expected nodes:

| Node | NICs | Payload |
|---|---:|---|
| server1 | NAT `bootstrap0` + `server1-data` | PostgreSQL 16, backend/worker, frontend, nginx |
| server2 | NAT `bootstrap0` + `server2-data` | PostgreSQL 16, backend/worker, frontend, nginx |
| server3 | NAT `bootstrap0` + `server3-data` | PostgreSQL 16, backend/worker, frontend, nginx |
| router | NAT `bootstrap0` + 3 data + control | isureal-router, Envoy |

Expected network:

| Segment | CIDR | Router | Other endpoint |
|---|---:|---:|---:|
| server1-data | `192.168.60.0/28` | `.5` | server1 `.6` |
| server2-data | `192.168.60.16/28` | `.21` | server2 `.22` |
| server3-data | `192.168.60.32/28` | `.37` | server3 `.38` |
| control | `192.168.60.64/28` | `.69` | host `.65` |

Expected host entry points:

| Purpose | Host bind | Target / caller |
|---|---|---|
| Client application ingress | `127.0.0.1:18080/tcp` | Vagrant forward → Router guest `8080` |
| Portal UI | `127.0.0.1:15174/tcp` | Compose `web:5174` |
| Portal API | `192.168.60.65:18082/tcp` | Compose `server:8080`; Routerだけが`control0`から利用 |
| VM SSH | `127.0.0.1:2201..2204/tcp` | Vagrant管理用forward |

`vm-up`成功は4 VM、Portal Compose、全service、Portal convergence、Client forward、6方向Router-only、management bypass拒否がすべてpassした場合だけである。boxとPortal imageがcache済みのsupported hostでは15分以内を目標とする。

## 6. Status and application access

```bash
make vm-status
```

Expected:

- 4 VM、release ID、host profile、provider versionが表示される。
- PostgreSQL/backend/worker/nginx、Router/EnvoyのhealthとRouter readinessがpass。
- Portal Compose project、source revision、image ID、API/UI publish、healthとdesired/applied convergenceが表示される。
- Client forwardが`127.0.0.1:18080`からRouter `8080`へのexact ruleとして表示される。
- 最新ValidationReport pathと、driftがあれば再実行commandが表示される。

host `/etc/hosts`を変更せずapplication ingressを確認する例:

```bash
curl --fail --show-error \
  -H 'Host: app.team1-local.isureal.test' \
  -H 'X-Country-Code: JP' \
  http://127.0.0.1:18080/
```

既存Router contractはport付きHostを拒否するため、connection先はloopback forward、送信Hostはportなしで分離する。Portal UIはbrowserで`http://127.0.0.1:15174/`から確認できる。host `/etc/hosts`の変更は不要である。

## 7. Full conformance

```bash
make vm-validate
```

Expected checks:

1. exactly 4 registered/running VM、server 2 NIC、Router 5 NIC、bridged/NAT Network/余分なNICなし。
2. MAC→interface role、Ubuntu version/arch、release/runtime manifest一致。
3. all node-local service health、Portal Compose API/UI health、desired/applied convergence。
4. Portal applied stateが返すapp/individual全Hostについて、`127.0.0.1:18080`のClient forwardによるcountry routing。
5. 6 ordered server pairすべてで、`ip route get`のnext hopがlocal Router data IP。
6. 各新規TCP接続でRouterの該当nft counterが増え、server neighbor tableにpeer MACがない。
7. Router停止中は6方向すべての新規接続が失敗し、再起動後に復旧。
8. 各serverから`10.0.2.2:18080`、他VMのhost-forwarded SSH port、Portal API、収集済み非data addressへの接続が拒否。
9. host LAN addressの`18080`/`15174`/`18082`、loopbackの`18082`、control addressの`18080`への接続が拒否される。一方、loopbackの`18080`/`15174`とRouterからcontrol addressの`18082`は成功する。
10. VagrantにClient + SSH以外のforwardがなく、Composeにwildcardまたは予定外のpublishがない。
11. IPv6 global route/forwardingによる迂回がない。

Expected final human output:

```text
local-vm conformance: PASS
```

Machine-readable reportは`provisioning/.state/isureal-local/reports/latest.json`へ残る。reportにsecret、任意のHTTP body、DB rows、非公開課題内容を含めない。

## 8. Idempotence and data preservation

テスト用sentinel dataをPostgreSQLと画像領域へ追加した後、同じdesired stateを2回適用する。

```bash
VM_IDEMPOTENCE_CHECK=1 make vm-provision
make vm-validate
```

Expected:

- second Ansible runのunexpected `changed`は0。
- sentinel dataと画像は維持される。
- unchanged serviceはrestartしない。
- release/runtime manifestは同じrelease IDを保持する。

次に4 VMをrebootして同じ検証を実行する。

```bash
(
  export VAGRANT_DOTFILE_PATH="$(pwd)/provisioning/.state/isureal-local/vagrant"
  cd provisioning/vagrant
  vagrant reload
)
make vm-validate
```

Expected: Portal Composeはhealthyを維持し、RouterはPortalへ再収束して、全serviceとdataが回復する。Vagrant直接commandはdiagnostic/release gate用であり、通常利用入口はMake targetとする。

## 9. Explicit data reset

resetが通常provisionから分離されていることを確認してから、test environmentだけで実行する。

```bash
VM_ENV_ID=isureal-local \
CONFIRM_RESET=isureal-local \
make vm-reset-data
```

Expected:

- 失われるstateとtarget 3 serverを実行前に表示する。
- schema/migration compatibilityを確認し、deterministic initial dataへ戻す。
- VM、network、box、Router configurationは作り直さない。
- reset後にfull conformanceがpassする。

`CONFIRM_RESET`欠落またはenvironment ID不一致なら、DBを変更せずexit code `7`になる。

## 10. Failure diagnostics

検証用release/profileで次を一度ずつ注入する。

- checksum mismatch。
- 1 nodeのruntime template failure。
- PostgreSQL major/schema/migration compatibility mismatch。
- nginx/backend/Router/Envoy停止。
- subnet、MAC、SSH port衝突。
- Client/Portal port衝突、Docker daemon停止、specific-address publish拒否、Portal image/health failure。
- Router停止とPortal Compose interruption。

```bash
make vm-status
make vm-logs
```

Expected:

- failure node、phase、非秘密reason、retry commandを5分以内に特定できる。
- preflight failureはVM side effectを残さない。
- provisioning failureはVM/DBを勝手にdestroy/resetしない。
- secretはhuman/JSON outputとjournal bundleでredactされる。
- 原因修正後に`make vm-provision`または`make vm-validate`で復旧する。

## 11. Scoped destruction

```bash
VM_ENV_ID=isureal-local \
CONFIRM_DESTROY=isureal-local \
make vm-destroy
```

Expected:

- exact Compose projectのcontainer/networkを先に削除し、その後に対象4 VM、environment専用network/stateを削除する。
- 他Compose projectのcontainer/network/volume、他Vagrant project、他VM、他host-only network、global OCI image/box/artifact cacheを保持する。
- 中断/partial failure時は残存resourceを列挙し、同じcommandでcleanupを再試行できる。
- 確認値が欠落・不一致なら何も削除せずexit code `7`。

## 12. Release qualification

同じrelease ID/checksumについて、Linux x86_64/VirtualBox/amd64でSections 3〜11を実行する。release workstationごとにPortal Composeを含む3回連続のcreate/reboot/destroyをpassし、機能ValidationReport、経過時間、flakeを記録する。

```bash
make image-promote \
  ROLE=problem-server \
  ARCH=amd64 \
  ARTIFACT_ID=<validated-artifact-id>
```

roleごとに合格済みamd64 artifactを昇格する。promotionはartifactを再build・変更せず、同じchecksumのmetadataだけを更新する。local host間のscore、latency、throughputを比較または公式値として記録しない。

## 13. Future AMI validation handoff

後続AMI featureは、VirtualBox boxを変換せずCanonical Ubuntu 24.04 amd64 AMIから同じ`ansible/playbooks/image.yml`を実行する。AMI ID、source AMI、region、architectureをPacker manifestへ残し、EC2上でSections 6〜10と同等のguest/service/network conformanceを行う。

AMI公開・copy/share、VPC/subnet/route/SG、source/destination check、official resource profile、benchmark/scoring、trial solvingは、このlocal quickstartがpassした後の別release gateである。
