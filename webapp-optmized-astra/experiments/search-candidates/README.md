# ユーザー検索の候補抽出比較

2万件の合成ユーザーを使ったローカルPostgreSQLの比較。実機ベンチスコアではない。
各条件で旧SQLと候補SQLを交互に3回実行した。結果は`measurements.json`。

| 検索条件 | 旧SQLの中央値 | 候補SQLの中央値 |
|---|---:|---:|
| 特定の名前 | 9.673 ms | 0.795 ms |
| 全員に一致する名前 | 45.535 ms | 28.701 ms |
| 1文字の広い検索 | 46.135 ms | 29.468 ms |
| 一致なし | 9.612 ms | 0.465 ms |

アプリで使うcustom planの比較。generic planでは広い検索が悪化する場合があったため、
候補SQLはunnamed statementを使う。GINのpending listを避けるため`fastupdate=off`とした。
書き込み負荷と実機スコアへの影響は別途測定する。

検索候補を索引から抽出し、名前の変更や索引の欠落は同じトランザクションでdirty集合に記録する。
アカウント状態、ブロック関係、キーは検索時にSQLで読む。
Rust側でもUnicode正規化した名前を照合し、51件集まるまで候補を取得する。

ローカルの使い捨てPostgreSQLコンテナで実行する。既存テーブルは変更せず、固有名のスキーマを作成し、終了時に削除する。

```sh
python3 webapp-optmized-astra/experiments/search-candidates/compare.py --container astra-local-assets --output /tmp/search-plans.json
```

検索の比較テストではUnicode、ページ送り、名前変更、削除、ブロック、派生行の欠落・破損・切り捨て、ID変更を検証した。
登録テストでは一意制約と更新失敗時のロールバックを検証した。Rust全テストとClippyが成功。
