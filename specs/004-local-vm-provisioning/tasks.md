# Tasks: ローカルVM競技環境

**Input**: Design documents from `/specs/004-local-vm-provisioning/`

**Prerequisites**: `plan.md`, `spec.md`, `research.md`, `data-model.md`, `contracts/`, `quickstart.md`

**Specification Stage**: 競技仕様（作問・検証用の環境プロビジョニング。負荷・採点・競技配布は後続仕様）

**Validation**: 全behavior changeにautomated testを用意し、最終gateではLinux x86_64上の実VirtualBoxで3 problem server + Router、Portal Compose、Client forward、Router経由frontendを検証する。

**Organization**: 共通のmanifest/image/network基盤を先に固定し、User Story 1の4 VM起動をMVPとして、反復運用と将来AMI境界を順に統合する。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 別ファイルで、未完了taskへ依存せず並行可能
- **[Story]**: `spec.md`のUser Story対応
- 全taskに対象pathを明記する

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: provisioning source、生成state境界、安定したMake入口を作る。

- [X] T001 Create the planned `provisioning/` directory skeleton and ignore generated state, image outputs, runtime secrets, and logs in `.gitignore` and `.dockerignore`
- [X] T002 Add pinned Ansible collection/configuration and stable provisioning targets in `provisioning/ansible/requirements.yml`, `provisioning/ansible/ansible.cfg`, and `Makefile`
- [X] T003 [P] Add a reproducible host artifact builder for backend binaries, frontend assets, Router, and Envoy in `provisioning/scripts/build-artifacts.sh`
- [X] T004 [P] Create the static test entry point and shared Python test helpers in `provisioning/tests/static/run.sh` and `provisioning/tests/static/support.py`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: 全storyが共有するversioned inputs、preflight、image pipeline、Portal公開境界を実装する。

**⚠️ CRITICAL**: このphase完了までuser story実装へ進まない。

- [X] T005 [P] Write failing schema and semantic topology/release/preflight tests in `provisioning/tests/static/test_config.py`
- [X] T006 Implement the Linux-amd64 topology and pinned release inputs in `provisioning/config/topology.yml` and `provisioning/config/release.json`
- [X] T007 Implement side-effect-free host/tool/schema/CIDR/port/artifact checks in `provisioning/scripts/preflight.py` and `provisioning/scripts/preflight.sh`
- [X] T008 [P] Define pinned Packer plugins, variables, Ubuntu 24.04.4 ISO builder, and NoCloud Autoinstall in `provisioning/packer/plugins.pkr.hcl`, `provisioning/packer/variables.pkr.hcl`, and `provisioning/packer/local-base/`
- [X] T009 [P] Implement secret-free common image setup and capture cleanup in `provisioning/ansible/roles/common/`, `provisioning/ansible/roles/image_cleanup/`, and `provisioning/ansible/playbooks/image.yml`
- [X] T010 Implement problem-server/router role-box builds and immutable artifact metadata in `provisioning/packer/role-images/` and `provisioning/scripts/manifest.py`
- [X] T011 Parameterize Portal API/UI bind addresses and implement environment-scoped Compose lifecycle in `portal-mock/compose.yaml`, `portal-mock/.env.example`, and `provisioning/scripts/portal-compose.sh`

**Checkpoint**: Linux-amd64 inputs validate without VM side effects, and base/role image definitions are reproducible.

---

## Phase 3: User Story 1 - Linuxで競技トポロジーを起動する (Priority: P1) 🎯 MVP

**Goal**: 3 problem server + Routerを起動し、loopback Client forwardからRouter/Envoy経由でfrontendを表示する。

**Independent Test**: `make vm-up`後、exactly 4 VMとPortal projectがreadyになり、`curl -H 'Host: app.team1-local.isureal.test' http://127.0.0.1:18080/`がfrontend HTMLを返す。各serverは専用data segmentだけを持つ。

### Validation for User Story 1 (MANDATORY) ⚠️

- [X] T012 [P] [US1] Write failing Vagrant topology and fixed-forward contract tests in `provisioning/tests/static/test_vagrant.py`
- [X] T013 [P] [US1] Write failing Ansible service/network template contract tests in `provisioning/tests/static/test_ansible.py`
- [X] T014 [P] [US1] Write the four-node service, Portal, frontend, and network conformance runner in `provisioning/tests/conformance/validate.py`

### Implementation for User Story 1

- [X] T015 [US1] Implement exactly four VirtualBox guests, deterministic MACs, three internal data segments, control host-only network, SSH forwards, and Router Client forward in `provisioning/vagrant/Vagrantfile`
- [X] T016 [US1] Generate the environment-scoped Ansible inventory and runtime variables from Vagrant/topology state in `provisioning/scripts/inventory.py` and `provisioning/scripts/inventory.sh`
- [X] T017 [US1] Implement deterministic guest interface discovery, addresses, routes, IPv4 forwarding, and nftables isolation in `provisioning/ansible/roles/runtime_network/`
- [X] T018 [US1] Deploy PostgreSQL 16, migrations/seed, backend/worker/sanitizer, frontend, nginx, health checks, and runtime secrets in `provisioning/ansible/roles/problem_server/`
- [X] T019 [US1] Deploy Router, Envoy 1.32.2, Portal endpoint, xDS/bootstrap, capabilities, and health checks in `provisioning/ansible/roles/router/`
- [X] T020 [US1] Compose node identity, network, database, application, Router, and conformance roles in `provisioning/ansible/playbooks/runtime.yml` and `provisioning/ansible/playbooks/validate.yml`
- [X] T021 [US1] Implement preflight → artifact build → four-VM boot → Portal start → inventory → Ansible → validation orchestration in `provisioning/scripts/vm.sh` and the `vm-up` target in `Makefile`
- [X] T022 [US1] Run static gates and build/register the Packer base and role boxes, recording immutable outputs in `provisioning/.state/images/image-artifact-index.json`
- [X] T023 [US1] Boot the four VMs and record Router-forwarded frontend, service, Portal, and NIC evidence in `specs/004-local-vm-provisioning/validation/quickstart-results.md`

**Checkpoint**: User Story 1 is usable through one command and the requested Router-to-frontend path is proven on real VMs.

---

## Phase 4: User Story 2 - 安全に反復して診断する (Priority: P2)

**Goal**: DBを暗黙resetせず再provision、status、logs、明示reset、環境限定destroyを行う。

**Independent Test**: sentinel DB rowを作成して同じplaybookを2回適用し、2回目`changed=0`とdata保持を確認する。明示destroyは対象Portal projectと4 VMだけを削除する。

### Validation for User Story 2 (MANDATORY) ⚠️

- [ ] T024 [P] [US2] Write lifecycle confirmation, scope, idempotence, and redaction tests in `provisioning/tests/static/test_lifecycle.py`
- [ ] T025 [P] [US2] Add DB sentinel, second-apply, reboot recovery, and scoped-destroy checks to `provisioning/tests/conformance/validate.py`

### Implementation for User Story 2

- [ ] T026 [US2] Implement idempotent reprovision, read-only status, and bounded redacted logs in `provisioning/scripts/vm.sh` and `Makefile`
- [ ] T027 [US2] Implement confirmation-gated deterministic PostgreSQL/application reset in `provisioning/ansible/playbooks/reset-data.yml`, `provisioning/scripts/vm.sh`, and `Makefile`
- [ ] T028 [US2] Implement exact environment-scoped Portal/VM/state destruction without global pruning in `provisioning/scripts/vm.sh` and `Makefile`
- [ ] T029 [US2] Run repeated provision/reboot/data-preservation/destruction validation and append evidence to `specs/004-local-vm-provisioning/validation/quickstart-results.md`

**Checkpoint**: User Stories 1 and 2 support safe daily authoring iteration.

---

## Phase 5: User Story 3 - 将来の競技イメージへ同じ構成を引き継ぐ (Priority: P3)

**Goal**: local role boxと将来amd64 AMIが同じAnsible guest rolesとrelease contractを使う。

**Independent Test**: Packer validationでVirtualBox role buildsとdisabled `amazon-ebs` source boundaryが同じ`image.yml`を参照し、provider固有network/runtime値がroleへbakeされない。

### Validation for User Story 3 (MANDATORY) ⚠️

- [ ] T030 [P] [US3] Write image-source/role-reuse, manifest-state-transition, and secret/identity exclusion tests in `provisioning/tests/static/test_images.py`

### Implementation for User Story 3

- [ ] T031 [US3] Add the disabled Canonical amd64 `amazon-ebs` source boundary reusing `image.yml` in `provisioning/packer/role-images/amazon-sources.pkr.hcl`
- [ ] T032 [US3] Implement validated-to-promoted immutable metadata transitions in `provisioning/scripts/manifest.py` and the `image-promote` target in `Makefile`
- [ ] T033 [US3] Document shared-role rebuild boundaries and the explicit AWS/AMI handoff in `provisioning/README.md`

**Checkpoint**: All three user stories are independently testable and the future AMI boundary does not fork guest configuration.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: CI、release qualification、traceabilityを仕上げる。

- [ ] T034 [P] Add schema, Packer, Vagrant, Ansible, Compose, and Python static gates to `.github/workflows/provisioning-ci.yml`
- [ ] T035 Run all static/application/Router checks and remove secret, wildcard-publish, mutable-artifact, and generated-state findings in `provisioning/tests/` and tracked provisioning files
- [ ] T036 Perform three consecutive create/reboot/validate/destroy cycles on Linux x86_64 and record timings/flakes in `specs/004-local-vm-provisioning/validation/quickstart-results.md`
- [ ] T037 Audit FR-001〜FR-030 and SC-001〜SC-008 against source, tests, and real runtime evidence in `specs/004-local-vm-provisioning/validation/traceability.md`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: 依存なし。
- **Foundational (Phase 2)**: Setup後。全user storyをblock。
- **US1 (Phase 3)**: Foundational後。実行可能な4 VM MVP。
- **US2 (Phase 4)**: US1 lifecycle/runtime stateへ依存。
- **US3 (Phase 5)**: Foundational image rolesへ依存し、US1/US2とはruntime上独立。
- **Polish (Phase 6)**: US1〜US3完了後。

### User Story Dependency Graph

```text
Setup -> Foundation -> US1 four-VM/frontend -> US2 safe iteration -> Qualification
                    \-> US3 shared AMI boundary -----------/
```

### Within Each User Story

- 対応するvalidation taskを先に作成し、実装前に失敗を確認する。
- canonical config → rendered provider/guest state → side effect → conformanceの順に実装する。
- destructive commandはexact environment identityと確認値を検証してから実行する。
- story checkpointで該当static/integration testを通してから次へ進む。

### Parallel Opportunities

- T003〜T004、T005/T008/T009、T012〜T014、T024〜T025は別ファイルで並行可能。
- US3のstatic contractはUS2 runtime validationと独立して実行可能。
- T034はruntime qualificationと独立して作成できる。

## Parallel Example: User Story 1

```text
Task T012: provisioning/tests/static/test_vagrant.py
Task T013: provisioning/tests/static/test_ansible.py
Task T014: provisioning/tests/conformance/validate.py
```

## Implementation Strategy

### MVP First

1. Setup + Foundationを完了する。
2. US1のtestsを先に失敗させる。
3. Packer/Ansible/Vagrant/Portal/Routerを統合する。
4. 実VirtualBoxで4 VMとRouter経由frontendを検証する。

### Incremental Delivery

1. versioned configとside-effect-free preflight
2. reproducible base/role images
3. four-VM networkとapplication/Router runtime
4. safe reprovision/reset/destroy
5. shared AMI boundary
6. repeated qualificationとtraceability

## Notes

- local supportはLinux x86_64/VirtualBox/amd64だけとし、未対応providerへfallbackしない。
- Portalは4 VM外のenvironment-scoped Docker Compose projectで、APIはcontrol IP、UIはloopbackへだけpublishする。
- Client application entryはRouter NATへの`127.0.0.1:18080` fixed forwardだけとする。
- 公式score、load、意図的bottleneck、trial solving、AMI build/register/shareは後続仕様へ渡す。

## Phase 7: Convergence

- [X] T038 [US1] Make the Portal-calculated app/server hostnames and Router/Envoy accepted Host values derive from one topology contract, then add static and runtime regression checks proving every Portal-reported hostname routes through the Client forward per FR-003, FR-017, and SC-003 (partial) in `provisioning/config/topology.yml`, `provisioning/scripts/portal-compose.sh`, `provisioning/ansible/roles/router/templates/router.env.j2`, `provisioning/tests/static/`, and `provisioning/tests/conformance/validate.py`
