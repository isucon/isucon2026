# Contracts: 擬似地理分散 Router

**Feature**: `003-router`

## 1. Portal control plane (existing SoT)

Source of truth: `proto/portal/v1/portal.proto`

Router uses:

- `PortalRouterService.StreamDesired(StreamDesiredRequest) returns (stream StreamDesiredResponse)`
- `PortalRouterService.ReportApplied(ReportAppliedRequest) returns (ReportAppliedResponse)`

Rules:

- request TeamKey is the configured positive team number.
- every received `ConfigSnapshot` is validated before side effects.
- success report contains the exact snapshot ID being correlated and the actual active Config.
- failure report contains the correlated snapshot ID and a bounded, non-secret actionable reason.
- reconnect begins a new stream and accepts the latest snapshot regardless of prior process memory.
- no Router-specific field is added to the Portal Mock-only protobuf.

## 2. HTTP data plane

### Listener

- Plain HTTP for this feature.
- Request Host must exactly match one configured app/server hostname.
- Unknown/missing Host: `404` direct response; no upstream request.

### Country header

- Header name: `X-Country-Code` (case-insensitive HTTP field name).
- Valid value: exactly one of US, CA, MX, JP, KR, CN, AU, GB, DE, FR.
- Missing: configured default country is inserted upstream.
- Duplicate, empty, malformed, unsupported: `400` direct response; no upstream request.
- Upstream receives exactly one effective value.

### Host routing

| Host kind | Target |
|---|---|
| configured `app` hostname | current `mainServer` |
| configured `server1` hostname | server1 |
| configured `server2` hostname | server2 |
| configured `server3` hostname | server3 |

No implicit failover is performed. Unavailable selected upstream returns a bounded `502`/`503`/`504` gateway response.

## 3. Controller health

Controller serves an internal HTTP endpoint on the configured status address.

### `GET /healthz`

- `200`: process and reconcile worker are alive.
- response is JSON with `status: "healthy"`.
- independent of Portal/Envoy connectivity.

### `GET /readyz`

- `200`: at least one Portal snapshot is fully committed and the current Envoy stream has ACKed RDS.
- `503`: initial unsynced、Envoy disconnected、または再接続後の RDS 再 ACK 前。
- response is bounded JSON containing snapshot ID, phase, Portal/Envoy connectivity, and non-secret last error.

後続 snapshot の apply/rollback 中または失敗時は、current-stream ACK 済みの last-known-good が利用可能なら `200` を
維持し、phase/error で新しい試行の状態を示す。

The E2E topology exposes these endpoints only to its private networks.

## 4. Fixture contract (E2E only)

The fixture server is not a production API and remains under `router/test-fixtures/`.

- `GET /healthz`: readiness.
- `GET /identity`: server ID and all received country header values.
- `GET /blob?bytes=N`: deterministic `N`-byte response within bounded test maximum.
- `GET /fetch?url=...`: perform one bounded peer HTTP GET and return elapsed time/byte count.
- `GET /route?target=<IPv4>`: return `ip route get` output for route assertions.

Target validation and size bounds prevent the fixture from becoming an unbounded proxy even inside E2E.

## 5. Compatibility

- Portal wire compatibility follows `portal.v1`; breaking changes require a new versioned endpoint/package.
- Envoy xDS is internal to Router deployment and pinned by `router/go.mod`, Docker image, and bootstrap validation.
- HTTP Host/country behavior and country table are public invariants to be pinned by a later competition specification.
