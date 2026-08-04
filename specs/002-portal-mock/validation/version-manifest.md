# Portal Mock version manifest

**対象タスク**: T079  
**記録日**: 2026-08-04  
**Source revision**: `469944ed5b6e551664b78d294af2a43a27375e71` を起点に並行実装中  
**Manifest status**: 2026-08-04 の最終検証済み作業ツリー

## Contract and generated artifact hashes (SHA-256)

| Artifact | SHA-256 |
|---|---|
| `proto/portal/v1/portal.proto` | `86d21740a45b488129a310a62e22654077f2cfdb4b2006d840deaf5c197efeff` |
| `proto/portal_mock/v1/portal_mock.proto` | `566194b45ea5ba5991db46c7d3cb39da4eb0cfaf2828e7b7bdf2c4673ea48daf` |
| `gen/go/portal/v1/portal.pb.go` | `03170ab7075b05e7ab627457855b43183662d0ab7681130a93396d859d317648` |
| `gen/go/portal/v1/portalv1connect/portal.connect.go` | `76d46960112db12e478eacd61dad20f8156e67d02d513a4df429d843bf285031` |
| `gen/go/portal_mock/v1/portal_mock.pb.go` | `4924dde678a2479360ac7e6e26b58156cee81db621b8d3d6d8b0ec7f1fd26008` |
| `gen/go/portal_mock/v1/portal_mockv1connect/portal_mock.connect.go` | `db091832a2286038e36695c0565f87740bdb84f712aa284f2a6b9bca971d1e8e` |
| `gen/ts/portal/v1/portal_pb.ts` | `112e8372d046707045020a8b08b6b56dd873f38f70a0c559c70b7ec8c43f2832` |
| `gen/ts/portal_mock/v1/portal_mock_pb.ts` | `f5e7932e50a4ddf4055e69e38e31bd85a9eb9ed0758f1c008d210509f6f46f01` |

## Toolchain

| Tool | Pinned version/source |
|---|---|
| Go | 1.26.5 (`mise.toml`) |
| Node.js / npm | 24.14.1 / 11.11.0 |
| TypeScript | 5.9.3 |
| Buf | 1.72.0 |
| protoc | 35.1 |
| protoc-gen-go | 1.36.11 |
| protoc-gen-connect-go | 1.20.0 |
| protoc-gen-es | 2.13.0 |
| Connect-Go | 1.20.0 |
| Connect-ES | 2.1.2 |
| React / Vite | 19.2.8 / 8.1.5 |
| Playwright package | 1.62.1 |

## Test-suite revision

検証対象は `469944ed5b6e551664b78d294af2a43a27375e71` を起点とする本 feature の
2026-08-04 最終作業ツリー。コミット時には本 manifest と実装を同一コミットへ含める。

- proto lint/build/generate-drift/breaking
- Go format/vet/unit/contract/integration/concurrency-race/build
- web lint/typecheck/Vitest/build/Playwright
- independent compose smoke
- secret scan/public artifact audit

## Supported countries v1

| Region | Codes |
|---|---|
| US | US, CA, MX |
| AP | JP, KR, CN, AU |
| EU | GB, DE, FR |

国コードは大文字 ISO 3166-1 alpha-2 string。上記以外と`AP`/`EU`等のregion名は拒否する。

## Mode/profile behavior

| Mode | Profile | Initial/reset applied | Report source |
|---|---|---|---|
| `stand-in` | `success` | desiredと一致 | built-in stand-in |
| `stand-in` | `fail-once-then-success` | desiredと一致。変更ごとにfail→same snapshot success | built-in stand-in |
| `stand-in` | `no-report` | 初期は一致。変更後は旧applied維持 | none after update |
| `external-router` | N/A | 不明/UNSYNCED | external Router only |

## Compatibility boundary

- Router contract package/route: `portal.v1`
- Mock UI/runtime/reset/benchmark package/route: `portal_mock.v1`
- 本文にcontract version fieldを持たず、非互換変更は別package/routeで提供する。
- `ReportAppliedResponse`は空のまま。
- 実Router/競技仕様はこのmanifestの契約hash、国表、report/reset semanticsを参照して引き渡す。
