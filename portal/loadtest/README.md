# 履歴が蓄積したポータルの負荷試験データ

`cmd/loadfixture` は、8時間に1000チームがベンチマークを繰り返した状態を PostgreSQL 用の SQL として生成します。
履歴一覧、詳細、通知、ランキングを読む際の負荷を調べるためのデータです。
実際のベンチマーカーや外部サービスへの通信は発生しません。
HTTP の負荷を与える処理は含みません。

## 実行頻度とデータの構成

頻度は依頼で指定された「各チームが3〜5分に1回」を採用しています。
初回の投入時刻は開始後0秒以上5分未満、次回までの投入間隔は毎回3〜5分の一様乱数で決めます。
間隔は投入から次の投入までの時間です。
キュー待ちは1〜10秒、実行は60〜120秒とし、同じチームの実行が重ならないようにしています。
終了時刻までに完了する履歴だけを保存します。

以下は試験用の仮定であり、過去大会の実測分布ではありません。

- 各チームに選手1〜3人とインスタンス3台を登録します。
- 失敗率はチームごとに変え、約35%のチームには競技時間の6〜16%にわたって失敗しやすい期間を設けます。失敗時のスコアは0です。
- スコア推移は「ほぼ停滞」「段階的改善」「序盤改善後に停滞」「終盤に急伸」「悪化（復旧する場合としない場合）」「不安定」の6種類をチームごとに無作為に選びます。改善や悪化の時刻と変化率も個別に決め、変更のない期間は同じ水準を保ちます。
- 成功時には水準に対する±2%の計測揺れと0〜1%の減点を加えます。
- 結果ごとに既定で2048バイトの標準出力を保存します。単一文字列の反復で圧縮率が極端に高くならないよう、リクエスト識別子とレイテンシーを変えます。
- 完了時に全メンバーへの protobuf 通知を作り、終了前10分間の通知は未読にします。
- チームの最高結果への参照3種類と、最新実行に対応する言語回答を保存します。
- ランキングの凍結は競技時間の7/8地点（既定では開始7時間後）です。

既定値の `seed=20261031` では次の件数になります。
同じ引数とコードからは同じ SQL が生成されます。

| 対象 | 件数 |
|---|---:|
| チーム | 1,000 |
| 選手 | 2,030 |
| インスタンス | 3,000 |
| ベンチマークジョブと結果 | 各119,538 |
| チームごとの実行 | 115〜127 |
| 失敗結果 | 14,496 |
| 通知 | 242,707 |
| 言語回答 | 1,000 |

既定 seed の内訳は、停滞190チーム、段階的改善154チーム、序盤改善後の停滞150チーム、終盤の急伸194チーム、悪化154チーム、不安定158チームです。
推移の選択とスコア用の乱数は実行スケジュールから独立しています。

`-scores-only` を直接使う場合は、元の `-start`、`-seed`、`-id-offset` を指定してください。
既存のジョブとID、チーム、投入時刻が一致しない場合は更新を拒否します。

## SQL の生成と投入

Go と PostgreSQL の `psql`、`createdb` を用意し、`portal/` で実行します。
SQL は約294 MiBです。
標準エラーには件数と時刻を JSON で出力します。

```sh
cd portal
go run ./cmd/loadfixture > /tmp/portal-loadfixture.sql 2> /tmp/portal-loadfixture.json
cat /tmp/portal-loadfixture.json

# portal/compose.yaml の PostgreSQL に専用 DB を作成する例
createdb -h 127.0.0.1 -p 15432 -U isuxportal portal_loadtest
export PGDATABASE='postgres://isuxportal:isuxportal@127.0.0.1:15432/portal_loadtest?sslmode=disable'
psql -X "$PGDATABASE" -v ON_ERROR_STOP=1 \
  -f internal/db/schema.sql -f /tmp/portal-loadfixture.sql
```

生成 SQL は既定では投入対象テーブルが空であることを確認し、1トランザクションで投入します。
`-id-offset N` を指定すると各IDと参照に N を加算し、既存IDが N 以下の場合に追加投入できます。
`-start recent` は生成時点までの過去8時間（`-duration` で変更可能）を指定します。
既存データがある場合はエラーで終了します。
再投入する場合は別の空の DB を作成してください。
投入後は採番シーケンスを更新し、`ANALYZE` を実行します。

時刻やログ量を変更する場合は引数を指定します。
以下の競技時刻はデータセット用の設定であり、本番開催時刻の指定ではありません。

```sh
go run ./cmd/loadfixture \
  -teams 1000 -duration 8h -seed 42 \
  -start '2026-10-31T10:00:00+09:00' -log-bytes 8192 \
  > /tmp/portal-loadfixture-large.sql 2> /tmp/portal-loadfixture-large.json
```

## ポータルへの接続と確認

試験用ポータルの `ISUXPORTAL_DATABASE_URL` を投入先 DB に向けます。
Redis も既存環境とは別の空の DB を指定してください。
SQL の投入では Redis キャッシュは生成しません。

既定データの競技時刻に合わせる場合は次を指定します。

```sh
export ISUXPORTAL_DATABASE_URL="$PGDATABASE"
export ISUXPORTAL_TIMING_CONTEST_START='2026-10-31T10:00:00+09:00'
export ISUXPORTAL_TIMING_CONTEST_FREEZE='2026-10-31T17:00:00+09:00'
export ISUXPORTAL_TIMING_CONTEST_END='2026-10-31T18:00:00+09:00'
```

選手への代理ログインには `/admin/impersonate` を使います。
チーム ID が `N` の代表者の contestant ID は `3*(N-1)+1` です。
GitHub や Discord の識別子は架空の値です。
通常の OAuth ログイン用アカウントとしては使えません。

負荷試験では選手ごとに履歴一覧と詳細を開き、通知と観客ランキングも対象にします。
履歴の蓄積による影響を見るには、少量データの場合と同じリクエスト条件で応答時間、エラー率、DB の CPU と I/O を比較してください。
ダッシュボードには Redis の事前生成キャッシュが使われるため、キャッシュ生成時の負荷と生成後の配信負荷を分けて測定します。
テストモードのポータルでは `POST /debug/update_contestant_dashboard?team_id=1&frozen=1` でキャッシュを生成できます。
凍結後の選手画面では対象チームごとに生成してください。
競技時刻によるガードを確認するときは、試験用の開始日時を合わせるか、既存のテストモードの時刻ヘッダーを使用します。

投入件数とチームごとの分布は SQL でも確認できます。

```sql
SELECT count(*) FROM teams;
SELECT count(*) FROM benchmark_results;
SELECT min(n), max(n), avg(n)
FROM (SELECT team_id, count(*) n FROM benchmark_jobs GROUP BY team_id) counts;
SELECT pg_size_pretty(pg_database_size(current_database()));
```

## 自動検証

```sh
go test ./internal/loadfixture -v
ISUXPORTAL_TEST_DATABASE_URL="$PGDATABASE" go test ./internal/loadfixture -run TestSQLImport -v
```

単体テストでは再現性、3〜5分間隔、初回の分散、実行の非重複、8時間以内の完了、成功と失敗のスコアを確認します。
結合テストは専用の一時スキーマを作成して実際に `psql` で取り込み、参照関係、ログ量、通知のデコード、本体のランキング集計との一致、採番、再投入の拒否を確認します。
テスト終了時に一時スキーマを削除します。
