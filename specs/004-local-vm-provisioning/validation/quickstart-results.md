# Quickstart validation results

## Result

- Outcome: **PASS**
- Validated at: `2026-08-06T05:07:59Z`
- Host: Linux `x86_64`
- Guest release: Ubuntu `24.04`, `x86_64`
- Provisioning release: `r2026.08.06.3`
- Environment: `isureal-local`
- Generated conformance report: `provisioning/.state/isureal-local/reports/latest.json`

The four VirtualBox VMs, Portal Compose project, native problem-server stack,
Router/Envoy path, and fixed Client forward were exercised on the local host.
The final `make image-validate` ran 20 tests and passed Packer, Ansible,
preflight, and image-index validation. The final `make vm-validate` completed
with `local-vm conformance: PASS`.

## Host toolchain

| Tool | Observed version |
| --- | --- |
| Docker Engine client/server | `29.7.1/29.7.1` |
| Docker Compose | `5.4.0` |
| Vagrant | `2.4.9` |
| VirtualBox | `7.2.14r174565` |
| Packer | `1.15.4` |
| ansible-core | `2.21.2` |

This validation covers the supported Linux amd64 target only. macOS was not
included in the support or validation matrix.

## Immutable box evidence

The Ubuntu base was built once from the pinned Ubuntu 24.04.4 ISO and reused
without changing its checksum. The corrected `.3` role builds completed in
9m08s for problem-server and 7m11s for Router.

| Box | SHA-256 |
| --- | --- |
| Ubuntu 24.04.4 base | `aec562f21fc1c6eb0c6a6638aa249db39e9d788b00c637a4846a38a19ff84331` |
| problem-server | `f1e5276afd7bb5ae25bf33992a1afac07de3b2815c374d59dd71008f635b1251` |
| Router | `cd39a5bd59f451f36a956000b06ca788ebb470055a21e5d442ac5092a9ba81db` |

All three outputs are recorded under release `r2026.08.06.3` in
`provisioning/.state/images/image-artifact-index.json`.

## Four-VM and NIC evidence

`make vm-status` returned success and reported exactly these four machines as
`running`: `server1`, `server2`, `server3`, and `router`. The conformance report
identified every node as Ubuntu 24.04/x86_64/release `r2026.08.06.3` and ready.

| Node | Data/control addresses | Observed MACs |
| --- | --- | --- |
| server1 | `192.168.60.6/28` | `08:00:27:60:01:01`, `08:00:27:60:01:02` |
| server2 | `192.168.60.22/28` | `08:00:27:60:02:01`, `08:00:27:60:02:02` |
| server3 | `192.168.60.38/28` | `08:00:27:60:03:01`, `08:00:27:60:03:02` |
| Router | `192.168.60.5/28`, `.21/28`, `.37/28`, `.69/28` | `08:00:27:60:04:01`, `.11`, `.21`, `.31`, `.69` |
| Host control adapter | `192.168.60.65/28` | VirtualBox `vboxnet0` |

The three server data adapters attach to distinct VirtualBox internal networks.
Each problem server has routes to the other two data segments only through its
local Router address (`.5`, `.21`, or `.37`). The Router has one directly
connected interface on each data segment and one host-only control interface.

The Router provider configuration exposed only the intended forwards:

- Client: `127.0.0.1:18080/tcp` to Router guest `8080/tcp`
- Router SSH: `127.0.0.1:2204/tcp` to guest `22/tcp`
- Problem-server SSH: `127.0.0.1:2201..2203/tcp`

## Problem-server services

All three problem servers reported the following services as `active`:

- PostgreSQL (`psql 16.14`, Ubuntu package `16.14-0ubuntu0.24.04.1`)
- `isureal-api.service`
- `isureal-worker.service`
- `nginx.service`

Migrations and deterministic initial seed ran against a newly initialized
PostgreSQL 16 cluster on each server. Node-local backend and nginx readiness
checks passed on all three nodes.

## Portal and Router evidence

The environment-scoped Compose project
`isureal-isureal-local-portal` reported both containers healthy:

| Service | Publish | Result |
| --- | --- | --- |
| Portal API | `192.168.60.65:18082 -> 8080/tcp` | `/healthz` returned `ok` |
| Portal UI | `127.0.0.1:15174 -> 5174/tcp` | HTTP 200 and Portal HTML |

`isureal-router.service` and `isureal-envoy.service` passed their process and
Portal convergence checks before external validation began.

## Router-forwarded frontend evidence

The requested Client path was tested without changing `/etc/hosts`:

```console
$ curl --fail --show-error --silent --include \
    -H 'Host: app.team1-local.isureal.test' \
    -H 'X-Country-Code: JP' \
    http://127.0.0.1:18080/
HTTP/1.1 200 OK
server: envoy
content-type: text/html

<!doctype html>
<html lang="ja">
  <head>
    <title>ISUReal</title>
```

The conformance report independently matched the Portal applied hostnames to
the topology contract and recorded status `200` with `frontendMarker: true`
for `app.team1-local.isureal.test` and each of
`server1..3.team1-local.isureal.test`. This proves every Portal-reported Host
uses host Client forward → Router guest `8080` → Envoy → problem-server
nginx/frontend.

## Build-time findings resolved during validation

- SSH host-key regeneration metadata was made durable before role-box capture
  with an explicit final filesystem sync; fresh clones then booted over SSH.
- Runtime MAC rendering was changed from a regex backreference that emitted a
  control character to deterministic byte slicing; all address, route, and
  firewall applications then passed.
- Portal status now obtains actual untruncated container image IDs from
  `compose ps`, so a no-op rebuild cannot make read-only status fail on stale
  image metadata.
- Portal `teamNumber`/`teamHash` and Router/Envoy Host configuration now derive
  from the same topology contract; all four Portal-reported hostnames returned
  HTTP 200 through the Client forward.

The validated environment was intentionally left running for authoring and
manual inspection.
