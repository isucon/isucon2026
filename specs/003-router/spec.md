# Feature Specification: 擬似地理分散 Router

**Feature Directory**: `specs/003-router/`

**Spec File**: `specs/003-router/spec.md`

**Created**: 2026-08-04

**Status**: Complete

**Specification Stage**: 支援インフラ仕様（擬似地理分散の開発・検証基盤。アプリ基準仕様・競技仕様とは独立）

**Input**: User description: "~/ad-private を参考に router の設計・実装を行なう。docker compose上でネットワークを組みenvoy-proxyとネットワーク共々正常に動作することがe2eテストできる状態となっている。"

## Summary

単一拠点の開発環境で US・AP・EU の3リージョンを再現する Router を提供する。server1〜server3 は互いに
分離された接続区画へ置かれ、相互通信は必ず Router を通る。Router は Portal が配信する望ましい設定へ
継続的に収束し、サーバー配置に応じたサーバー間の遅延・帯域制約と、クライアントの国および接続先サーバーに
応じた HTTP 通信の制約を一貫して適用する。ホスト名によるメイン／個別サーバー選択、国ヘッダーの検証・伝播、
設定反映結果の報告、再接続後の自己回復を含め、隔離されたローカル構成を一つの再現可能な E2E シナリオで検証する。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 国とホスト名に応じたアプリ接続 (Priority: P1)

開発者はクライアントの国を表す要求をチームのアプリ用ホスト名へ送り、Portal で選択されたメインサーバーから
応答を受ける。個別サーバー用ホスト名を使った場合は、そのサーバーから応答を受ける。クライアントの国と
接続先サーバーのリージョンが異なる場合だけ、定義済みの地理的制約が適用される。

**Why this priority**: ベンチマーカーを含む利用者トラフィックが Router を通り、意図したサーバーへ届くことが
擬似地理分散環境の最小価値だからである。

**Independent Test**: 3つの識別可能なサーバーを用意し、同じアプリ用ホスト名へ国を変えて要求する。メイン変更前後の
応答元、個別ホスト名の応答元、同一／異リージョン時の通信特性、および上流が受け取った有効国を確認できる。

**Acceptance Scenarios**:

1. **Given** メイン接続先が server1 である, **When** アプリ用ホスト名へ対応国付きの要求を送る,
   **Then** server1 が応答し、有効な国コードが変更されずにアプリへ渡る。
2. **Given** メイン接続先が server1 である, **When** Portal でメイン接続先を server3 へ変更して反映を待ち、
   同じアプリ用ホスト名へ要求する, **Then** 再起動や接続先変更を要求側で行わず server3 が応答する。
3. **Given** 任意のメイン接続先が設定されている, **When** server2 の個別ホスト名へ要求する,
   **Then** メイン設定にかかわらず server2 が応答する。
4. **Given** 国ヘッダーがない, **When** 要求を送る, **Then** Portal で設定されたデフォルト国が制御と
   アプリへの伝播の双方に使われる。
5. **Given** 形式不正または未対応の国ヘッダーがある, **When** 要求を送る, **Then** 上流へ転送せず入力エラーを返す。
6. **Given** クライアント国と応答サーバーが異なるリージョンにある, **When** 応答を受ける,
   **Then** リージョン間の追加遅延と帯域上限が適用され、同一リージョンでは適用されない。

---

### User Story 2 - 分離されたサーバー間通信 (Priority: P2)

開発者は server1〜server3 を独立に US・AP・EU へ配置し、サーバー同士の通信が必ず Router を経由した状態で、
同一リージョンと異なるリージョンの通信特性を確認する。

**Why this priority**: DB やキャッシュを含むサーバー間通信にも地理的制約がかかることが、本環境固有の設計判断を
生み出すためである。

**Independent Test**: 各サーバーを異なる接続区画へ置き、経路と直接到達不能性を確認した後、配置を同一／異なる
リージョンへ切り替えて双方向通信の遅延と帯域特性を測定できる。

**Acceptance Scenarios**:

1. **Given** 3台が分離された接続区画にある, **When** server1 から server2 または server3 へ通信する,
   **Then** 通信経路は Router を通り、Router を停止すると新規通信は成立しない。
2. **Given** 送信元と宛先が同一リージョンにある, **When** サーバー間通信を行う,
   **Then** Router 固有の追加遅延はなく、同一リージョン用帯域上限が適用される。
3. **Given** 送信元と宛先が異なるリージョンにある, **When** サーバー間通信を行う,
   **Then** 両方向にリージョン間の追加遅延と帯域上限が適用される。
4. **Given** Portal でいずれかのサーバー配置を変更した, **When** 反映完了後に同じ通信を再測定する,
   **Then** コンテナやサーバープロセスを再起動せず新しい配置の制約へ切り替わる。

---

### User Story 3 - Portal 設定への継続的な収束 (Priority: P3)

運用者は Router を Portal の対象チームへ接続し、現在値と以後の変更を自動適用させる。適用の成功または失敗は
Portal へ報告され、通信断やプロセス再起動後も最新設定へ自己回復する。

**Why this priority**: Portal の desired と実ネットワークの applied を一致させ、設定状態を利用者が信頼できることが
継続運用に必要だからである。

**Independent Test**: Portal を external-router モードで起動し、初期同期、連続更新、適用失敗、Portal 再起動、
Router 再起動を順に発生させ、報告状態と実際の通信経路が最新 desired に収束することを確認できる。

**Acceptance Scenarios**:

1. **Given** Portal と Router が起動した, **When** Router が設定購読を開始する,
   **Then** 最新 snapshot を適用し、同じ snapshot ID と実際の適用値を成功報告する。
2. **Given** 複数の desired 更新が短時間に発生した, **When** Router が処理を継続する,
   **Then** 少なくとも最新 snapshot へ収束し、古い処理完了で新しい設定を巻き戻さない。
3. **Given** 設定の一部を安全に適用できない, **When** snapshot を処理する,
   **Then** 成功として報告せず理由を失敗報告し、直前の完全な適用状態を維持する。
4. **Given** Portal との接続が切断された, **When** Portal が復旧する,
   **Then** 人手を介さず再接続し、再配信された最新 snapshot へ収束する。
5. **Given** Router が再起動した, **When** Portal が利用可能である,
   **Then** 初期状態を推測して成功報告せず、Portal から受け取った設定を適用後に ready となる。

---

### User Story 4 - 再現可能な統合検証 (Priority: P4)

開発者はクリーンなローカル環境から単一の検証コマンドを実行し、Portal、Router、HTTP プロキシ、3台のサーバー、
分離ネットワークをまとめて起動して、主要な制御面とデータ面を自動検証できる。

**Why this priority**: 権限を必要とするネットワーク制御と動的プロキシ設定は単体テストだけでは保証できず、
変更ごとに同じ環境で回帰確認できる必要があるためである。

**Independent Test**: 事前に起動済みの対象サービスがない状態で検証コマンドを実行し、構築、health 判定、初期同期、
HTTP 経路、国別制約、サーバー間経路、動的設定変更、再接続の全シナリオが自動判定され、後始末されることを確認できる。

**Acceptance Scenarios**:

1. **Given** 対応するコンテナ実行環境だけがある, **When** E2E 検証を実行する,
   **Then** 必要な全サービスと隔離ネットワークが自動作成され、全シナリオが成功する。
2. **Given** 検証が成功または途中失敗した, **When** 検証処理が終了する,
   **Then** 診断に必要なログを提示し、同じ検証を再実行できるよう専用資源を後始末する。
3. **Given** 実装が接続区画の分離、経路制御、HTTP 動的設定のいずれかを失った,
   **When** E2E 検証を実行する, **Then** 対応するシナリオが失敗し、退行箇所を識別できる。

### Edge Cases

- 対応国であるが空白や小文字を含むヘッダーは正規化せず拒否する。
- 同名ヘッダーが複数指定された場合は曖昧な国判定を避けて拒否する。
- 不明な Host、Host 欠落、規定外ポート付き Host は既定サーバーへ転送せず拒否する。
- 上流サーバーが停止中の場合は有限時間でゲートウェイエラーを返し、別サーバーへ暗黙に切り替えない。
- 同じ snapshot を再受信した場合は冪等に扱い、実状態が一致すれば再適用による通信断を発生させない。
- 古い snapshot の適用処理が新しい snapshot より後に完了しても、最新設定を上書きしない。
- 制御面が停止しても最後に完全適用したデータ面は維持するが、未同期の初回起動を ready としない。
- 制約値が実行環境の能力を超える場合は部分適用せず失敗を報告する。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST support exactly three application server identities, each independently assigned to US, AP, or EU.
- **FR-002**: System MUST place the three server identities in mutually isolated connection segments and MUST route every supported server-to-server path through the Router.
- **FR-003**: System MUST prevent an application server from bypassing the Router to reach another application server's segment.
- **FR-004**: System MUST apply no Router-added latency and a 1 Gbps ceiling to same-region server traffic.
- **FR-005**: System MUST apply 200 ms additional latency and a 200 Mbps per-region ceiling to cross-region server traffic in both directions.
- **FR-006**: System MUST route the team application hostname to the configured main server and each server-specific hostname only to its named server.
- **FR-007**: System MUST determine the effective client country from one valid `X-Country-Code` value, or use the configured default country when the header is absent.
- **FR-008**: System MUST support the same initial country set and country-to-region mapping as the Portal contract: US/CA/MX→US, JP/KR/CN/AU→AP, GB/DE/FR→EU.
- **FR-009**: System MUST reject malformed, duplicated, or unsupported country headers before forwarding a request.
- **FR-010**: System MUST forward exactly one effective `X-Country-Code` value to the selected application server, including when the default was used.
- **FR-011**: System MUST apply no Router-added latency and a 1 Gbps ceiling when effective client and selected server regions match.
- **FR-012**: System MUST apply 200 ms additional latency and a 200 Mbps per-region ceiling when effective client and selected server regions differ.
- **FR-013**: System MUST consume desired configuration through the versioned Portal Router contract using the configured positive team number.
- **FR-014**: System MUST validate every received snapshot completely before changing the active data plane.
- **FR-015**: System MUST make a snapshot externally active only when all required routing, network-control, and HTTP-control changes for that snapshot have succeeded.
- **FR-016**: System MUST report the snapshot ID and actual active configuration after successful application, and MUST report an actionable reason after failed application.
- **FR-017**: System MUST preserve the last completely applied configuration when a later snapshot fails.
- **FR-018**: System MUST reconnect with bounded backoff after control-plane interruption and converge to the latest snapshot without operator action.
- **FR-019**: System MUST make duplicate snapshot processing idempotent and MUST prevent stale work from replacing a newer active configuration.
- **FR-020**: System MUST expose health and readiness separately: health means the process can reconcile; readiness means an accepted Portal snapshot is fully active.
- **FR-021**: System MUST emit structured diagnostics containing snapshot correlation, apply phase, outcome, and retry information without recording secrets or arbitrary request payloads.
- **FR-022**: System MUST provide a reproducible, isolated local topology containing the Portal integration point, Router, HTTP proxy, three distinguishable servers, and a client test runner.
- **FR-023**: System MUST provide an automated E2E suite that proves control-plane convergence, HTTP main/individual routing, default/header country propagation, invalid input rejection, same/cross-region HTTP constraints, Router-only server paths, same/cross-region server constraints, dynamic reconfiguration, and reconnect/restart recovery.
- **FR-024**: System MUST allow the E2E environment to use proportionally smaller delay and bandwidth values while preserving the same classification rules, so timing and throughput assertions remain deterministic and practical.
- **FR-025**: System MUST clean up only the E2E suite's dedicated resources after success, failure, or interruption and MUST preserve useful diagnostics on failure.

### Key Entities

- **Desired Snapshot**: Portal が発行した識別子、発行時刻、および Config の不変な組。
- **Router Config**: 3台のリージョン配置、メインサーバー、デフォルト国からなる検証済み設定。
- **Active Snapshot**: HTTP 制御とネットワーク制御の双方へ完全に反映済みの snapshot と実設定。
- **Server Segment**: 1台のアプリケーションサーバーと Router の対応インターフェースだけが参加する隔離区画。
- **Country Mapping**: 対応国コードを US・AP・EU のいずれかへ一意に写像する版固定データ。
- **Apply Attempt**: snapshot の検証、準備、反映、確認、報告という段階と結果を持つ処理記録。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: クリーンな環境で単一の E2E コマンドを実行すると、全構築と主要シナリオが5分以内に完了し、再実行でも同じ判定になる。
- **SC-002**: Portal の設定変更後10秒以内に applied が desired と一致し、その後の100件の要求がすべて新しいメイン／個別ルーティング結果を返す。
- **SC-003**: テスト用制約値で測定した同一リージョン通信と異リージョン通信の中央値差が、設定した追加遅延の80%以上となる。
- **SC-004**: テスト用帯域値で十分な大きさのデータを転送した際、異リージョン経路の測定速度が設定上限の±35%内に入り、同一リージョン経路より明確に低速となる。
- **SC-005**: 検証対象の6方向すべてのサーバー間経路が Router 経由であり、Router を利用不能にすると6方向すべての新規接続が失敗する。
- **SC-006**: 対応10か国、ヘッダー欠落、形式不正、重複、未対応コードの自動検証が100%期待どおりに分類される。
- **SC-007**: Portal または Router を一度再起動する回復シナリオで、人手なしに30秒以内に最新 desired へ再収束し、最後の完全適用状態より古い設定へ戻らない。

## Application Correctness & Safety Requirements

- **Public Contract & Invariants**: `portal/v1` の TeamKey、Config、snapshot、成功／失敗報告を共有契約とし、
  3サーバー、3リージョン、対応10か国、Router-only 経路、Host と国の決定性、last-known-good を不変条件とする。
- **Required Diagnostics**: snapshot ID、チーム番号、適用段階、設定の非秘密フィールド、成否、再試行回数、
  データ面 readiness を構造化して記録する。秘密値、認証情報、任意の HTTP body は記録しない。
- **Delegated Production Concerns**: 本番 TLS 終端、証明書更新、WAF、一般的な DDoS 防御、高可用化、
  ログ・メトリクスの長期保存、ホスト OS の恒久設定、参加者ログイン制御はデプロイ仕様へ委譲する。
- **Competition Handoff**: 後続の競技仕様は共有 Portal 契約、国対応表、地理制約値、E2E シナリオ、
  Router とプロキシの検証済み版を固定する。
- **Out of Scope**: 実ベンチマーカー、負荷・採点、意図的ボトルネック、想定解、スコア較正、
  複数チーム同居、本番 VPN／物理 NIC 構成、TLS、パケットキャプチャを含まない。

## Assumptions

- 初期対象は Portal Mock が提供する1チームであり、チーム番号と公開ホスト名は起動時設定で与えられる。
- Portal の desired が望ましい設定の Single Source of Truth、Router の active state が applied の
  Single Source of Truth である。
- HTTP は初期版では平文で検証し、TLS 終端はデプロイ仕様へ委譲する。
- 同一リージョンの「0 ms」は Router が追加遅延を設定しないことを意味し、実行環境固有の基礎遅延は含まれる。
- 帯域上限は共有方向ごとの制約であり、厳密な課金・トラフィック計量ではなく通信特性の再現を目的とする。
- ローカル E2E の制約値は分類ロジックを変えずに縮尺変更でき、本番相当値は既定値として保持する。
- `~/ad-private` は reconciliation、権限分離、動的プロキシ設定、ネットワーク設定の冪等化を学ぶ参照であり、
  そのサービス固有の WireGuard、リポジトリ proxy、参加者 firewall、PCAP 機能は移植対象ではない。
