# Quickstart Validation Results: 擬似地理分散 Router

**Feature**: `003-router`

**Validated at**: 2026-08-04 09:52〜09:56 UTC

**Result**: PASS

## Validation context

- Branch: `feature/router`
- Baseline commit: `14a8a0c210a2bcec574aa1cb5db2784a5545ef12`
- Source under validation: baseline commit plus the uncommitted `003-router` working-tree changes（commit は依頼範囲外のため未作成）
- OS/kernel: Linux 6.8.0-136-generic x86_64
- Go: go1.26.5 linux/amd64
- Docker Engine: client/server 29.7.1
- Docker Compose: v5.4.0
- Envoy: `envoyproxy/envoy:v1.32.2`

検証前から起動していた project `isureal` と `portal-mock` は停止・再作成されず、検証後も稼働している。

## Static gates

2026-08-04 09:56 UTC に最終 working tree で次を実行した。

| Command | Result | Evidence |
|---|---|---|
| `make router-check` | PASS | `go vet ./...`、`go test ./...`、Router build、Compose parse、Envoy validate が exit 0 |
| `go -C router test -race ./... -count=1 -timeout=2m` | PASS | 全 package が race detector 付きで exit 0（09:52 UTC、約1.90秒） |
| `test -z "$(gofmt -l router)"` | PASS | 未整形 Go file なし |
| `bash -n router/tests/e2e/run.sh` | PASS | shell syntax error なし |
| `git diff --check` | PASS | whitespace error なし |

Envoy bootstrap は listener 1、static cluster 4（xDS + server1〜3）を読み、
`configuration '/etc/envoy/envoy.yaml' OK` となった。private `172.16.0.0/12` を明示したため deprecated/default 警告もない。

## Clean Compose E2E

最終 working tree に対し、同じコマンドを連続して2回実行した。

```bash
/usr/bin/time -f 'router_e2e_elapsed_seconds=%e' router/tests/e2e/run.sh
/usr/bin/time -f 'router_e2e_repeat_elapsed_seconds=%e' router/tests/e2e/run.sh
```

| Run | Result | Elapsed | Final line |
|---|---|---:|---|
| 1 | PASS | 71.68 s | `router e2e: PASS` |
| 2 | PASS | 68.98 s | `router e2e: PASS` |

両方とも5分上限内で、毎回 project `isureal-router-e2e` を空の状態から build/up し、終了時に container、network、volume を
cleanup した。終了後の `docker compose ... ps --all --quiet` は空だった。

### Data-plane measurements

| Assertion | Run 1 | Run 2 | Gate |
|---|---:|---:|---|
| HTTP same median | 1.099 ms | 0.936 ms | observation |
| HTTP cross median | 122.940 ms | 122.217 ms | same との差が96 ms以上 |
| HTTP cross transfer | 2.836 s / 1,109,306 bps | 2.838 s / 1,108,519 bps | 1 Mbps ±35% |
| Peer cross transfer | 2.784 s / 753,287 bps | 2.780 s / 754,371 bps | 1 Mbps ±35% |
| Peer same transfer | 21 ms | 21 ms | cross より750 ms以上高速 |

E2E override は same 0 ms/100 Mbps、cross 120 ms/1 Mbps。HTTP delay は3回ずつ測定した中央値で、両 run とも
cross−same が追加 delay の80%（96 ms）以上だった。HTTP/peer cross rate はともに設定上限の±35%内だった。

### Functional assertions

- app Host と server1〜3 individual Host が期待 server へ到達。
- 対応10か国すべての `X-Country-Code` が1値のまま upstream へ到達。
- header 欠落時に Portal default country を挿入。
- 未対応 code と duplicate header は `400`、unknown Host は `404` で upstream へ送らない。
- main server/default country/server region の変更が約0.20秒で desired=applied へ収束。
- dynamic update 後の連続100要求がすべて新 main server と default country を返す。
- server1〜3 間の6方向すべてで `ip route get` の next hop が対応 Router local IP。
- Router 稼働中は6方向すべて疎通し、Router/Envoy 停止中は6方向すべての新規接続が timeout。

### Recovery assertions

| Injection | Observed recovery | Gate/result |
|---|---:|---|
| Portal restart | 約6.1〜6.6 s | 30秒以内、desired=applied、HTTP/peer PASS |
| Envoy-only stop/start | 約6.1〜6.2 s | 停止中 `/readyz=503`、再 ACK 後 `200` |
| Router + Envoy stop/start | 約8.3 s | 30秒以内、Portal snapshot を再適用後 ready |
| Router namespace stop after six probes | 6方向すべて timeout | bypass なし |
| Final Router + Envoy recovery | PASS | HTTP/peer path を再確認 |

## Cleanup and diagnostics

- success: exact project の container/network/volume を cleanup。
- failure/interrupt: `ps` を stderr へ出し、全 service log を `router/.e2e-artifacts/compose.log` へ保存してから cleanup。
- `ROUTER_E2E_KEEP_ON_FAILURE=1`: failure 時だけ専用 project を保持可能。
- runner container に Docker socket は mount していない。stop/restart は host orchestration のみが行う。

以上により quickstart の unit/vet/bootstrap/Compose/full-E2E gate はすべて PASS した。
