# 競技機能引き継ぎ用バージョンマニフェスト

記録日: 2026-07-19 (UTC)

この文書は、将来の競技機能が参照する基準アプリの固定点を記録する。ハッシュはSHA-256であり、backendの配布対象binaryは`deploy/backend.Dockerfile`の固定済みGo 1.26.5 builderで`CGO_ENABLED=0`、`-trimpath`を指定して生成したものを採用する。

## 仕様・契約・生成client

| 成果物 | SHA-256 |
|---|---|
| `spec.md` | `5fd243dab7a5de8904580c7c6bdc5b1a894f8ac9ef6435e073afd8dadfdded47` |
| `plan.md` | `4d4780eba2bd96bd1f2f01643355a94dcc5e6ac3ad592c7371002ac59344bb3c` |
| `data-model.md` | `84b6da03ab69bec361b459f1aa6b374bba21ee35021792dced503e78fd607c1d` |
| `contracts/openapi.yaml` | `37362ca5c65ae1077255116a72f7f5f37c9fcff7825df88a9148a3df69579040` |
| `frontend/src/api/schema.d.ts` | `aaa96e2d27e8158fe0273ea7228a2bdd2c49a88b73ba765e7690e2087b326cca` |

OpenAPI lint、request/response conformance、生成client drift検査はいずれもPASS。依存解決入力は`go.mod`=`c483d2ee80cd9f165ee246813eef06d3021f9de7c1a867dd77d5c760266a21a0`、`go.sum`=`506d1332934db5492a9a680be713f42d837f768d41808ad33b7d8885af026c9b`、`package.json`=`68114aea7af2cf4bda1e906929c5234c6018a944c22a3499acfcd7a49c973363`、`package-lock.json`=`cf027b1e569518bc3d5b9fbbf6f457358ac34dfe285b2fb096751fecb0a144f4`である。

## Migration

| version | SHA-256 |
|---|---|
| `000001_migrations.up.sql` | `a5860b7b59edbf0e3641081d9d6564b427093541f78640a9c32d6e9ef7b9814f` |
| `000002_foundation.up.sql` | `0c5e0c68536ba9add54a689e8a92cf31906407573b85bb06935c63de518b0405` |
| `000003_posts.up.sql` | `3d18a464e4822927af003bede93484864ed582c2341735ecd3fa0bed8b5e4a90` |
| `000004_relationships.up.sql` | `8b2fc9d76b12a15cb457d45cf60e6a7c7bfe3db8e30fdfb7e2f8b46a2938c7a9` |
| `000005_interactions.up.sql` | `6289281b4ff6c71cb972776234cf85a505d0cf7a69698785bca169629a5cec4a` |
| `000006_accounts_memories.up.sql` | `b9dca6d1b895bf6b170dfaa35b0ff48c51566c9122d0b6a15fbbe5a5ae886063` |
| `000007_notifications.up.sql` | `8dab5969753422b98236ea318e05ba6746e8689ccb8a51c6887bd53e8f45bb12` |

forward-only適用、失敗時rollback、安全な全reset、clean seed、再seedを検証済み。

## Test suite

`backend/`と`frontend/`配下の`*_test.go`、`*.test.ts`、`*.spec.ts`をpath順に並べ、各ファイルの`sha256sum`出力を再度SHA-256化したsuite digestは`985754a19215d1bcf3b867c1ece6f7756bcd80e249daeedbf1394d78ec349f7d`。

固定CI定義`.github/workflows/ci.yml`は`154f07063566266cf64c477ee018a505ca486352e92a4fb71dca65695059378b`。unit、contract、integration、concurrency、migration、sanitizer isolation、OpenAPI/client drift、frontend unit、9件のPlaywright E2E、build、secret scanを包含し、ローカル同等実行は全件PASSした。

## 配布対象build成果物

backend image: `isureal-backend:validation` = `sha256:60807d723891e564f6d8e43bdc0cbeff4b1668f4f0ee10774dfe1fbc0ecd8f6d`

| `/usr/local/bin/` | SHA-256 |
|---|---|
| `api` | `aa9f78e1e95670789dd6caf9aab2187fde7d45a021304400a82fe69f27c87562` |
| `worker` | `1de2a61397594c4e290e6aa43d9cc8b933c214892d930d888a30d5bed309281a` |
| `image-sanitizer` | `79962574a313fc0b38e7ae36964ae42ba43d053aeb6bf374dfcde10ce82e93b2` |
| `migrate` | `b9199b1c02b61046aa5e6aa25ed110ea6004ccfa9262b8b91fbe50e837de1105` |
| `reset` | `346d4684837e34a06a6140cab979cd9450427956f5a186f6ee2a9af8c721dff9` |
| `seed` | `64a6345335ce63f1c07245a4c94175f5b32cf60b43e845e9f6b7c5f8f925a9f3` |

全6 binaryがimage内で実行可能であることを検査済み。frontend imageはCIと同じ公開VAPID fixtureでbuildした`isureal-frontend:validation`=`sha256:f4c70ae5effff59eac1ca97936e2c75a64a50f6d12306b93b504541383b080d5`。bundleは`index.html`=`76db1b2faad50f4ce08f046845bcff9b309a42f6f37484edbd137caeee83fd5a`、`service-worker.js`=`d9f40a4a0071025328c821bd1f0e16126faa9097ad231adac539d69bdff9bfef`、JS=`29bd74cde8e6c71fd9963467419d3a5bf39517ce99a2a522bd4420cd96e8885f`、CSS=`b04061b0a5577f269f965551215154d56f5065f1e2f68710bb53a3300698c6b1`である。

## Browser固定点

- 解決日: 2026-07-19 UTC
- Playwright: `1.61.1`
- Chrome exact: `149.0.7827.55`
- immutable test image: `mcr.microsoft.com/playwright:v1.61.1-noble@sha256:5b8f294aff9041b7191c34a4bab3ac270157a28774d4b0660e9743297b697e48`

将来の引き継ぎでは、上記digestを変更せずに基準挙動を再確認し、変更が必要な場合は新しい承認済みfeatureで新manifestを採番する。
