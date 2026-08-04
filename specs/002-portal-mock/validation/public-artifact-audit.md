# Public artifact audit

**対象タスク**: T078  
**監査日**: 2026-08-04  
**状態**: 静的設計監査とcontainer build済み。最終tracked tree/gitleaks後に再実行する

| 対象 | 検査方法 | 現在の判定 |
|---|---|---|
| tracked source | `git ls-files`、禁止語/秘密候補検索、gitleaks CI | ダミーhash/password例だけを許容し、実secretは保持しない |
| generated Protobuf | `buf generate`後`git diff --exit-code -- gen/go gen/ts` | ローカルpluginのみ。手編集禁止 |
| Go/Web artifacts | `git status --ignored`、`.gitignore`、container stage確認 | binary/dist/node_modules/reportを配布追跡しない |
| container contexts | Dockerfileの明示COPY、`.dockerignore`、image history確認 | root contextだがserver/web/gen以外をruntime imageへコピーしない |
| examples/docs | hostname/hash/token/個人情報の目視監査 | `abc123`等の明示ダミーだけ。個人名・mail・実tokenなし |
| real benchmark | RPC、README、source tree検索 | UI simulationだけ。実bench通信/採点/負荷なし |
| spoilers | bottleneck/solution/score calibration検索 | 公開仕様の対象外説明だけ。非公開台帳・想定解なし |
| BSR dependency | `buf.yaml`, `buf.gen.yaml`, lock/import、network log | remote plugin/moduleなし。mise固定local plugin |
| generated drift | proto CI generate + diff | CI gateあり |
| dependency/tool pin | mise、go.mod、package-lock、Docker base、Actions SHA | Playwright package/image整合をCIで検証し、CI imageはmulti-arch digest固定 |

## 2026-08-04 partial audit result

- Server image: build成功。runtime user=`portal:portal`、read-only composeでhealth成功。
- Web image: build成功。nginx syntax成功、read-only composeでhealthとConnect proxy成功。
- Compose: project/network名は`portal-mock`、DB/named volume/ISUReal service参照なし。
- Buf: `buf lint` / `buf build` 成功。
- ドキュメント例: `abc123` は明示的な非秘密ダミーで、実認証情報は追加していない。

## 最終実行コマンド

```bash
git status --short --ignored
git ls-files -z | xargs -0 grep -InE \
  '(BEGIN (RSA|OPENSSH|EC) PRIVATE KEY|api[_-]?key|secret|password|token)' || true
manifest_dir="$(mktemp -d)"
cp gen/go/go.mod "$manifest_dir/gen-go.mod"
cp gen/ts/package.json gen/ts/package-lock.json "$manifest_dir/"
mise exec -- buf generate
cp "$manifest_dir/gen-go.mod" gen/go/go.mod
cp "$manifest_dir/package.json" gen/ts/package.json
cp "$manifest_dir/package-lock.json" gen/ts/package-lock.json
rm -rf "$manifest_dir"
git diff --exit-code -- gen/go gen/ts
docker build -f portal-mock/server/Dockerfile .
docker build -f portal-mock/web/Dockerfile .
docker history --no-trunc <server-image>
docker history --no-trunc <web-image>
```

## 許容する公開例

- `PORTAL_TEAM_HASH=abc123`: ホスト名説明用の非秘密ダミー。
- `local-development-only`: 本体CI等の明示ローカル値（Portal MockはDBを使用しない）。
- 契約hash/commit hash: 完全性識別子であり秘密情報ではない。

## 未解決/フォローアップ

- 最終commitでgitleaks、生成差分、image内容を再監査する。
- Playwright imageはpackage `1.62.1` に合わせ、multi-arch digest
  `sha256:dcc5531e97840b9b5e794f2814476b21571c5124a3fca2267d73041f56e7580e`へ固定した。
