# Protobuf

Portal 関連の Protobuf 定義を置く。

`portal/v1/portal.proto` は Router と `isureal-router-control` の通信定義です。
サービス名 `PortalRouterService` は既存 Router との通信互換性のため維持しています。
競技用 Web ポータルの API ではありません。

- `StreamDesired`: Router へ設定を配信
- `ReportApplied`: Router から適用結果を受信
- `TeamKey`、`Config`、`ConfigSnapshot`: チームと固定地域配置の定義

実装と配備手順は [router/README.md](../router/README.md) を参照してください。
旧モック専用の `portal_mock/v1` と生成コードは削除しています。

## コマンド

```bash
mise install
mise exec -- buf lint
mise exec -- buf generate
```

`buf.gen.yaml` は `local: protoc-gen-*` のみを使用する。BSR remote plugin／remote module は
使用しない。後方互換検査では比較対象の Git ref から `proto/` をローカルディレクトリへ展開し、
そのパスを指定する。

```bash
BASE_REF="${BASE_REF:-origin/main}"
if git cat-file -e "$BASE_REF:proto" 2>/dev/null; then
  BASE_DIR="$(mktemp -d)"
  trap 'rm -rf "$BASE_DIR"' EXIT
  git archive "$BASE_REF" proto buf.yaml | tar -x -C "$BASE_DIR"
  mise exec -- buf build "$BASE_DIR" -o "$BASE_DIR/base.binpb"
  mise exec -- buf breaking --limit-to-input-files --against "$BASE_DIR/base.binpb"
else
  echo "$BASE_REF has no proto directory; there is no prior API to compare."
fi
```
