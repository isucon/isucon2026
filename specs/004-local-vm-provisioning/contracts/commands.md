# Command Contract: ローカルVM競技環境

**Feature**: `004-local-vm-provisioning`

**Status**: Design contract; commands are implemented in a later task phase.

## Principles

- Repository rootのMake targetsだけをstable public interfaceとする。
- 利用者はprovider CLI、生成inventory、Packer HCLを直接組み合わせなくてよい。
- 全commandはrepository root以外から呼ばれてもrootを解決し、stateを`provisioning/.state/<environment-id>/`へ閉じる。
- destructive operationは名前で明示し、environment IDを再確認する。通常の`up`/`provision`/`validate`はVMやDBを削除しない。
- logはsecretをredactし、失敗時にnode、phase、再実行commandを返す。
- host `/etc/hosts`、system firewall、他environmentのVM/network/Compose projectを暗黙変更しない。

## Common inputs

| Input | Default | Contract |
|---|---|---|
| `VM_ENV_ID` | `isureal-local` | `^[a-z][a-z0-9-]{2,31}$`; resource namespace |
| `VM_TOPOLOGY` | `provisioning/config/topology.yml` | topology schemaに合格するpath |
| `VM_RELEASE` | `provisioning/config/release.json` | release manifest schemaに合格するpath |
| `VM_PROFILE` | `compact` | topologyで定義されたresource profile |
| `VM_PROVIDER` | `auto` | `auto`または`virtualbox`; other valueは初期versionで拒否 |
| `VM_KEEP_ON_FAILURE` | `0` | `1`なら診断用に起動済みresourceを保持。ただし新規破壊は行わない |
| `VM_OUTPUT` | `human` | `human`または`json`; secretはどちらでもredact |

`auto`はLinux x86_64と利用可能なVirtualBoxを検出して`virtualbox`を選ぶ。未対応host/profileを別providerへ暗黙fallbackしない。

default topologyはClient `127.0.0.1:18080`、Portal UI `127.0.0.1:15174`、Portal API `192.168.60.65:18082`を固定する。変更はschema検証される`VM_TOPOLOGY`で明示し、自動port correctionは行わない。local workflowに外部Portalへの切替入力は設けない。

## Exit codes

| Code | Meaning |
|---:|---|
| `0` | requested operation and all mandatory checks passed |
| `2` | input/schema/tool/host preflight failure; VM side effectなし |
| `3` | artifact resolution, checksum, architecture, compatibility failure |
| `4` | VM/provider/network lifecycle failure |
| `5` | Ansible runtime convergence or service readiness failure |
| `6` | conformance failure after services became inspectable |

## `make vm-preflight`

Purpose: VMを作成・変更せず、実行可能性を判定する。

Must check:

- Linux x86_64、Packer、Vagrant、VirtualBox、ansible-playbook、Docker Engine/Composeコマンド、Docker daemon、hardware virtualization。host toolのversionはpreflightで完全一致検査せず、Python/ansible-core/Packerは`mise.toml`で固定する。Docker/Compose互換性はresolved Compose configと必要なpublish機能を実動作で検査する。
- topology/release schema、role/provider/architecture artifact variant、DB compatibility、ローカルartifactの存在。開発用のlocal build artifactはchecksumを照合しない。将来remote artifactを取得する場合だけrelease manifestのSHA-256を検証する。
- 4 CIDRの相互関係とhost/VPN/policy route、VirtualBox host-only/NAT/DHCP、既存VMとの衝突。
- environment-namespaced internal network名、MAC、`127.0.0.1:2201..2204`、Client/Portal portの衝突。
- host-only CIDRがVirtualBox許可範囲にあること。
- `portal-mock/compose.yaml`のresolved config、external-router mode、environment-scoped project名、API/UIのspecific-address publish。`0.0.0.0`または予定外publishは拒否する。
- disk/memoryの最低条件。過不足は数値とprofile変更方法を示す。

Output: resolved host profile、release ID、artifact checksums、node resource totals、network table、Client forward、Portal project/publish、pass/fail reasons。状態を変更してはならない。

## `make vm-up`

Purpose: 検証済みrole boxesから4 VMを作り、runtime convergenceとfull validationまで行うprimary entry point。

Sequence:

1. `vm-preflight`。
2. content-addressed cache/private registryからexact boxesをresolveしてchecksum検証。
3. Vagrant `up --no-provision`でexactly 4 VMを作成し、Router control host-only interfaceを成立させる。
4. control host IPへのspecific-address publishを実動作で検査する。非対応時は`0.0.0.0`へfallbackせず失敗する。
5. `.state/<id>/portal.env`をmode `0600`で生成し、`isureal-<id>-portal` projectとしてPortal Compose stackをbuild/startしてAPI/UI healthを待つ。
6. SSH inventoryを生成し、Portal API endpointを含むruntime playbookを全nodeへ1回適用する。
7. `vm-validate`を実行。

Success: Portal Compose、4 node、全serviceがreadyで、Client forwardを含む全conformanceがpassした時だけexit `0`。partial readyをsuccessにしない。

Failure: 既に作成したVMやCompose diagnosticsを暗黙destroyしない。node/phase/log pathと`make vm-provision`または`make vm-validate`の再実行方法を表示する。`VM_KEEP_ON_FAILURE=0`でも作成済みVMとDBを削除しない。

## `make vm-provision`

Purpose: 既存4 VMへruntime desired stateを冪等に再適用する。

Preconditions: environmentの4 VM、release ID、provider identityが一致する。欠損VMがある場合は勝手に再作成せず`vm-up`を案内する。

Behavior:

- environment-scoped Portal Compose desired stateを再適用し、API/UI healthを確認してからRouterを収束させる。
- default synced folderを使用しない。
- DB drop/reset/seedを通常実行しない。empty DBのinitial migrationだけを一度行う。
- 変更されたtemplate/artifactに対応するhandlerだけを起動する。
- 成功後に`vm-validate`を実行する。
- `VM_IDEMPOTENCE_CHECK=1`では同じplaybookを2回実行し、2回目のunexpected `changed`が1件でもあればexit `5`。

## `make vm-validate`

Purpose: environmentを変更せず、Linux x86_64上で同じconformanceを実行する。ただしRouter-stop試験だけはRouterを一時停止し、必ず復旧する明示的なtest phaseとして扱う。

Required checks:

- exactly 4 VM、expected NIC count/mode/name、余分なbridged/NAT Network NICなし。
- guest OS/architecture/release/runtime manifestsとnode identity。
- PostgreSQL/backend/worker/nginx、Router/Envoy、Portal Compose API/UI、desired/applied convergence、health/readiness。
- `127.0.0.1:18080`からHost/country付きrequestが期待serverへ届き、Vagrant Client forwardがRouter guest `8080`へのexact TCP ruleである。
- peer 6方向のroute、new TCP connection、Router nft counter、neighbor table。
- Router停止時の6方向failure、再起動後のrecovery。
- problem serverから`10.0.2.2:18080`、他VMのforwarded SSH port、Portal API、収集済み非data addressへのnegative connection。
- Client/Portal publishがloopback/controlへ限定され、host LAN address、cross-plane address、wildcard publishでは到達不能であること。
- Client + SSH以外のVagrant forwardと、予定外のCompose publishが存在しないこと。
- IPv6 global route/forwardingによるbypassがない。
- secretを含まないValidationReportを`.state/<id>/reports/`へatomic write。

Router-stop phaseは既存keep-alive/conntrackを閉じ、各方向で新規connection IDを使う。失敗またはinterruptでもRouterを元のstateへ戻すtrapを持つ。

## `make vm-status`

Purpose: read-onlyで現在状態と次の行動を表示する。

Output:

- environment/release/provider/host profile。
- 4 VMのprovider state、SSH到達性、runtime revision。
- service health/readinessと最後のvalidation report。
- Portal Compose project、source revision、effective config hash、OCI image IDs、API/UI publishとhealth/convergence。
- Client/SSHのactual forward rules。
- drift、partial resource、cleanup failureがあれば具体的に列挙。

Missing/partial stateを自動修復しない。

## `make vm-logs`

Purpose: environment-scoped diagnosticsを収集する。

Includes: Vagrant/provider metadata、Ansible recap、systemd status/journalのbounded tail、Router status、route/nft summary、environment-scoped `docker compose logs`のbounded tail。arbitrary HTTP body、secret vars、full PostgreSQL rows、SSH/cloud credentialsを収集しない。

## `make vm-reset-data`

Purpose: 3 problem serversのPostgreSQL schema/dataとapplication image dataを既知の初期状態へ戻す。

Behavior:

- target 4 VMとrelease compatibilityを再確認。
- reset前に対象と失われるstateを表示。
- migration + deterministic seedを各serverへ適用。
- Router/topology/boxを変更しない。
- failureをpartial successとして隠さず、node別結果を残す。

`vm-up`、`vm-provision`、`vm-validate`から暗黙に呼ばない。

## `make vm-destroy`

Purpose: 指定environmentのPortal Compose project、VM、environment専用host-only/internal network state、generated inventory/runtime secretを削除する。

Default targetはtopologyの`environmentId`。別environmentを対象にする場合だけ`VM_ENV_ID=<id>`で上書きする。

Safety:

- project名とCompose labelがともに一致するPortal container/networkを最初に`down --remove-orphans`し、global prune、image削除、`--volumes`を実行しない。
- provider metadataとresource namespaceの双方が一致する対象だけを解決する。
- wildcard、全VM、全host-only network、他Compose project、global OCI image/Vagrant box cacheを対象にしない。
- downloaded content-addressed boxes/artifactsはcacheとして保持し、別の`cache-prune` contractなしに削除しない。
- partial cleanupは残存resourceを列挙してexit `4`。

## Image commands

### `make image-validate`

Packer HCL、plugin pins、Autoinstall schema、Ansible、release manifestをstatic validationする。開発用local artifactのchecksum contractは持たない。hypervisor side effectなし。

### `make image-build-base [ARCH=amd64]`

公式Ubuntu amd64 ISO + SHA-256からVirtualBox base boxを生成する。`ARCH`省略時は`amd64`とし、他の値はrelease buildで拒否する。`ROLE`を受け付けない。outputはcontent-addressed pathとPacker manifest。既定では既存boxを上書きしないが、`FORCE=1`でローカル再buildできる（`promoted` artifactは不可）。

### `make image-build-role ROLE=<problem-server|router> [ARCH=amd64]`

exact amd64 base boxからAnsible `image.yml`を適用し、role boxを生成する。`ARCH`の省略・固定値制約はbase buildと同じ。build時secretを受け付けず、capture cleanup、image smoke、SHA-256を必須とする。既定では既存boxを上書きしないが、`FORCE=1`でローカル再buildできる（`promoted` artifactは不可）。

### `make image-promote ROLE=... ARCH=... ARTIFACT_ID=...`

required ValidationReportを照合し、同じartifact checksumのmetadataだけをprivate registryへ昇格する。初期`file://` modeは`image-artifact-index.schema.json`に合うpromotion indexをlocalに生成する。artifactの再buildや内容変更を行わない。

amd64 AMI build/publish commandは今回実装しない。後続featureが同じrelease/image/conformance contractへ追加する。

## Hostname access

default hostnamesはPortal topology contractの`teamNumber=1`、`teamHash=local`、`domain=127.0.0.1.nip.io`から導出する`app.team1-local.127.0.0.1.nip.io`と`server1..3.team1-local.127.0.0.1.nip.io`。environment IDはresource namespacingだけに使い、公開hostnameの正本にしない。commandsはhost `/etc/hosts`を自動変更しない。

ValidationはPortal applied stateが返したapp/server1〜3の全hostnameについてClient forward経由でHTTP requestを実行する。Routerのvirtual-host照合はHostのportを無視するため、default appは`http://app.team1-local.127.0.0.1.nip.io:<client-port>/`としてbrowser/curlから直接利用できる。Portal UIは`http://127.0.0.1:<portal-ui-port>/`でbrowserから利用できる。

## JSON output envelope

`VM_OUTPUT=json`は次のstable envelopeを使う。

```json
{
  "schemaVersion": 1,
  "command": "vm-validate",
  "environmentId": "isureal-local",
  "releaseId": "r2026.08.05.1",
  "outcome": "pass",
  "phase": "conformance",
  "message": "all mandatory checks passed",
  "reportPath": "provisioning/.state/isureal-local/reports/latest.json"
}
```

Failure時も同じenvelopeで`outcome: fail`、非秘密な`reasonCode`、`nodeId`、`retryCommand`を追加する。stdoutはJSON 1 document、diagnostic progressはstderrへ出す。
