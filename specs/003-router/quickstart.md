# Quickstart Validation Guide: 擬似地理分散 Router

**Feature**: `003-router`

**Date**: 2026-08-04

## 1. Prerequisites

- Linux host
- Docker Engine + Docker Compose v2
- Go 1.26.5（unit test を host で行う場合）
- Router container に `NET_ADMIN`、E2E fixture に static route 設定用 `NET_ADMIN` を付与できること
- `tc` の HTB/netem と nftables を利用できる kernel

既存の root `compose.yaml` と `portal-mock/compose.yaml` は停止不要。E2E は専用 project、未公開 port、専用 network
だけを使う。

## 2. Static validation

```bash
go -C router test ./...
go -C router vet ./...
docker compose -f router/compose.e2e.yaml config --quiet
docker run --rm \
  -v "$PWD/router/envoy/bootstrap.yaml:/etc/envoy/envoy.yaml:ro" \
  envoyproxy/envoy:v1.32.2 \
  envoy --mode validate -c /etc/envoy/envoy.yaml
```

Expected:

- domain、country、tc/nft render、RDS matrix、ACK/NACK、latest-wins test が成功する。
- Compose と Envoy bootstrap が parse/validate される。

## 3. Full E2E

```bash
router/tests/e2e/run.sh
```

Script behavior:

1. project name `isureal-router-e2e` で image と専用 network を作る。
2. Portal Mock を `external-router`、Router、Envoy、server1〜3 を起動する。
3. Controller `/readyz` と Portal desired/applied 収束を待つ。
4. app/individual Host、country/default/invalid、main 切替を検証する。
5. HTTP same/cross delay と帯域を縮尺値で測る。
6. 6方向の server route が対応 Router IP を next hop にすることを検証する。
7. server internal fetch で same/cross delay と帯域を測る。
8. Portal 再起動後の再接続・初期 desired への収束を検証する。
9. Envoy 単独停止中は readiness が落ち、再起動後は現 stream の RDS 再 ACK を待って復帰することを検証する。
10. Router/Envoy 再起動後の再同期を検証する。
11. Router namespace 停止中は6方向すべての server 間新規接続が失敗し、復旧後に再び成功することを検証する。
12. 成否にかかわらず、失敗時 log/ps を表示してから専用 project を down する。

Expected final line:

```text
router e2e: PASS
```

## 4. Manual inspection during development

失敗時の自動 cleanup を止めて調査する場合:

```bash
ROUTER_E2E_KEEP_ON_FAILURE=1 router/tests/e2e/run.sh
docker compose --project-name isureal-router-e2e -f router/compose.e2e.yaml ps
docker compose --project-name isureal-router-e2e -f router/compose.e2e.yaml logs router envoy portal
```

server1 から server2 への route:

```bash
docker compose --project-name isureal-router-e2e -f router/compose.e2e.yaml \
  exec -T server1 ip route get 172.31.2.10
```

Expected next hop: `via 172.31.1.2`。

Router kernel state:

```bash
docker compose --project-name isureal-router-e2e -f router/compose.e2e.yaml \
  exec -T router nft list table inet isureal_router
docker compose --project-name isureal-router-e2e -f router/compose.e2e.yaml \
  exec -T router tc -s qdisc show
```

Envoy state:

```bash
docker compose --project-name isureal-router-e2e -f router/compose.e2e.yaml \
  exec -T router wget -q -O - http://127.0.0.1:9901/config_dump
```

Cleanup:

```bash
docker compose --project-name isureal-router-e2e -f router/compose.e2e.yaml \
  down --volumes --remove-orphans
```

## 5. Production-value defaults

E2E override を外した Router defaults:

| Class | Additional delay | Bandwidth ceiling |
|---|---:|---:|
| same region | 0 ms | 1 Gbps |
| cross region | 200 ms end-to-end（server間はRTT） | 200 Mbps |

値を変更しても country/server region classification、Portal applied contract、Host routing は変化しない。

## 6. Failure interpretation

- Portal が `FAILED_RETRYABLE`: Router log の `phase` と `snapshot_id` を確認する。
- `/healthz=200`, `/readyz=503`: Portal 未同期、Envoy 未接続、RDS NACK/timeout、rollback 中のいずれか。
- Envoy NACK: Controller log の bounded `xds_error` と Envoy log を確認する。
- server route failure: fixture route、Router local IP/interface discovery、nft forward table を確認する。
- timing only failure: `tc -s class/qdisc` と Envoy bandwidth/fault statsを確認し、分類 miss と host 負荷を区別する。
