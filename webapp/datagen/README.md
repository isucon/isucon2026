# datagen — ISUReal 初期データ生成ツール

リポジトリ直下の `dataset` モジュールが決定的に作る初期データを、`webapp/sql/002-seed.sql` に
書き出す作問用のツールです。DB は使いません。競技者へ配布するアプリ本体 (`webapp/go`) とは
独立した Go モジュールです。

`002-seed.sql` は生成物なので git では管理しません。Docker Compose や VM で起動する前に、
リポジトリ直下で一度生成してください。

```sh
make datagen
# または
cd webapp/datagen && go run .   # -out で出力先を変えられる
```

同じ `dataset` の設定からは、毎回同じ行と ID が生成されます。bench も同じ `dataset` から
データを再計算するので、`dataset` の設定や生成手順を変えたら seed を作り直してください。
データの規模と分布は [`dataset/README.md`](../../dataset/README.md) を参照してください。

## seed の形式

- 全体を 1 つのトランザクションで投入します。`-scale large` では画像だけで十数 GB になります。
- 各テーブルは `COPY ... FROM stdin` 形式です。`psql -f` や
  `/docker-entrypoint-initdb.d` からそのまま読み込めます。
- `users.profile_image_id` と `image_objects.owner_user_id` は互いを参照しています。そのため
  ユーザーをプロフィール画像なしで入れ、画像を入れたあとで一時テーブル経由で `UPDATE` します。

## 初期データの投入と初期化

履歴は大きいので、`/api/initialize` のたびに入れ直すことはしません。DB を作るときに 1 回だけ
入れ、初期化では負荷走行で変わった分だけを戻します。

| ファイル | いつ流すか | 内容 |
|---|---|---|
| `001-schema.sql` | DB を作るとき | 全テーブルの作り直し |
| `002-seed.sql` | DB を作るとき | 初期データ（このツールの生成物） |
| `003-initial-snapshot.sql` | DB を作るとき | 初期化で戻すための控え。初期データの最後の作成時刻（`initial_data_boundary`）と、ユーザーと関係のテーブルの写し（`initial_*`） |
| `reset.sql` | `/api/initialize`（`init.sh`） | 初期化 |

`reset.sql` は次のように戻します。

- `image_objects` は作成月ごとのパーティション（`image_objects_YYYYMM` と `image_objects_default`）なので、
  名前が `initial_data_boundary` の月より後のパーティションを `TRUNCATE` で空にする。画像は走行 1 回で GB
  単位になり、`DELETE` だと量に比例して時間がかかる（AWS の実測で 1.5 GB に 23 秒）が、`TRUNCATE` は量に
  よらず一瞬で、dead tuple も残らない。そのため初期データはすべて `TRUNCATE` しないパーティションに入り、
  負荷走行の画像は `TRUNCATE` するパーティションに入らなければならない。本番の規模の初期データは
  2026-10-28 の 4 時（UTC）ごろまであるので、10 月だけは 28 日の 6 時で分け、後ろを
  `image_objects_20261028` にしている（`EndDate` を変えるときは注意）。
- 作成時刻が `initial_data_boundary` より後の行（負荷走行で作られた投稿、RealMoji、コメント、
  通知、投稿期間、画像、ユーザー）を消す。画像の `DELETE` はパーティションの絞り込みで境界の月だけを見る。
- セッションを空にし、関係のテーブル（友達申請、友達、フォロー、ブロック）を写しから入れ直す。
- 変更されたユーザーの行を写しに戻す。

投稿、RealMoji、コメントの既存の行は bench が書き換えないので、戻しません。消す行を外部キーで
確かめると、索引のない列では 1 行ごとに表を全件走査するので、初期化の間だけ
`session_replication_role = replica` で外部キーの検査を止めます。この設定には、スーパー
ユーザーか `GRANT SET ON PARAMETER session_replication_role` の権限が要ります（Docker Compose の
`isureal` はスーパーユーザー）。

アプリの作る行の作成時刻が初期データの最後の作成時刻より後であること（時計が初期データより
進んでいること）を前提にしています。

## 開発

```sh
make datagen-test   # dataset と datagen のテスト
```
