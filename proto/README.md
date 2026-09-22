# Protobuf

Portal 関連の Protobuf 定義を置く。

```text
proto/
├── portal/
│   └── v1/
│       └── portal.proto
└── portal_mock/
    └── v1/
        └── portal_mock.proto
```

## `portal/v1`

Routerとの通信に使用する定義。

- Router への desired 配信
- Router からの applied 報告
- TeamKey、Config、Router向けConfigSnapshot

## `portal_mock/v1`

モックポータルのUIとCIで使用する定義。

- 状態取得と設定変更
- 対応国の取得
- ホスト名などの計算済み表示値
- applied、収束状態、診断情報
- Router のスタンドイン設定
- ベンチマークシミュレーション
- 状態の初期化

契約の詳細なstream、エラー、TeamKey検証、同値更新no-op、診断回復の規則は
`specs/002-portal-mock/contracts/README.md`を参照する。

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
  git archive "$BASE_REF" proto | tar -x -C "$BASE_DIR"
  mise exec -- buf breaking --against "$BASE_DIR/proto"
else
  echo "$BASE_REF has no proto directory; there is no prior API to compare."
fi
```
