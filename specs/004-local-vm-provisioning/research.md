# Research: ローカルVM競技環境

**Feature**: `004-local-vm-provisioning`

**Date**: 2026-08-06

## Decision 1: Linux x86_64の標準providerをVagrant + VirtualBoxへ固定する

**Decision**: 検証済み開始値をVagrant `2.4.9`、VirtualBox `7.2.14`とし、Linux x86_64 hostでUbuntu 24.04 amd64 guestを実行する。Vagrantfileは`virtualbox` providerだけを受け付け、box architecture metadataは`amd64`だけを許可する。macOS、Linux arm64、その他のhost OS/architectureはsupport、artifact、release qualification matrixに含めない。

**Rationale**: 開発者が保有し検証できるhostをLinux x86_64へ限定すると、単一provider・単一guest architectureで再現性を判定できる。CanonicalはUbuntu 24.04以降の公式Vagrant boxを公開しないため、公式amd64 ISOとSHA-256から自前boxを生成する。

**Alternatives considered**:

- VMware/QEMU系provider: 現在のLinux x86_64 + VirtualBox要件に対してprovider、box、network検証matrixを増やすため不採用。VirtualBoxから暗黙fallbackしない。
- macOSまたはlocal arm64対応: 対象機材がなくrelease qualification不能なため今回のscope外。必要になった時点で別featureとしてhost、provider、artifact、test matrixを追加する。

**Validation gate**: 最初のincrementでLinux x86_64 release workstationを使い、amd64 base box生成、Router 5 NIC、Client port forward、control IPへのDocker publish、4 VM + Portal Compose同時起動、internal network、再起動、destroy/recreateを連続3回検証する。未対応providerへfallbackせず、失敗時はreleaseを不合格にする。

References:

- [Vagrant box architecture](https://developer.hashicorp.com/vagrant/vagrant-cloud/boxes/architecture)
- [Vagrant 2.4.9 changelog](https://github.com/hashicorp/vagrant/blob/v2.4.9/CHANGELOG.md)
- [Vagrant VirtualBox networking](https://developer.hashicorp.com/vagrant/docs/providers/virtualbox/networking)
- [VirtualBox 7.2.14 manual](https://download.virtualbox.org/virtualbox/7.2.14/UserManual.pdf)
- [Packer VirtualBox plugin releases](https://github.com/hashicorp/packer-plugin-virtualbox/releases)
- [Ubuntu Vagrant image policy](https://documentation.ubuntu.com/public-images/public-images-explanation/vagrant/)

## Decision 2: 5 NIC Router、3つの1対1 data segment、Client port forwardを使う

**Decision**: 各問題serverはVagrant用の個別NAT `bootstrap0`と専用internal network `data0`の2 NICだけを持つ。Routerは`bootstrap0`、serverごとの`data1`〜`data3`、host-onlyの`control0`という5 NICを持つ。Client入口はVagrant `forwarded_port`でhost `127.0.0.1:18080`からRouter guest `8080`へTCP転送し、`auto_correct=false`とする。bridged、NAT Network、Client用host-only ingress、全VM共有host-only management LANは使用しない。

既定CIDRは、VirtualBox既定host-only範囲かつ将来AWSへ写像可能な`192.168.60.0/24`内の`/28`とする。

| Segment | CIDR | Router | Peer |
|---|---:|---:|---:|
| `server1-data` | `192.168.60.0/28` | `192.168.60.5` | server1 `192.168.60.6` |
| `server2-data` | `192.168.60.16/28` | `192.168.60.21` | server2 `192.168.60.22` |
| `server3-data` | `192.168.60.32/28` | `192.168.60.37` | server3 `192.168.60.38` |
| `control` | `192.168.60.64/28` | `192.168.60.69` | host `192.168.60.65` |

internal network名とMACはenvironment IDで名前空間化して固定する。guestはMACから論理interface名を決め、Adapter番号や`ethN`列挙順に依存しない。CIDRは明示的に上書きできるが、自動選択や暗黙変更はしない。

**Rationale**: 通常NATは各VMを別のL2に保ちながらVagrant SSHとpackage取得を維持する。data internal networkはhostからも他serverからも直接見えず、Router-only経路を構造で表現する。Clientはhost loopbackへだけ公開され、専用host-only ingress NICを削減できる。Vagrantはhost/guest port、`host_ip`、`auto_correct`を明示できる。`/28`はAWS subnetの最小サイズで、`.5`と`.6`はAWS予約アドレスを避ける。

**Alternatives considered**:

- 共有host-only management LANまたはNAT Network: VM間直通が可能になり、Router bypassを生むため不採用。
- 1つのdata subnet + firewall: 同一L2のpeer通信をRouter経由へ強制できないため不採用。
- Client用host-only ingress: 動作するが、Clientは単一TCP入口だけで足り、追加NIC/CIDR/host interfaceを維持する必要がない。
- `0.0.0.0`へのClient port publish: LANから意図せず到達可能になるため不採用。
- `/30` point-to-point: localには十分だがAWS subnetへ写像できないため不採用。
- provisioning後のNAT NIC detach: 分離は強いが日常の再provisionと診断を壊す。後続のsealed-mode候補とする。

References:

- [Vagrant networking](https://developer.hashicorp.com/vagrant/docs/networking)
- [VirtualBox networking modes and limits](https://docs.oracle.com/en/virtualization/virtualbox/7.2/user/networkingdetails.html)
- [Vagrant forwarded ports](https://developer.hashicorp.com/vagrant/docs/networking/forwarded_ports)
- [AWS subnet sizing](https://docs.aws.amazon.com/vpc/latest/userguide/subnet-sizing.html)

## Decision 3: routeとfirewallの両方でRouter-onlyを保証する

**Decision**: 各serverは他2つのdata CIDRへのrouteを自身のRouter側IPへ向け、data NICにはdefault gatewayを置かない。RouterはIPv4 forwardingを有効にし、nftables forward policyをdefault dropとしてdata pairだけを明示許可する。Client forwardは`bootstrap0:8080`のEnvoy inputで終端し、Envoyがdata addressへ新しいupstream接続を作る。controlはRouter/Portal自身で終端する。bootstrapからdata/controlへの汎用L3 forwardとIPv6 forwardingは拒否し、inter-server SNATは行わない。

problem server側もinterface別default dropとし、SSHは`bootstrap0`、nginx/PostgreSQLの競技用portは`data0`からだけ受ける。Vagrant SSH forwardは`127.0.0.1:2201`〜`2204`へ固定し、`auto_correct`を無効化する。application/DBのhost forwardをproblem serverへ作らない。通常NAT経由のhost-loopbackや他VMのforwarded portもnegative E2Eで検査する。

**Rationale**: L2分離だけでなくroute、forward policy、bind addressを重ねることで、管理NICが代替data planeになる設定ミスを検出できる。送信元IPを維持するため既存Routerのregion分類と診断も成立する。

**Alternatives considered**:

- route確認だけ: guest firewallやprovider配線の誤りを見逃す。
- `traceroute`だけ: ICMP policyとtimeoutに依存し、合否根拠として弱い。
- inter-server NAT: 元server identityを消し、既存Routerの分類と観測を壊す。

## Decision 4: Ansibleをguest構成の唯一の正本にする

**Decision**: `common`、`problem_server`、`router`、`runtime_network`、`image_cleanup`、`conformance`のAnsible rolesを設ける。Packer builder、Vagrantfile、接続、provider network、出力先はadapterとしてrolesの外へ置く。各roleは`meta/argument_specs.yml`で入力を検証する。

Packerは2段階で使用する。

1. 公式Ubuntu Server 24.04 amd64 ISOをSubiquity Autoinstallし、VirtualBox用の最小base boxを生成する。
2. base boxをPacker `vagrant` builderで起動し、同じ`image.yml`から`problem-server`と`router`のrole boxを生成する。

将来のamd64 AMIはVirtualBox artifactを変換せず、Canonical amd64 AMIをsourceにした`amazon-ebs` builderへ同じ`image.yml`を適用する。3台のserverは同一problem-server imageを使い、node identityはruntimeに注入する。

**Rationale**: OS install/Guest Additionsの遅い処理をbaseへ分離し、通常のrole変更は派生boxだけ再buildできる。localとAWSで共有すべきものはguest desired stateであり、hypervisor image形式やnetwork wiringではない。

**Alternatives considered**:

- 1つの万能image: Routerへ不要なPostgreSQL/アプリを持ち込み、role別AMIと乖離する。
- VirtualBox imageをAWSへimport: AWS kernel、ENA、cloud-init、source image lifecycleとの差を抱える。
- local/AWS別playbook: 構成driftを許すため不採用。

References:

- [Packer build blocks](https://developer.hashicorp.com/packer/docs/templates/hcl_templates/blocks/build)
- [Packer Vagrant builder](https://developer.hashicorp.com/packer/integrations/hashicorp/vagrant/latest/components/builder/vagrant)
- [Packer Ansible provisioner](https://developer.hashicorp.com/packer/integrations/hashicorp/ansible/latest/components/provisioner/ansible)
- [Ansible roles](https://docs.ansible.com/projects/ansible/latest/playbook_guide/playbooks_reuse_roles.html)
- [Ubuntu Autoinstall reference](https://canonical-subiquity.readthedocs-hosted.com/en/latest/reference/autoinstall-reference.html)

## Decision 5: image bakeとruntime convergenceを分ける

**Decision**: role boxには秘密でない固定物だけをbakeする。Ubuntu package、PostgreSQL/nginx、backend/frontend/router artifact、systemd unit、設定skeleton、health tooling、`/etc/isucon/release.json`を含める。ただしPostgreSQL clusterは初期化しない。node identity、hostname、MACとinterface role、IP/route、environment ID、Portal endpoint、公開host名、DB資格情報、cluster作成、初回migration/seed、service startは薄い`runtime.yml`で行う。

Vagrantは4台を`--no-provision`でbootしてSSH inventoryを生成した後、host側の固定Ansible環境から全4台へ1回のruntime playbookを実行する。default synced folderと`ansible_local`は使わない。

**Rationale**: 通常の起動と再provisionを速くしつつ、node固有値や秘密をcloneされるimageから除外できる。変更の影響範囲を次のように明確化できる。

- runtime configのみ: reprovision
- backend/frontend/router artifact: artifact再build + role image再build、開発用fast pathではreprovision copy
- OS/package/role: role image再build
- ISO/Guest Additions: base box再build
- topology/NIC: VM recreate

**Alternatives considered**:

- 全bake: 最速だが秘密、address、node identityがimageへ入り、反復が遅い。
- base OSだけbake: 毎回のpackage取得が遅く、AMIとの同等性と再現性が低い。
- `ansible_local`: 各guestに制御toolchainとplaybookを置き、box肥大化と秘密流入の面を増やす。

References:

- [Vagrant remote Ansible provisioner](https://developer.hashicorp.com/vagrant/docs/provisioning/ansible)
- [Vagrant Ansible Local](https://developer.hashicorp.com/vagrant/docs/provisioning/ansible_local)

## Decision 6: version制約は実際の導入箇所だけに置く

**Decision**: Python、ansible-core、Packerは`mise.toml`、Packer pluginsは各Packer HCL、PostgreSQL collectionは`requirements.yml`をそれぞれ正本とする。Vagrant、VirtualBox、Docker Engine、Docker Composeはexact versionを要求せず、コマンドの存在、daemon接続、resolved Compose config、specific-address publish contractの実動作で互換性を判定する。これらの値をrelease manifestへ重複させない。

Ubuntu packageはpackage名だけをAnsible roleへ記述してNoble repositoryから取得する。PostgreSQLはruntime contractであるmajor `16`をpackage名`postgresql-16`で維持する。packageのminor versionや未適用のAPT snapshotをrelease入力へ置かず、build後のbox SHA-256またはAMI IDとconformance結果を昇格単位にする。

**Rationale**: release manifestの重複versionはinstallerに使われず、更新箇所と認知負荷だけを増やす。個別packageの一部だけを固定しても、未固定packageとtransitive dependencyが残るためroot filesystemの再現にはならない。必要な不変性はsource ISO/AMIのidentity、runtime compatibility、生成machine imageのidentityで担保する。

**Alternatives considered**:

- release manifestにもtool版を複製: 正本が増える一方、build behaviorは変わらないため不採用。
- 一部packageだけexact versionへ固定: live repositoryから削除された版でbuildが壊れ、完全な再現性も得られないため不採用。
- dependency closureを含むAPT snapshotを実装: byte-for-byte再構築が必要になった場合の選択肢だが、現在のlocal development要件には過剰なため採用しない。

References:

- [Ansible apt module](https://docs.ansible.com/projects/ansible/latest/collections/ansible/builtin/apt_module.html)
- [Packer required plugins](https://developer.hashicorp.com/packer/docs/templates/hcl_templates/blocks/packer)
- [Packer manifest post-processor](https://developer.hashicorp.com/packer/docs/post-processors/manifest)
- [Docker Compose port publishing](https://docs.docker.com/reference/compose-file/services/#ports)

## Decision 7: native systemd servicesとしてpayloadを配備する

**Decision**: 問題serverではUbuntu NobleのPostgreSQL 16、`python3-psycopg`、nginx、backend API/worker/image-sanitizer/migration tools、frontend static artifactをnative service/fileとして配備する。nginxをdata addressの`8080`で公開し、backend APIはloopbackの`8081`で受ける。これにより既存Envoy upstream portを維持する。image buildでは`postgresql-common`の自動cluster作成を抑止する。runtime初回だけnode固有cluster、system identifier、application role/database、資格情報を生成し、PostgreSQLはloopbackと自身のdata addressだけでlistenする。`pg_hba.conf`はapplication roleとcompetition data CIDRに限定する。

Routerでは`isureal-router`と既存仕様に固定されたEnvoy `1.32.2`を別systemd serviceとして同じVM network namespaceで動かす。Router binaryは必要最小の`CAP_NET_ADMIN`、Envoyは非特権userとし、bootstrapはcanonical topologyから生成・validateする。

Portal MockはVM imageへ入れず、既存`portal-mock/compose.yaml`のserver/webをenvironment-scoped Compose projectとして`external-router` modeで起動する。Compose fileのport定義をbind IP変数へ対応させ、Portal APIはcontrol host-only address `192.168.60.65:18082`、Portal UIは`127.0.0.1:15174`だけへpublishする。Routerはcontrol0からAPIへ接続する。Clientは`127.0.0.1:18080`からRouter guest Envoy `8080`へのVagrant port forwardを使う。Compose projectは`isureal-<environment-id>-portal`とし、`up --build --wait`、`ps`、`logs`、`down --remove-orphans`をstable Make lifecycleへ包む。

**Rationale**: nested container networkを避け、Routerのnft/tc操作をVM namespaceへ直接適用できる。競技者と作問者がservice、config、log、source/artifactを通常のLinux境界で観測しやすい。既存Routerのnetwork discovery、xDS、Envoy契約は変更しない。

**Alternatives considered**:

- guest内Docker Compose: 既存E2Eを再利用しやすいが、network namespace、capability、log、参加者の変更経路が一段増える。
- nginxをport 80、Envoy bootstrapを変更: 成立するが既存Routerの固定upstream contractを不要に分岐させる。
- Portal MockをRouter imageへbake: 本番AMIへの混入とcontrol-plane境界違反を招く。
- Portalをhost processとして直接起動: Composeという要求を満たさず、server/webのhealth・logs・cleanup境界が別実装になる。
- Compose APIを`0.0.0.0`へpublish: host LANへ不要に露出するため不採用。Compose engineがhost IP mappingを拒否する場合はprovider spikeをfailさせる。

References:

- [Ubuntu Noble PostgreSQL 16 package](https://packages.ubuntu.com/noble/postgresql-16)
- [PostgreSQL 16 `initdb`](https://www.postgresql.org/docs/16/app-initdb.html)
- [PostgreSQL 16 database file layout](https://www.postgresql.org/docs/16/storage-file-layout.html)
- [Vagrant forwarded ports](https://developer.hashicorp.com/vagrant/docs/networking/forwarded_ports)
- [Docker Compose port publishing](https://docs.docker.com/reference/compose-file/services/#ports)

## Decision 8: 既存PostgreSQL application contractを再利用する

**Decision**: 現行backendが要求する`postgres`/`postgresql` URL、`pgx/v5`、既存PostgreSQL migrationsをそのまま採用する。`release.json`へengine `postgresql`、major version `16`、schema compatibility IDを置き、backendとdatabase-migrations artifactへ同じcompatibility IDを付ける。provisioningはapplication role/databaseを作成し、empty DBにだけ既存migration/seedを適用する。契約の欠落または不一致はpreflight/readiness failureにする。

**Rationale**: `backend/internal/config/config.go`はPostgreSQL URLだけを受け入れ、`backend/go.mod`は`pgx/v5`を使用し、CIもPostgreSQL 16でmigrationとintegration testを実行している。ユーザー指定と既存アプリ契約が一致したため、DB移植用の別featureやfixture readinessは不要である。一方、engineが一致してもschema/migration driftは起こり得るため、compatibility gateは維持する。

**Alternatives considered**:

- 別DB engineへbackendを移植: 現在の要件・コードと一致せず、repository、transaction、SQL、migrationを横断する不要な変更になる。
- 初期化済みPostgreSQL clusterをrole imageへbake: system identifier、資格情報、初期dataが3台へcloneされるため不採用。
- engine名だけ検査してschema/migration契約を省く: artifact driftを起動後まで見逃すため不採用。

## Decision 9: 入力manifest、出力manifest、runtime stateを分離する

**Decision**: version管理する`release.json`をbuild入力manifestとし、Ubuntu amd64 source/checksum、app/Router artifact URI、DB compatibilityだけを持つ。開発用local artifactは頻繁に再buildされ、独立した信頼源もないためchecksum比較を行わない。将来remote artifactを配布する場合だけ入力manifestでSHA-256を必須とする。Packer生成物はbuild名、時刻、artifact ID/path、role、provider、arch、source IDを記録し、Git revisionはbuild時にrepositoryから取得してprovider非依存の`image-artifact-index.json`へ正規化する。Portal Composeのproject、effective config hash、amd64 OCI image IDはenvironmentのValidationReportへ記録する。guest内`/etc/isucon/release.json`は入力releaseを保持し、node固有値は`/etc/isucon/runtime.json`へ分離する。

private box/artifactは将来のversioned private S3 bucketへ置き、短期資格情報でhost/CIが取得してSHA-256検証後、Packer/Ansibleへlocal fileとして渡す。初期incrementは同じcontractの`file://` sourceを使えるようにする。

**Rationale**: build入力と生成outputを分ければ、手動のversion転記を増やさず「testしたartifactそのもの」だけを昇格できる。

**Alternatives considered**:

- artifactをGitへcommit: 大容量、秘密情報監査、更新履歴の面で不適切。
- floating URL/tag: digestと実体の対応を保証できない。
- build後に同じ版名で再build: test済みartifactと配布artifactが異なり得るため禁止する。

## Decision 10: secret-free imageとclone hygieneをgateにする

**Decision**: build roleは秘密値を受け取らない。private artifact/cloud認証はhost側の短期資格情報だけで使い、Packer変数、manifest、guestへ残さない。runtime secretと生成Portal envはgitignore対象のenvironment stateまたは外部secret storeから供給し、Ansibleは`no_log: true`、`diff: false`、root所有`0600`で書く。default synced folderは無効化する。Compose config/log/status出力も秘密値をredactし、生成envの内容をValidationReportへ直接埋め込まない。

capture前に一時SSH key、cloud-init/autoinstall seed、shell history、logs、host SSH key、machine-id、初期化済みPostgreSQL cluster dataをcleanupし、clone後のruntimeで一意に生成する。box filesystemとlogsをsecret scanしてから昇格する。

**Rationale**: Packerの`sensitive`は表示maskであり、imageへ書かれない保証ではない。PostgreSQL cluster system identifierやmachine-idをcloneすると、将来のreplication、backup、診断でidentity衝突を起こす。

References:

- [Packer sensitive variables](https://developer.hashicorp.com/packer/docs/templates/hcl_templates/blocks/variable)
- [Ansible Vault scope](https://docs.ansible.com/projects/ansible/latest/vault_guide/vault.html)

## Decision 11: static、image、4-node、promotionの4段階で検証する

**Decision**: 次のtest ladderを採用する。

1. Static: Packer fmt/init/validate、Autoinstall schema、Vagrant validate、Ansible syntax/lint/argument spec、JSON Schema。
2. Image/Compose convergence: image playbook、runtime playbook 2回、2回目`changed=0`、Compose config/API/UI health、release/clone hygiene/secret scan。
3. Four-node conformance: exactly 4 VMと1 Portal project、OS/arch/manifest、service health、Portal desired/applied、Client loopback forward、6方向routeとRouter counter、neighbor table、Router-stop、新規接続復旧、未許可forward/bootstrap bypass negative test、再provision/reboot後のPostgreSQL data保持。
4. Promotion: buildしたbox/AMIの同一ID/checksumをtestし、合格済みartifactだけをprivate registry manifestへ昇格する。test後に同じrelease IDを再buildしない。

Full E2Eはnested virtualizationを期待できないhosted CIでは実行せず、Linux x86_64のself-hosted runnerまたは明示的release workstationで実行する。static testは通常CIで毎回行う。

**Rationale**: route textやtracerouteだけでは実データがRouterを通ったことを証明できない。route、nft counter、neighbor、Router停止を組み合わせると構造と実動作を検証できる。複数のLinux x86_64 hostで機能testを共通化するが、local score/latencyは比較しない。

References:

- [Ansible check and diff mode](https://docs.ansible.com/projects/ansible/latest/playbook_guide/playbooks_checkmode.html)
- [Ansible module idempotence guidance](https://docs.ansible.com/projects/ansible/latest/module_plugin_guide/modules_intro.html)

## Decision 12: future AWSではnetwork wiringだけをprovider layerへ置く

**Decision**: amd64 AMIにVirtualBoxのNIC数、interface名、local CIDR、Vagrant port forward、local Portal Compose endpointをbakeしない。AWSではserver1〜3を別`/28` subnet、Router applianceを別transit subnetへ置き、各server subnet route tableのpeer CIDRをRouter ENIへ向ける。Router instanceのsource/destination checkを無効化し、cloud ingress/controlを別subnet/SGに保つ。初期構成は同一AZとし、instance typeのENI上限をcloud preflightで検査する。

**Rationale**: AWS middlebox routingではapplianceがsource/destinationと異なるsubnetにある構成が公式にサポートされる。localのdirect-attached NICと物理的に同型にはならないため、共通化対象をlogical segment、route policy、guest roles、conformanceへ限定する。

**Alternatives considered**:

- Routerへ3 server subnetのENIを直接接続: instance typeのENI/IP上限とAZ配線へ強く依存する。
- 同一subnet内でrouteをRouterへ向ける: VPC local routeより細かい同一subnet host間経路を一貫してmiddleboxへ強制できない。

References:

- [AWS middlebox routing](https://docs.aws.amazon.com/vpc/latest/userguide/route-table-options.html)
- [EC2 source/destination checks](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/using-eni.html)
- [Packer amazon-ebs builder](https://developer.hashicorp.com/packer/integrations/hashicorp/amazon/latest/components/builder/ebs)
- [Finding Ubuntu AMIs](https://documentation.ubuntu.com/aws/en/latest/aws-how-to/instances/find-ubuntu-images/)

## Resolved Unknowns

- 対応host/provider: Linux x86_64 + VirtualBox。macOS、local arm64、別providerへfallbackしない。
- guest/artifact architecture: amd64だけ。release buildでcross-emulationしない。
- Canonical Vagrant box: Ubuntu 24.04では提供されないため自前buildする。
- role image: problem-serverとRouterの2種類。server identityはruntimeで与える。
- provisioning方式: Packer build時とVagrant runtimeの双方から同じAnsible rolesを呼ぶhybrid。
- network: Router 5 NIC、server 2 NIC、3 internal data + control host-only + per-VM NAT。ClientはRouter NATへのloopback-only forward。
- package管理: Noble repositoryからrole指定のpackageを取得し、生成boxのSHA-256またはAMI IDで配備物を固定する。tool/plugin版はmise/HCL/requirementsだけで管理する。
- runtime形式: native systemd services。既存Router/Envoy契約とupstream port 8080を維持する。
- Portal: 4 VM外のenvironment-scoped Docker Compose projectをexternal-Router modeで起動し、APIはcontrol IP、UIはloopbackだけへpublishする。imageへ含めない。
- Database: 既存backendとPostgreSQL 16を直接組み合わせ、major version・schema・migrationの互換ID不一致は失敗させる。
- AMI: 今回はamd64 builder境界と共通roleまで。build/register/network IaCは後続increment。

未解決の`NEEDS CLARIFICATION`はない。
