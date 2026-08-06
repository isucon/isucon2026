# Feature Specification: ローカルVM競技環境

**Feature Directory**: `specs/004-local-vm-provisioning/`

**Spec File**: `specs/004-local-vm-provisioning/spec.md`

**Created**: 2026-08-05

**Status**: Draft

**Specification Stage**: 競技仕様（作問・検証用の環境プロビジョニングサブ仕様。競技配布は後続段階）

**Input**: User description: "作問段階で手元で手軽にVMを立ち上げられるようにする。対象hostはLinux x86_64のみとし、Macは対象外。将来的にAMI化するためProvisioningにはAnsibleとPackerを使う。LinuxではVagrantとVirtualBoxを使う。問題サーバー3台とRouter 1台を用意し、問題サーバーは別々のネットワークに置く。問題サーバーにはPostgreSQL、backend、frontend、nginxを、Routerにはrouterをデプロイする。Ubuntu 24.04ベース。PortalはDocker Composeで起動し、ClientからRouterへの入口はport forwardingで実現する。Docker Engine 29.7.1とDocker Compose 5.4.0を使用する。"

## Summary

作問者がLinux x86_64の手元環境で、3台の問題サーバーと1台のRouterからなる競技相当トポロジーを再現可能に構築、再設定、検証、破棄できるようにする。3台の問題サーバーはデータプレーン上で相互に分離し、相互通信は必ずRouterを経由する。問題サーバーにはデータベースとWebアプリ一式、Routerには既存Router仕様が要求する実行スタックを配備する。ローカルPortalは4台のVMとは独立したCompose stackとして管理し、Clientはhostの固定portからRouterへ接続する。同じゲスト構成定義を将来のamd64 AMI生成へ引き継げる境界を固定し、ローカル仮想化方式固有の設定を競技ホスト構成から分離する。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Linuxで競技トポロジーを起動する (Priority: P1)

Linuxを使う作問者は、前提ツールを準備したクリーンな作業環境から、文書化された単一の入口で4台のVMを起動し、各VMの役割とネットワーク分離が正常であることを確認する。

**Why this priority**: 4台構成を日常的に作成・破棄できることが、本機能の最小価値だからである。

**Independent Test**: 対応Linuxホストで初回構築を実行し、Portal stackと4台すべての準備完了、Client入口、サービスのhealth、Router経由通信、問題サーバー間の直接通信不能を一つの検証入口から確認できる。

**Acceptance Scenarios**:

1. **Given** 対応するLinuxホストと必要な空き資源がある, **When** 作問者が環境起動を実行する, **Then** environment専用Portal Compose stackと`server1`、`server2`、`server3`、`router`の4台が作成され、全必須サービスがreadyになる。
2. **Given** 4台がreadyである, **When** 6方向すべての問題サーバー間通信を検査する, **Then** 各経路はRouterを通り、Routerを停止すると新規通信は成立しない。
3. **Given** 管理・プロビジョニング用の接続経路が存在する, **When** 問題サーバー間の迂回経路を検査する, **Then** その経路を使った直接通信は成立しない。

---

### User Story 2 - 安全に反復して診断する (Priority: P2)

作問者はアプリやRouterの変更後に、VM全体を作り直さず対象だけを再設定できる。途中失敗時には失敗したノードと段階が分かり、同じ操作を安全に再実行できる。

**Why this priority**: 作問中は反復回数が多く、全イメージ再作成を毎回要求すると手軽さが失われるためである。

**Independent Test**: 同じ環境へ設定を2回連続適用し、2回目に予期しない変更や停止がないこと、意図的な失敗後に修正して再実行できることを確認する。

**Acceptance Scenarios**:

1. **Given** 正常な環境がある, **When** 同じ構成を再適用する, **Then** 永続データを暗黙に消さず、不要なサービス再起動を起こさず、ready状態を維持する。
2. **Given** 1台への設定適用が途中で失敗した, **When** 作問者が診断結果を確認する, **Then** ノード、段階、原因候補、再実行方法を特定できる。
3. **Given** 環境専用資源が存在する, **When** 作問者が破棄を明示する, **Then** 対象のVM、専用ネットワーク、Portal Compose資源だけが削除され、他のVM、container、network、volumeは変更されない。

---

### User Story 3 - 将来の競技イメージへ同じ構成を引き継ぐ (Priority: P3)

環境担当者は、ローカルVMで検証済みのゲスト構成を、環境固有の値だけ差し替えてクラウド用マシンイメージの作成に再利用する。

**Why this priority**: ローカル専用構成と本番イメージ構成の乖離を防ぎ、競技配布の再現性を高めるためである。

**Independent Test**: ローカル用とクラウド用の成果物が同じ役割定義と版マニフェストを参照し、生成後に同じノード別適合性検証を通ることを確認できる。

**Acceptance Scenarios**:

1. **Given** ローカルVMで検証済みの構成リビジョンがある, **When** 別のイメージ出力先を選ぶ, **Then** ゲスト内の役割定義を複製せず同じ構成リビジョンを使用できる。
2. **Given** 環境固有のアドレスや資格情報が異なる, **When** イメージを生成する, **Then** それらは実行時設定として分離され、ベースイメージに開発用秘密値が残らない。

### Edge Cases

- ホストの仮想化支援が無効、必要プラグインが不足、またはサポート外のCPUアーキテクチャである場合は、VM作成前のpreflightで具体的に失敗する。
- Docker daemonまたはCompose機能が利用不能な場合は、VMやCompose資源を作成する前のpreflightで具体的に失敗する。
- 予定したサブネットがホストのVPN、社内ネットワーク、既存VMネットワークと衝突する場合は、衝突箇所を示して設定変更を促し、部分的なトポロジーをreadyにしない。
- Client転送port、Portal UI/API公開port、SSH転送portのいずれかが使用中なら、自動的に別portへ変更せず衝突を示して失敗する。
- VMのNIC列挙順が変わっても、MACアドレスまたは設定上の役割でインターフェースを識別し、偶然の`ethN`順へ依存しない。
- 初回ダウンロードやパッケージ取得が失敗した場合は、不完全な状態を成功扱いせず、安全に再試行できる。
- Portal image buildまたはCompose health checkが失敗した場合は、Routerをreadyとせず、environment専用Compose logsと再実行方法を残す。
- 1台だけ停止または再起動した場合に、他の問題サーバーへ直接到達する経路が新たに生じない。
- アプリ成果物、DBマイグレーション、設定の版が互換でない場合は、サービス起動後の曖昧な障害ではなく配備検証で失敗する。
- PostgreSQL major version、schema compatibility ID、migration revisionのいずれかがbackend releaseと一致しない場合は、環境全体をreadyと報告しない。
- 破棄または再作成を中断しても、次回の状態確認で残存資源を列挙できる。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST create exactly four guest node identities: `server1`, `server2`, `server3`, and `router`.
- **FR-002**: Every guest node MUST use a version-pinned Ubuntu 24.04 base and MUST record its OS image identifier and CPU architecture in a release manifest.
- **FR-003**: System MUST support one explicitly documented Linux x86_64 host profile that presents the fixed node identities, guest roles, logical addresses, Portal lifecycle, Client entry point, and validation interface.
- **FR-004**: System MUST reject unsupported host operating systems and CPU architectures before creating resources and MUST NOT fall back to another provider; Mac and local arm64 support are outside this feature.
- **FR-005**: Each problem server MUST have a dedicated competition data-plane segment shared only with the corresponding Router interface.
- **FR-006**: Any management or bootstrap path attached to a problem server MUST NOT permit direct problem-server-to-problem-server communication.
- **FR-007**: Every supported path between distinct problem servers MUST traverse the Router, and disabling the Router MUST prevent all six directions of new inter-server communication.
- **FR-008**: Network identities and host port assignments MUST be deterministic and configurable, MUST reject overlapping subnets or ports, and MUST NOT depend on guest interface enumeration order.
- **FR-009**: The Router MUST expose a host-loopback-only forwarded Client ingress and a separately identifiable control path required by the pinned Router contract.
- **FR-010**: Each problem server MUST receive a running PostgreSQL 16 service, backend runtime, built frontend assets, and nginx configuration associated with one pinned application release.
- **FR-011**: Each problem server MUST expose node-local health checks for PostgreSQL, backend, and nginx, and MUST serve the application through nginx before being declared ready.
- **FR-012**: A backend release deployed by this feature MUST declare compatibility with the provisioned PostgreSQL major version, schema, and migrations; an incompatible or missing release MUST fail readiness with an actionable message.
- **FR-013**: The Router node MUST receive the pinned Router runtime and every colocated runtime dependency required by `specs/003-router` without embedding E2E-only fixtures.
- **FR-014**: The Router node MUST expose separate process-health and data-plane-readiness checks consistent with the pinned Router contract.
- **FR-015**: The configuration process MUST be idempotent: applying an unchanged desired state twice MUST preserve persistent application data and avoid unrelated changes.
- **FR-016**: The environment MUST provide separate entry points for preflight, initial creation, incremental provisioning, validation, status inspection, and explicit destruction, with Portal Compose lifecycle included in the same environment-scoped workflow.
- **FR-017**: A single conformance validation MUST verify node identity, OS version, installed release manifest, service health, Portal convergence, forwarded Client ingress, all six inter-server routes, management-path isolation, and Router-stop behavior.
- **FR-018**: Provisioning failures MUST identify the node and stage and MUST leave enough state and diagnostics for safe retry.
- **FR-019**: Destruction MUST target only VM, network, state, and Compose resources carrying this environment's explicit identity and MUST NOT remove unrelated host, VM, container, network, image, or volume resources.
- **FR-020**: Guest role definitions MUST be shared between local VM image creation and future cloud image creation; provider-specific source, transport, networking, and output settings MUST remain outside those roles.
- **FR-021**: All base images, system packages, application artifacts, Router artifacts, and configuration inputs MUST be versioned or content-addressed in a machine-readable manifest.
- **FR-022**: Build-time images MUST contain no developer credentials, cloud credentials, private keys, Portal credentials, or environment-specific secrets.
- **FR-023**: Runtime secrets MUST be supplied through ignored or external inputs, rendered with least-privilege permissions, and redacted from normal logs and validation output.
- **FR-024**: Results measured on the local development host MUST NOT be used as official score, latency, or throughput calibration values.
- **FR-025**: Initial data reset and application data preservation MUST be separate explicit operations; ordinary reprovisioning MUST NOT reset PostgreSQL implicitly.
- **FR-026**: The local workflow MUST make clear whether a change requires only reprovisioning, rebuilding an application artifact, rebuilding a base box, or recreating all VMs.
- **FR-027**: The local Portal MUST run from the repository's Portal definition as an environment-scoped Docker Compose stack in external-Router mode, and both its API health and Router desired/applied convergence MUST pass before the environment is ready.
- **FR-028**: Client ingress MUST use one fixed TCP port forward from host loopback to the Router application listener; automatic port correction and application or database forwards to problem servers MUST NOT be permitted.
- **FR-029**: The Portal UI MUST be published only to host loopback, while the Portal API used by the Router MUST be published only on the dedicated control path; neither endpoint may bind indiscriminately to every host interface.
- **FR-030**: Portal Compose source revision, container image identities, Compose project identity, and effective non-secret configuration MUST be inspectable in status and validation results.

### Key Entities

- **Environment Profile**: ホスト種別、仮想化方式、ゲストアーキテクチャ、資源既定値、サブネット範囲をまとめた選択可能な構成。
- **Node**: `server1`〜`server3`または`router`という不変ID、役割、アドレス、割当資源、適用済みリリースを持つゲスト。
- **Network Segment**: 用途、CIDR、Router側アドレス、接続を許可されたノード集合を持つ論理ネットワーク。
- **Port Forward Binding**: environment、host loopback port、Router guest port、protocol、衝突判定を持つClient入口。
- **Portal Compose Deployment**: environment専用project identity、Portal API/UIの公開境界、source revision、image identity、healthを持つhost側stack。
- **Guest Role**: 問題サーバーまたはRouterへ適用する、環境出力先に依存しない望ましい構成。
- **Machine Image Artifact**: ベースOS、アーキテクチャ、ビルド時刻、入力リビジョン、チェックサムを持つ再利用可能なイメージ。
- **Release Manifest**: OS、パッケージ、アプリ、Router、設定スキーマ、イメージの互換な組を固定する記録。
- **Runtime Configuration**: アドレス、環境識別子、公開ホスト名、外部接続先、秘密値参照など、イメージへ焼き込まない環境固有値。
- **Validation Result**: 環境ID、リリース、検査時刻、ノード別health、経路、分離、失敗理由を持つ適合性判定。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 前提ツール、Portal image、ベースイメージが取得済みの対応ホストでは、作問者が1つの入口から開始して15分以内にPortalと4台すべてをreadyにできる。
- **SC-002**: 初回利用者がQuickstartだけを参照し、環境作成、検証、状態確認、破棄を30分以内に一巡できる。
- **SC-003**: 共通の自動検証が、Client転送入口からのHost/国別routingと6方向すべてのサーバー間Router通過を100%確認し、許可されていない管理・転送経路による直接到達試行を100%拒否する。
- **SC-004**: 同じリリースと入力で2回連続して構成を適用したとき、2回目の予期しない変更件数が0で、アプリデータが維持される。
- **SC-005**: 対応Linux x86_64環境の1台以上で、同じリリース、Portal Compose契約、Client転送契約によるcreate/reboot/destroyを3回連続で実行し、共通の機能適合性検証を100%通過する。
- **SC-006**: 意図的に各主要段階を1回ずつ失敗させたとき、診断出力から対象ノードと失敗段階を5分以内に特定でき、修正後の再実行で復旧できる。
- **SC-007**: ローカル出力と将来のクラウド出力が、重複したゲスト役割定義なしに同じ構成リビジョンと版マニフェストを参照し、同じノード別適合性検証を通過する。
- **SC-008**: イメージと通常ログに対する秘密情報監査で、開発者・クラウド・Portalの資格情報露出が0件である。

## Competition Performance & Distribution Requirements

- **Public Contract & Invariants**: `specs/001-isureal-social-app` の公開機能・データ不変条件と `specs/003-router` のRouter-only経路、Host/国判定、last-known-goodを固定して継承する。初期実装と最適化後実装へ同じアプリ契約・競合・ネットワーク適合性検証を適用する。
- **Load & Scoring Model**: 本サブ仕様は負荷モデル、採点式、初期・目標スコア帯を定義しない。後続の競技仕様が、ここで固定したVM役割、ネットワーク、資源プロファイル、アプリ版、Router版を参照して定義する。ローカルホスト間の性能値を公式較正に使用しない。
- **Intended Bottlenecks (private)**: 本機能は意図的なボトルネックを導入しない。後続の非公開課題台帳が指定する挿入点と強度を、ゲスト役割やイメージ生成方式へ不可分に埋め込まず、版指定されたアプリ成果物として受け取る。
- **Calibration Conditions**: OSイメージ、CPUアーキテクチャ、VM資源、ネットワーク、初期データ、アプリ、Router、時計・乱数設定を版マニフェストに固定する。公式較正は将来の競技相当環境で反復し、ローカルVMは機能検証と作問時の相対比較に限定する。
- **Trial Solving**: 本環境は作問者の開発・事前検証に使用できるが、競技者同等の情報、時間、資源による正式な試し解き条件と完了判定は後続仕様で定める。
- **Observability**: ノード、構成段階、リリース、service health、経路、Router readinessを相関可能な診断として残す。秘密値、任意のHTTP body、非公開課題の内容は通常ログへ出さない。
- **Distribution Safety**: ローカルboxや一時ビルド成果物をそのまま競技配布物とみなさない。クラウドイメージは再生成、適合性検証、秘密・spoiler scan、入力版と出力IDの記録を経て後続の競技配布ゲートへ渡す。
- **Out of Scope**: PostgreSQL以外のDBエンジンへの移行、実ベンチマーカー、負荷・採点、意図的ボトルネック、想定解、公式スコア較正、Portal本番運用、TLS/WAF/VPN、クラウドネットワーク作成、AMI公開・配布を含まない。

## Assumptions

- 今回の設計対象はローカルVMと、将来のAMIへ再利用するゲスト構成境界までであり、AMIの生成・登録・配布自体は後続incrementとする。
- Linuxの必須ローカル経路はVagrant + VirtualBox、ゲスト構成はAnsible、再利用可能なマシンイメージ生成はPackerを使うという依頼者指定を計画上の固定制約とする。
- 対応hostはLinux x86_64だけとする。macOS、Linux arm64、その他のhost OS/architectureは今回のsupport、artifact、release qualification matrixに含めない。
- ローカルの既定資源値は開発者が上書きできる。競技相当の公式資源値は後続仕様が固定する。
- Routerは既存の`specs/003-router`とその必要なHTTP proxyを一体のRouterスタックとして配備し、Portal endpointはtopologyのcontrol pathから導出してruntime設定として与える。
- ローカルPortalは既存`portal-mock/compose.yaml`をexternal-Router modeで起動する。Portalは5台目のVMには数えず、Linux上のDocker EngineとDocker Composeをhost toolとして利用する。
- Clientは独立VMやcontainerとして追加せず、host上のloopback-only forwarded portからRouterへ接続する。
- 現行backendのPostgreSQL URL、pgx、migrationを既存のDB契約として利用し、PostgreSQL 16、schema compatibility ID、migration revisionをrelease manifestへ固定する。
- frontendはビルド済み静的成果物、backendとRouterはCPUアーキテクチャに対応した版固定成果物として配備できるものとする。
