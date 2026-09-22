# Data Model: ローカルVM競技環境

**Feature**: `004-local-vm-provisioning`

**Date**: 2026-08-06

このfeatureは業務DBを追加しない。ここでのmodelは、build入力、VM topology、runtime convergence、validation、artifact promotionを再現可能にする構成・状態modelである。

## EnvironmentProfile

1つのlocal VM環境を識別するversioned desired state。

Fields:

- `schemaVersion`: 構成schemaの整数version。
- `environmentId`: host上でVM、internal/host-only network、Compose project、state directoryを名前空間化する小文字ID。
- `hostProfile`: 固定値`linux-amd64-virtualbox`。
- `baseCidr`: 既定`192.168.60.0/24`。4つのlogical segmentを含む。
- `sshPorts`: nodeごとのloopback-only forwarded port。
- `clientForward`: host loopback address/port、Router guest port、protocol。
- `domain`: local public hostname suffix。
- `portal`: Compose file、API/UI host port、environment-scoped project derivationを持つPortal設定。
- `resourceProfile`: node roleごとのvCPU、memory MiB、disk GiB。
- `releaseId`: 1つのReleaseManifestを参照。

Validation:

- `environmentId`は`^[a-z][a-z0-9-]{2,31}$`、予約済みglobal名を許可しない。
- host profileとLinux/x86_64/VirtualBoxが一致する。macOS、arm64、別providerはpreflightで拒否する。
- base CIDRと全segmentはhost、VPN、VirtualBox、既存environmentと非重複。
- Client/SSH portは`127.0.0.1`だけへbindし、相互に異なり、使用中でない。Portal UIもloopbackだけ、Portal APIはcontrol host addressだけへbindする。
- secret値を含まない。

Relationships:

- exactly 1 ReleaseManifestを参照する。
- exactly 4 NodeDefinition、4 SegmentDefinition、1 PortForwardDefinition、1 PortalComposeDeploymentを所有する。
- 0または1 ProvisioningRunのactive stateを持つ。

## NodeDefinition

VMの不変な論理identityと期待role。

Fields:

- `nodeId`: `server1`、`server2`、`server3`、`router`のいずれか。
- `role`: `problem-server`または`router`。
- `hostname`: environment内で一意。
- `architecture`: host profileから導出する固定値`amd64`。
- `imageRef`: role、provider、architectureに一致するMachineImageArtifact参照。
- `resources`: vCPU、memory、disk。
- `interfaces`: InterfaceBindingの集合。
- `sshPort`: host loopback forwarded port。

Invariants:

- environmentごとに4 IDがexactly 1件ずつ存在する。
- `server1`〜`server3`は同じproblem-server image checksumを使う。
- Routerは5 interface、各serverは2 interfaceを持つ。
- interface MACはenvironment全体とhost上の既存VMに対して一意。
- node identityはimageへbakeせずRuntimeStateで確定する。

## SegmentDefinition

競技data planeまたはhost接続面を表すlogical L2 segment。

Fields:

- `segmentId`: `server1-data`、`server2-data`、`server3-data`、`control`。
- `kind`: `internal`または`host-only`。
- `cidr`: canonical IPv4 `/28`。
- `virtualizationName`: environment IDを含むprovider resource名。
- `routerAddress`: subnet内のRouter IP。
- `peerAddress`: serverまたはhost IP。
- `allowedBindings`: node/interface roleの集合。

Invariants:

- 4 CIDRは相互非重複でbase CIDR内にある。
- network、broadcast、AWS予約相当の先頭4 IPと末尾1 IPをguest/hostへ割り当てない。
- 各`serverN-data`はRouterと該当serverだけを許可する。
- `control`はRouterとhostだけを許可し、problem serverを許可しない。
- `internal` network名は他environmentのVMから参照されない。

## InterfaceBinding

Node上の論理interfaceとSegmentまたはbootstrap NATの対応。

Fields:

- `role`: `bootstrap0`、`data0`、`data1`、`data2`、`data3`、`control0`。
- `macAddress`: 固定unicast MAC。
- `providerMode`: `nat`、`intnet`、`hostonly`。
- `segmentId`: NAT以外で必須。
- `address`: static IPv4/prefix。bootstrap NATはprovider assigned。
- `routes`: destination CIDRとnext hopの集合。
- `ingressPolicy`: interface/portごとの許可規則。

Invariants:

- MAC→roleは一意で、guest device列挙順からroleを推測しない。
- data interfaceにdefault gatewayを置かない。
- serverのpeer CIDR routeは必ず自segmentのRouter IPをnext hopにする。
- Router bootstrap/controlからdata segmentへ汎用L3 forwardしない。Client forwardはbootstrap上のEnvoy inputだけを例外として許可する。
- IPv6 global addressとforwardingは初期schemaで許可しない。

## PortForwardDefinition

host上のClient入口とRouter guest listenerを結ぶVagrant/VirtualBox NAT規則。

Fields:

- `id`: 固定値`client-ingress`。
- `nodeId`: 固定値`router`。
- `hostAddress`: 固定値`127.0.0.1`。
- `hostPort`: 既定`18080`、1024〜65535のconfigurable TCP port。
- `guestPort`: 固定値`8080`。
- `protocol`: 固定値`tcp`。
- `autoCorrect`: 固定値`false`。

Invariants:

- problem serverをtargetにするapplication/DB forwardを許可しない。
- SSH forward、Portal UI/API portと衝突しない。
- Vagrant/providerの実規則がdesired bindingと完全一致し、余分なforwardがない。
- Client trafficはRouter Envoyで終端し、bootstrap→dataのkernel forwardingを有効にしない。

## PortalComposeDeployment

4 VMとは独立してhost上で動くenvironment-scoped Portal Mock stack。

Fields:

- `projectName`: `isureal-<environmentId>-portal`から決定的に導出。
- `composeFile`: 固定値`portal-mock/compose.yaml`。
- `routerMode`: 固定値`external-router`。
- `teamNumber`: PortalとRouter contractで共有する正のteam番号。
- `teamHash`: Portalのhostname label生成に使うDNS-safe hash。
- `calculatedHostnames`: `app|server1..3.team<teamNumber>-<teamHash>.<domain>`から決定的に導出し、Router/Envoyが受理するHostと一致させる。
- `apiHostAddress`: control SegmentDefinitionのhost address。
- `apiHostPort`: 既定`18082`、container target `8080`。
- `uiHostAddress`: 固定値`127.0.0.1`。
- `uiHostPort`: 既定`15174`、container target `5174`。
- `effectiveConfigHash`: secretを除いたCompose configのhash。
- `serverImageId`, `webImageId`: amd64 build後のimmutable OCI image identity。
- `healthState`: `absent`、`starting`、`healthy`、`unhealthy`。

Invariants:

- project名、container、default network、labelはenvironment IDで名前空間化する。
- API/UI publishに`0.0.0.0`またはhost LAN addressを許可しない。
- Compose envは`.state/<environment-id>/portal.env`にmode `0600`で生成し、Git管理しない。
- Portalのcalculated hostname、Router runtime Host、Client conformance期待値は同じ`teamNumber`、`teamHash`、`domain`から導出する。
- `healthy`だけではenvironmentをreadyにせず、Router desired/applied convergenceも必要。
- destroyは同じproject labelを持つcontainer/networkだけを対象にし、global OCI image cacheと他projectを保持する。

## ReleaseManifest

image buildに必要なrelease入力。`contracts/release-manifest.schema.json`に従う。

Fields:

- `schemaVersion`, `releaseId`。
- `ubuntu`: release、amd64 ISO URL/SHA-256、AWS source AMIのregion/ID/owner。
- `artifacts`: component、architecture、URI、必要なcompatibility ID。開発用local artifactはchecksumを持たず、将来のremote artifactだけSHA-256を必須とする。
- `databaseContract`: engine `postgresql`、major version `16`、schema compatibility ID。

Toolのversionは実際に導入へ使う`mise.toml`、Packer HCL、`requirements.yml`が正本である。Ubuntu packageはroleにpackage名を記述し、24.04 repositoryから取得してmachine imageへ閉じ込める。Git revisionはbuild時にrepositoryから取得して出力artifactへ記録するため、release入力へ重複させない。Router compatibilityはRouter artifact自身が持ち、同じrelease内に比較用の複製を置かない。PortalのCompose path、mode、portはtopologyを正本とする。

Invariants:

- backendとRouterはamd64 artifactを持つ。frontendがarchitecture-neutralの場合は`architecture: any`を明示する。
- remote artifactはchecksumなしURLやsigned temporary URLを許可しない。
- PostgreSQL major version、backend/database-migrations artifactのcompatibility IDが一致しないreleaseはbuild/ready不可。
- secret、access tokenを含まない。
- release IDはimage/artifactの名前空間であり、promoted outputは同じIDで上書きしない。

## MachineImageArtifact

Packerが生成し、test/promotionされるimmutable output。正規化されたindexは`contracts/image-artifact-index.schema.json`に従う。

Fields:

- `artifactId`: provider固有IDまたはcontent-addressed path。
- `releaseId`: 入力ReleaseManifest参照。
- `role`: `base`、`problem-server`、`router`。
- `provider`: `virtualbox`、将来`amazon-ebs`。
- `architecture`: 固定値`amd64`。
- `sourceImageId`: ISO checksumまたはsource AMI ID。
- `checksum`: file artifactではSHA-256。AMIではregion/AMI IDとbuild metadata。
- `buildName`, `buildTime`, `sourceRevision`。
- `validationState`: `unverified`、`validated`、`rejected`、`promoted`。

Invariants:

- `validated`はbuildした同一artifactへtestした場合だけ設定できる。
- `promoted`はLinux x86_64/VirtualBox/amd64の必須ValidationReportがpassしたartifactだけ。
- promoted artifactを同じrelease IDで上書きしない。
- role imageにはnode identity、runtime secret、host credential、初期化済みPostgreSQL cluster/system identifierを含めない。

State transitions:

```text
built(unverified)
  -> static/image validation passed (validated)
  -> required matrix passed (promoted)

any failed gate -> rejected
rejected/promoted -> immutable terminal state
new content -> new artifact/release ID
```

## ArtifactRef

ReleaseManifest内で外部成果物を指すcontent-addressed参照。

Fields:

- `component`: backend、frontend、router、envoy、migration等。
- `architecture`: `amd64`またはarchitecture-neutral artifact用の`any`。
- `uri`: `file://`または認可されたprivate HTTPS/S3 location。
- `sha256`: remote artifactでのみ使用する64桁lowercase hex。local development artifactでは省略する。
- `compatibilityId`: DB、Router、config schema等の検証ID。

Validation:

- host/CIが取得してSHA-256を検証してからguestへ転送する。
- guestがprivate registry credentialを受け取らない。
- amd64に一致するvariantがなければpreflight failure。`any`はarchitecture-neutralと宣言されたartifactだけに許可する。

## RuntimeState

cloneされたNodeへruntime playbookが適用した、非秘密な実状態の記録。

Fields:

- `environmentId`, `nodeId`, `role`, `releaseId`。
- `hostname`, `architecture`。
- interface role、MAC、address、routeの実値。
- control SegmentDefinitionとPortalComposeDeploymentから導出した`portalEndpointHash`または非秘密endpoint。
- `databaseCompatibilityId`, `migrationVersion`。
- `configuredAt`, `configurationRevision`。

Invariants:

- `/etc/isucon/runtime.json`へatomic writeする。
- DB password、VAPID private key、cloud/Portal credentialを含めない。
- `/etc/isucon/release.json`を上書きしない。
- environment/node/release不一致時はserviceをreadyにしない。

## RuntimeSecretSet

host側だけに保存し、runtime taskへ渡すenvironment固有secret。

Fields:

- PostgreSQL application role password。
- cursor HMAC key、VAPID key等のapplication secret。
- 必要なPortal credential reference。

Invariants:

- `.state/<environment-id>/`またはexternal secret storeにあり、Git対象外、mode `0600`。
- image buildの入力にしない。
- Ansible log/diffとValidationReportへ値を出さない。
- rotation時はruntime convergenceを実行し、秘密でないrevision/hashだけを状態へ記録する。

## ProvisioningRun

1回のpreflight/create/configure/validate操作と結果。

Fields:

- `runId`, `environmentId`, `releaseId`。
- `operation`: `preflight`、`up`、`provision`、`validate`、`reset-data`、`destroy`。
- `phase`: `preflight`、`image-resolve`、`vm-create`、`inventory`、`compose`、`network`、`runtime`、`services`、`conformance`、`cleanup`。
- `startedAt`, `completedAt`。
- `nodeOutcomes`: node/phase/changed/result/reason。
- `outcome`: `running`、`passed`、`failed-retryable`、`failed-terminal`。

Transitions:

```text
requested
  -> preflighted
  -> images-resolved
  -> vms-created
  -> portal-healthy
  -> runtime-configured
  -> services-ready
  -> validated

any phase -> failed-retryable -> same operation may resume
schema/tool/artifact incompatibility -> failed-terminal until input changes
destroy requested -> scoped cleanup -> absent or cleanup-failed
```

Invariants:

- 失敗したnodeとphase、非秘密reason、再実行入口を必ず残す。
- 一部nodeだけ成功してもenvironment全体をreadyにしない。
- `provision`はDB resetを呼ばない。
- `destroy`は一致するenvironment IDのVM/network/Compose/state resourceだけを対象にする。

## ValidationReport

同一artifactとenvironmentがcontractを満たす証拠。

Fields:

- `reportId`, `runId`, `environmentId`, `releaseId`。
- host OS/architecture、VirtualBox/Vagrant、Docker Engine/Compose versions。
- nodeごとのOS、arch、release、runtime、service health。
- provider NIC/port-forward model、route、nftables ruleset hash。
- Portal project名、effective config hash、OCI image IDs、API/UI health、desired/applied convergence。
- Client loopback forwardからのHost/country routing results。
- 6 ordered peer path results、Router counters、neighbor observations。
- Router-stop failure/recovery results。
- bootstrap/control、未許可forward、Portal API/UI publish境界のnegative results。
- idempotence changed count、DB sentinel preservation。
- secret/clone hygiene findings。
- `outcome`: `pass`または`fail`。

Invariants:

- secret値、arbitrary HTTP body、private bottleneck内容を含めない。
- 全必須checkがpassした場合だけreport全体をpassにする。
- 複数のLinux x86_64 hostで機能結果は比較できるが、score/latencyを合否比較しない。
- MachineImageArtifact promotionはLinux x86_64/VirtualBox/amd64 qualificationのpass reportを参照する。

## Environment lifecycle

```text
absent
  -> preflight-passed
  -> vms-created
  -> portal-healthy
  -> configured
  -> ready

configured/ready -> reprovisioning -> configured -> validating -> ready
any non-destructive state -> failed-retryable -> reprovisioning
ready -> rebooting -> configured -> validating -> ready
ready -> resetting -> configured -> validating -> ready
any existing state -> destroy-requested -> absent
destroy failure -> cleanup-failed -> destroy-requested
```

`ready`は4 nodeのservice health、Portal health/convergence、Router data-plane readiness、Client forward、network isolation、release/DB compatibilityが同じrunでpassした場合だけ成立する。

## Cross-entity invariants

- `EnvironmentProfile.releaseId = Node.imageRef.releaseId = guest ReleaseManifest.releaseId = RuntimeState.releaseId = ValidationReport.releaseId`。
- `server1`〜`server3`は同一problem-server artifactを使い、node-specific差分はRuntimeStateだけにある。
- topologyのMAC/IP/route/port、Vagrant forward、Portal Compose publish、guest実状態、Router/Envoy config、validation expectationは同じEnvironmentProfileから導出する。
- build input、build output、runtime secret、runtime state、validation resultを同じfileへ混在させない。
- DB compatibilityが満たされない限り、networkだけ正常でもEnvironmentをready/promotedにしない。
