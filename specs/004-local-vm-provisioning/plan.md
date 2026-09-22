# Implementation Plan: ローカルVM競技環境

**Branch**: `004-local-vm-provisioning` | **Date**: 2026-08-06 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/004-local-vm-provisioning/spec.md`

## Summary

Linux x86_64で、Vagrant + VirtualBoxからUbuntu 24.04 amd64の4 VMを起動する。problem-server 3台はそれぞれ専用internal networkへ置き、Router VMは3 data segmentとhost-only controlへ接続する5 NIC構成とする。Client ingressはRouterのNAT adapterへ`127.0.0.1:18080`からguest `8080`への固定Vagrant port forwardで渡し、専用ingress NICは作らない。Packerは公式Ubuntu amd64 ISOから単一のbase boxを作り、共通Ansible rolesからproblem-server/Routerのrole boxを生成する。通常の`vm-up`は検証済みboxを利用し、薄いruntime playbookだけでnode identity、network、秘密値、初回DB構成を収束させる。

問題VMにはPostgreSQL 16、backend、frontend、nginxを、Router VMには既存`003-router`仕様のRouter + Envoyをnative systemd serviceとして配備する。Portalは既存`portal-mock/compose.yaml`をenvironment専用Docker Compose projectとしてexternal-Router modeで起動し、APIはcontrol host-only addressだけ、UIはhost loopbackだけへpublishする。入力release manifest、生成artifact manifest、guest runtime stateを分け、同じAnsible image playbookを将来の`amazon-ebs` buildへ渡す。現行backendのPostgreSQL URL、pgx、migrationをそのままversioned application contractとして利用し、DB major version・schema・migrationの不一致をready扱いしない。

## Technical Context

**Language/Version**: Packer HCL2（mise: Packer 1.15.4）、Ansible YAML/Jinja（mise: Python 3.12.13 + ansible-core 2.21.2）、Vagrant Ruby DSL、Docker Compose YAML、POSIX shell + Make、JSON Schema 2020-12

**Primary Dependencies**: VirtualBox、Docker Engine + Docker Compose、Packer plugins `virtualbox=1.1.5` / `vagrant=1.1.7` / `ansible=1.1.5`、`community.postgresql=4.2.0`、Ubuntu Subiquity Autoinstall、Ubuntu Snapshot Service、systemd、netplan、nftables、iproute2、PostgreSQL 16 + `python3-psycopg`（Ubuntu Noble snapshot固定）、nginx、Envoy 1.32.2、既存Go 1.26.5 / Node 24.14.1 Portal artifacts

**Storage**: hostのcontent-addressed box/artifact/OCI image cache、environment-scoped Compose/Vagrant state、将来のversioned private S3 artifact registry。guestはPostgreSQL cluster data directory、画像領域、release/runtime manifest、journald。通常reprovisionはguest永続データを保持し、reset/destroyだけが明示的に変更・削除する。Portal Mockは永続volumeを持たない。

**Testing**: `packer fmt/init/validate`、Autoinstall schema、`vagrant validate`、`docker compose config --quiet`、Ansible syntax/lint/role argument specs、JSON Schema validation、2回目`changed=0` idempotence、box boot smoke、Portal health/convergence、Client port-forward smoke、4-node conformance、secret/clone-hygiene scan。full matrixはLinux x86_64のself-hosted環境または明示的release workstationで実行する。

**Target Platform**: hostはLinux x86_64。guestはUbuntu Server 24.04 amd64。将来の出力先はCanonical Ubuntu 24.04 amd64 AMIをsourceにするAWS EC2。macOSとlocal arm64は本featureの対象外。

**Project Type**: Infrastructure as Code / image build / multi-VM local development environment

**Performance Goals**: base/role box取得済みなら`vm-up`から4台readyまで15分以内。Quickstartによるcreate/validate/status/destroyの初回一巡30分以内。ローカルhost間のscore、latency、throughputは比較・公式較正に使用しない。

**Constraints**: exactly 4 VM、Router 5 NIC、3 server segmentの相互L2分離、全6方向Router経由、Routerの明示Client forward以外はbootstrap経由のcompetition/control到達禁止、Client/Portal/SSH portのloopbackまたはcontrol限定bind、port auto-correction禁止、IPv4-only初期increment、Ubuntu 24.04 amd64、秘密や初期化済みDB clusterをimageへbakeしない、Linux x86_64上のnative build、hosted CIでnested virtualizationを前提にしない。

**Scale/Scope**: 2 guest roles × 1 architecture、4 runtime nodes、3 internal data segments + 1 host-only control + per-VM NAT、1 environment-scoped Portal Compose stack、1 Client forward、2 local role boxes + 1 base box。AMI生成・登録、AWS network IaC、公式benchmark、競技者account/source distributionは後続feature。

## Constitution Check

*GATE: Phase 0前に評価し、Phase 1後に再評価する。*

- **Specification stage — PASS**: Constitution 2.0.0の「競技仕様」段階に属する環境サブ仕様である。今回の成果物はlocal VM topology、guest image contract、reproducible provisioning、conformance、版manifest、AMI handoffであり、AMI公開と競技配布は後続段階へ渡す。
- **Competition correctness — PASS**: `specs/001-isureal-social-app`の公開契約・データ不変条件と`specs/003-router`のRouter-only、Host/country、last-known-goodを固定参照する。同じapplication/Router contract testを初期・最適化後artifactへ適用する。DB compatibility ID、migration、service health、6方向route、管理経路negative testをready gateに含める。
- **Stage-appropriate quality boundary — PASS**: runtime入力をschema検証し、serviceはleast privilegeで実行する。資格情報はexternal input、`0600`、redacted logsとし、image/box/logへsecret scanを行う。host rootへの防御、WAF、TLS、HA、長期log/metric retention、cloud key managementはlocal作問環境の対象外として後続deploymentへ明示委譲する。
- **Independently tunable bottlenecks — PASS**: provisioning自身は意図的な性能問題を導入しない。非公開課題はversioned application artifactとして受け取り、Ansible roleやbase imageへ不可分に埋め込まない。問題ID、強度、想定解はprivate ledgerだけで管理する。
- **Private registry — PASS**: 初期はcontent-addressed local `file://` cache、共有時はversioningを有効にしたprivate S3 bucketをartifact contractとする。短期資格情報はhost/CIだけが持ち、guestやmanifestへ保存しない。公開artifactにはbox/AMIやspoilerを含めない。
- **Workload/scoring — PASS**: 本サブ仕様はload、scoring、初期/目標score帯を定義しない。後続競技仕様がrelease ID、VM resource profile、topology、data、clock/randomnessを固定して定義する。local host上の数値を公式較正に使わないことをcontract化する。
- **Reproducibility and calibration checkpoints — PASS**: ISO/AMI source、remote artifact digest、arch、output IDをmanifest化する。tool/pluginは実際に導入へ使う設定、Ubuntu packageはbuilt imageのID/checksumで固定し、static → image convergence → 4-node local conformance → future EC2 conformance → baseline/solution calibrationの順で同一artifactを昇格する。
- **Trial solving — PASS**: local VMは作問者の機能検証・相対比較に利用可能とする。競技者同等情報、time box、resource、完了条件による正式なtrial solvingは後続競技仕様がversion-pinned imageを用いて実施する。
- **Quality and information separation — PASS**: public contracts、generic roles、local workflowと、private application artifact、課題台帳、想定解、score結果を分離する。unit/static、image、service、network E2E、migration/idempotence、secret/distribution auditを別gateとして記録する。

### Post-design re-check

Phase 1設計後も全gateはPASSである。`data-model.md`はrelease/runtime/build/Compose状態を分離し、`contracts/`はcommand、topology、release manifestの公開境界を固定した。`quickstart.md`は同じartifact、Portal Compose、Client forwardに対するLinux x86_64検証を定義する。既存backendとPostgreSQL 16の契約を整合させ、AMI/AWS配線、benchmark/scoring、trial-solvingを名前付きhandoffにしたため、現在段階の未実装を黙示的な例外にしていない。Constitution違反はない。

## Architecture

### 1. Sources of truth

```text
topology.yml ──> Vagrant NICs/forwards ──> runtime inventory ──> netplan/nft/routes
      │                                               └────────> conformance expectations
      ├──> generated Portal Compose env/project ────────────────> Portal endpoint
      └────────────────────────────────────────────────────────> Router/Envoy config

release.json ──> Packer vars ──> Ansible image roles ──> box/AMI
      │                 │                 └───────────> /etc/isucon/release.json
      │                 └──> packer-manifest.json ───> image-artifact-index.json
      └───────────────────────────────────────────────> artifact checksum checks
```

- `topology.yml`は秘密を持たない論理network、host port、Portal publish、resource profileの正本である。Vagrantfile、Portal Compose wrapper、Ansible template、validatorが直接読み、IP/MAC/segment/portを重複定義しない。
- `release.json`はversion管理するbuild入力である。Packer HCLはwrapperが生成するJSON var fileを受け取るが、値の独自な正本を持たない。Packer固有manifestは正規化した`image-artifact-index.json`へ変換し、`vm-up`はexact release/role/provider/archのpromoted recordだけを解決する。
- `.state/<environment-id>/`はVagrant SSH inventory、runtime secret、生成済みPortal env、Compose project metadata、validation reportなどの生成物を持ち、Git管理しない。

### 2. Image pipeline

```text
Ubuntu 24.04 ISO + checksum
        │  virtualbox-iso + Autoinstall
        ▼
VirtualBox/amd64 base box
        │  Packer vagrant builder + ansible/image.yml
        ├───────────────────────┐
        ▼                       ▼
problem-server role box     router role box
        │                       │
        └──── Vagrant 3 copies ─┴──── Vagrant 1 copy

Canonical Ubuntu AMI (future)
        │  amazon-ebs + the same ansible/image.yml
        ├── problem-server AMI
        └── router AMI
```

Base boxはPython、SSH、Guest Additions、時刻同期、最小OSだけを持つ。role boxは固定package/artifactとservice skeletonを持つ。PostgreSQL packageはbakeするがclusterは作成せず、capture前にmachine identity、SSH host key、cloud-init state、logs、temporary credential、初期化済みPostgreSQL cluster dataを除去する。Packer build hostの秘密やrepository checkoutをguestへmountしない。

通常の開発者はprivate registry/local cacheの検証済みrole boxを使い、Packerを毎回実行しない。image担当者だけが`image-build-base`または`image-build-role`を明示する。

### 3. Runtime convergence

`vm-up`はpreflight後に4 VMを`--no-provision`で起動してcontrol host-only interfaceを確立し、`vagrant ssh-config`から環境専用inventoryを生成する。次にPortal Composeを起動してhealthを待ち、1回の`runtime.yml`を全nodeへ適用する。

1. release/architecture/DB compatibilityとDocker Compose configをside effect前に検証する。
2. Vagrantが4 VMとprovider NICを作成し、host側control addressが成立したことを確認する。
3. specific-address publishを検査し、environment専用project名と生成envからPortal Composeをexternal-Router modeで起動してAPI/UI healthを待つ。
4. `runtime.yml`でhostname、MAC→interface role、netplan、route、nftablesを全nodeに設定する。
5. Routerからcontrol host address上のPortal APIへ到達できることを確認する。
6. problem serverごとにPostgreSQL 16 cluster、system identifier、application role/database/credentialを初回だけ生成する。
7. 初回empty DBだけmigration/seedを行い、通常reprovisionではresetしない。
8. backend、worker、nginx、Router、Envoyを依存順で起動する。
9. Portal convergence、node-local health、Router readiness、Client forward、6方向route/isolationを検証する。

runtime playbookは同じ入力で2回目`changed=0`を満たし、変更されたtemplateに関係するhandlerだけを実行する。DB resetは`vm-reset-data`という別の確認付き入口だけで行う。

### 4. Network topology

```text
Client 127.0.0.1:18080
       │ Vagrant TCP forward -> router guest :8080 (Envoy)
       ▼
per-VM NAT ─ bootstrap0 ──────────────────────── [router]
                                               data1│ 192.168.60.5
                           server1 data0 .6 ─────────┘
                                               data2│ 192.168.60.21
                           server2 data0 .22 ────────┘
                                               data3│ 192.168.60.37
                           server3 data0 .38 ────────┘
                                                   │control0 192.168.60.69
Portal Compose API 192.168.60.65:18082 ─ control /28
Portal Compose UI  127.0.0.1:15174

each server: bootstrap0 (its own NAT) + data0 (one internal network)
router: bootstrap0 + data1..3 + control0
```

各data segmentはenvironment ID付きVirtualBox internal networkとする。controlだけをhost-onlyとし、problem serverを接続しない。各serverはpeer 2 CIDRを自身のRouter IPへrouteする。Routerはdata pairだけをkernel forwardし、controlはRouterとPortal API間で終端する。Client trafficはRouter bootstrapの明示port forwardからEnvoyへ入り、Envoyがdata addressのnginxへ新規接続する。bootstrapからdata/controlへの汎用L3 forwardは許可しない。IPv6は初期incrementで無効化する。

default CIDRは`192.168.60.0/24`内の4 `/28`で、controlは`192.168.60.64/28`を維持する。preflightがhost route、VPN policy route、VirtualBox host-only/NAT/DHCP、Docker network、既存VM、MAC、Client/Portal/SSH portと衝突を検査する。衝突時は候補を表示して明示overrideを要求し、自動的に別CIDRやportを選ばない。

### 5. Problem-server service layout

```text
data0:8080
    nginx
    ├── /api/* ──> 127.0.0.1:8081 (isureal-api)
    └── /*     ──> /opt/isucon/frontend/<release>/

isureal-api / isureal-worker / image-sanitizer
    └──> local PostgreSQL 16 + /var/lib/isucon/images
```

- native Ubuntu package/systemdを使い、guest内Composeを追加しない。
- nginxを既存Envoy clusterのport `8080`へ合わせ、backendはloopbackへ移す。
- PostgreSQLはloopbackと自身のdata addressだけで受け、`pg_hba.conf`はapplication roleとcompetition data CIDRへ限定し、bootstrap addressでは受けない。初期backendは`postgres://` URLでlocal DBを使うが、peer DBへの競技上の経路はRouter経由で到達可能にする。
- backend release bundleはAPI、worker、image-sanitizer、migrate/reset/seed、frontend、schema compatibilityをcontent-addressed artifactとして提供する。
- 現行backendのPostgreSQL URL、pgx、migrationを利用し、PostgreSQL major version・backend/database-migrations artifactのschema compatibility ID不一致はpreflight/readinessで拒否する。

### 6. Router、Portal Compose、Client entry layout

- `isureal-router.service`: dedicated user + `CAP_NET_ADMIN`、network準備後に起動。
- `envoy.service`: unprivileged user、既存xDS node/resource名とEnvoy 1.32.2を固定。
- generated bootstrap: upstream server1〜3のdata IP:`8080`、xDS loopback:`18000`、admin loopback:`9901`。
- Client: Vagrant `forwarded_port`を`host_ip=127.0.0.1`、host `18080`、guest `8080`、TCP、`auto_correct=false`でRouterだけに設定する。Envoy application listener以外のguest portやproblem serverのapplication/DB portはforwardしない。
- Router input firewall: `bootstrap0:8080`はEnvoy終端だけを許可し、bootstrapからdata/controlへのkernel forwardは拒否する。Router `/healthz`と`/readyz`は`control0`だけに公開し、host port forwardしない。
- Portal: `portal-mock/compose.yaml`を`--project-name isureal-<environment-id>-portal`、`PORTAL_ROUTER_MODE=external-router`、生成済みenvironment envで起動する。server/webのDockerfile inputとbase image digest、生成image IDをrelease/validationへ記録する。
- Portal publish: APIはcontrol host address `192.168.60.65:18082`からcontainer `8080`へ、UIは`127.0.0.1:15174`からcontainer `5174`へpublishする。既存Compose fileに`PORTAL_SERVER_BIND_IP`と`PORTAL_WEB_BIND_IP`を追加し、未指定時もloopbackを既定にする。`0.0.0.0` publishはpreflightで拒否する。
- Router endpoint: `http://192.168.60.65:18082`をruntime configへ渡し、Router→Portal stream/reportとdesired/applied convergenceをready gateにする。local profileで外部Portalへの切替は提供しない。
- Lifecycle: `vm-status`/`vm-logs`は同じCompose projectをread-onlyで参照し、`vm-destroy`はそのprojectだけへ`down --remove-orphans`を実行する。global image cacheや他projectのcontainer/network/volumeは削除しない。
- TLS、WAF、public DNS、Portal本番認証は後続deploymentの責務とする。

### 7. Artifact and promotion flow

1. `release.json`変更をreviewし、ISO/AMI owner/checksum、app/Router artifact参照、DB compatibilityを確認する。開発用local artifactにchecksumは設定せず、remote artifactだけdigestを必須とする。
2. Linux x86_64上でapp bundleとamd64 base/role imageをbuildする。Packer imageについては`packer-manifest.json`とSHA-256を生成する。
3. built artifactそのものをbootし、Portal OCI imageをamd64 buildしてimage IDを記録し、idempotence、clone hygiene、secret scan、Portal/Clientを含む4-node conformanceを実行する。
4. 合格済みchecksumだけをprivate registry metadataへ昇格する。同じrelease IDの再build/上書きを禁止する。
5. snapshot/security/tool更新は新release IDのPRとして全matrixを再実行する。

### 8. Future AMI handoff

今回作成する`amazon-ebs` source boundaryはdisabled template/contractまでとし、AWS credential、region build、AMI registration、copy/shareを実行しない。後続incrementは次を追加する。

- Canonical owner/SSMから候補を解決してreview済み具体AMI IDをmanifestへ固定。
- problem/Routerのamd64 role別AMIを同じ`image.yml`でbuild。
- serverごとの`/28` subnet、Router transit、ingress、control、route table、SGをcloud IaCで作成。
- Router source/destination check無効、ENI/AZ/instance limitのpreflight。
- EC2上でlocalと同じguest/service/network conformanceを実行してからbaseline calibrationへ渡す。

## Project Structure

### Documentation (this feature)

```text
specs/004-local-vm-provisioning/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── checklists/
│   └── requirements.md
├── contracts/
│   ├── commands.md
│   ├── image-artifact-index.schema.json
│   ├── topology.schema.json
│   └── release-manifest.schema.json
└── tasks.md                       # /speckit-tasksで後続生成
```

### Source Code (repository root)

```text
provisioning/
├── README.md
├── ansible/
│   ├── ansible.cfg
│   ├── requirements.yml
│   ├── playbooks/
│   │   ├── image.yml
│   │   ├── runtime.yml
│   │   ├── validate.yml
│   │   └── reset-data.yml
│   └── roles/
│       ├── common/
│       ├── problem_server/
│       ├── router/
│       ├── runtime_network/
│       ├── image_cleanup/
│       └── conformance/
├── packer/
│   ├── plugins.pkr.hcl
│   ├── variables.pkr.hcl
│   ├── local-base/
│   │   ├── virtualbox.pkr.hcl
│   │   └── http/
│   │       ├── user-data
│   │       └── meta-data
│   └── role-images/
│       ├── vagrant-sources.pkr.hcl
│       ├── amazon-sources.pkr.hcl # disabled future boundary
│       └── builds.pkr.hcl
├── vagrant/
│   └── Vagrantfile
├── config/
│   ├── topology.yml
│   ├── release.json
│   └── runtime.env.example
├── scripts/
│   ├── preflight.sh
│   ├── inventory.sh
│   ├── portal-compose.sh
│   ├── manifest.sh
│   └── validate.sh
├── tests/
│   ├── static/
│   └── conformance/
└── .state/                        # ignored generated state

backend/                           # PostgreSQL-compatible application source/artifact
frontend/                          # architecture-neutral static artifact
router/                            # router binary + Envoy contract source
portal-mock/                       # environment-scoped Compose stack; never baked into role image
├── compose.yaml                  # bind IPをparameterizeして再利用
├── server/Dockerfile
└── web/Dockerfile
Makefile                           # stable user-facing vm/image targets
mise.toml                          # Packer/Python tool pins where supported
.github/workflows/
└── provisioning-ci.yml           # static + optional self-hosted matrix
```

**Structure Decision**: `provisioning/`へimage、configuration、local lifecycle、validationを集約し、既存`deploy/`のapplication Docker Compose stackとは分離する。ただしPortalだけは既存`portal-mock/compose.yaml`をenvironment-scoped projectとして再利用する。Ansible rolesを中心に置き、Packer/Vagrant/AWSはadapterとする。root Makefileだけを安定した利用入口にし、利用者がHCL、inventory、Compose、provider CLIを直接組み合わせなくてもよい構造にする。

## Delivery Increments and Gates

1. **Linux provider spike**: exact tool pins、amd64 base box、4 VM/Router 5 NIC、loopback Client forward、control IPへのDocker port publishをLinux x86_64 release workstationで検証する。
2. **Contracts and preflight**: topology/release schema、Make command contract、CIDR/NIC/Client・Portal・SSH port/Docker・artifact検査、Vagrant/Compose state namespacingを実装する。
3. **Shared image roles**: common/cleanup、problem-server、Router artifact/service、secret-free capture、2段階Packer buildを実装する。PostgreSQL packageをbakeし、未初期化clusterであることを検査する。
4. **Runtime topology**: Vagrant lifecycle、inventory、runtime network、Portal Compose wrapper、Client forward、node identity、initial DB lifecycleを実装する。
5. **Conformance and promotion**: static、Compose health/convergence、Client forward、idempotence、service、6-direction network、Router-stop、data preservation、secret scan、manifest promotionを実装する。
6. **Linux release qualification**: Linux x86_64/amd64で同じreleaseを検証し、Quickstart時間目標と3回連続recreateを記録する。
7. **Downstream handoff**: AMI/AWS featureへshared roles、manifest、logical topology、PostgreSQL runtime lifecycle、conformanceを渡す。

各incrementは前段のartifact/contractを固定してから進む。Linux provider spikeをfull readinessの最初のhard gateとする。

## Testing Strategy

- **Static CI**: HCL/YAML/JSON formatting、schema、Packer/Vagrant/Ansible validation、`docker compose config --quiet`、Portal bind/default config、template rendering、role argument specs、generated inventory snapshot。hosted `ubuntu-24.04`でVirtualBox起動は要求しない。
- **Base image**: ISO checksum、Autoinstall schema、SSH、Guest Additions、OS/arch、machine-id/host-key cleanup、no secret。
- **Role image**: required package/artifact/release manifest、service disabled until runtime config、未初期化PostgreSQL cluster、Router/Envoy config validation。
- **Runtime/idempotence**: initial apply、second apply `changed=0`、targeted handler、failure retry、reboot recovery、sentinel DB row preservation、explicit reset target only。
- **Service integration**: PostgreSQL 16 compatibility/migration、backend/worker、frontend/nginx、Router/Envoy xDS、Compose API/UI health、Portal desired/applied、health/readiness separation。
- **Network E2E**: provider NIC/forward model、MAC mapping、route、nft ruleset hash、Client loopback forwardでのHost/country routing、6 ordered peer connectionsとRouter counter、peer MAC absence、Router停止失敗/復旧、未許可forward・bootstrap L3 bypass拒否、Portal API/UI bind境界、control/application cross-exposure拒否、IPv6 bypass不在。
- **Matrix**: Linux x86_64/VirtualBox/amd64。性能値はhost間比較や公式較正に使わず、機能判定だけを共通化する。
- **Distribution audit**: box content、Packer/Ansible/autoinstall logs、manifest、Git tracked filesのsecret/spoiler scan。合格artifact ID/checksumを再buildせずpromotionする。

## Risks and Explicit Handoffs

- **PostgreSQL clone hygiene**: package install時の自動cluster作成を抑止し、role imageにcluster system identifierや初期dataを残さない。runtime初回収束と再provision時の保存を別testにする。
- **Docker/VirtualBox coexistence**: LinuxでDocker EngineとVirtualBoxを同時稼働し、control host-only IPへのpublishが機能することをprovider spikeのhard gateにする。Compose engineが`host_ip`を拒否する場合は成功扱いせず、公開範囲を`0.0.0.0`へ広げるfallbackは行わない。
- **Resource pressure**: 4 VMとPortal 2 containerはLinux workstationへ負荷をかける。既定compact profileと上書き可能なresource profileを持つが、公式競技資源値とは分離する。
- **CIDR/port/VPN collision**: 自動回避せずpreflight failure + explicit overrideにする。異なるCIDR/portで同じconformanceを通す。
- **APT snapshot staleness**: reproducibilityとsecurityのtradeoffを、新snapshot release PR + full revalidationで管理する。
- **Host trust**: malicious host rootによるVirtualBox配線変更は防御範囲外。構成driftとguest起点bypassは検出する。
- **AWS topology difference**: local direct-attached Router NICとAWS middlebox routeは物理的同型でない。共通化はroles、logical policy、manifest、conformanceに限定する。

## Complexity Tracking

現在段階で必須のConstitution Check違反はない。4 VM、Router 5 NIC、loopback Client forward、control限定Portal Compose publish、2段階Packer buildは、3つの分離data segment、hostからの明示入口、control分離、反復速度、将来AMIのguest-role共有を同時に満たすための直接的な構成である。AMI build、AWS network、benchmark/scoring、trial solvingは名前付き後続featureへの引渡しであり、例外ではない。
