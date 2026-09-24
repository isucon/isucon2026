# datagen — ISUReal 初期データ生成ツール

`webapp/sql/001-schema.sql` を適用した PostgreSQL にダミーデータを投入し、
その内容から `webapp/sql/002-seed.sql` を生成する作問用のツールです。
競技者へ配布するアプリ本体 (`webapp/go`) とは独立した Go モジュールです。

## 使い方

DB に接続できる状態で、このディレクトリから実行します。

```sh
cd webapp/datagen

# DB にスキーマを適用してダミーデータを投入し、002-seed.sql を書き出す
go run . all

# それぞれ単独でも実行できる
go run . generate   # 001-schema.sql を適用 + COPY でダミーデータ投入
go run . dump       # DB の内容を 002-seed.sql として書き出し
```

接続先は次の順で解決します。

1. `-dsn 'postgres://...'`
2. 環境変数 `ISUREAL_DATABASE_URL`
3. 環境変数 `ISUREAL_DB_HOST` / `ISUREAL_DB_PORT` / `ISUREAL_DB_USER` /
   `ISUREAL_DB_PASSWORD` / `ISUREAL_DB_NAME`(既定値は `webapp/sql/init.sh` と同じ)

### フラグ

| フラグ | 既定値 | 説明 |
|---|---|---|
| `-dsn` | 上記の順で解決 | PostgreSQL 接続文字列 |
| `-schema` | `../sql/001-schema.sql` | 適用するスキーマ |
| `-out` | `../sql/002-seed.sql` | 生成する seed ファイル |

## 処理の流れ

1. **generate** — `001-schema.sql`(DROP + CREATE)を 1 トランザクションで適用し、
   `internal/dataset` が定義したダミーデータを PostgreSQL の
   `COPY` プロトコルで一括投入する。行ごとの INSERT を避けるため高速。
2. **dump** — 各テーブルを外部キーの依存順に読み出し、1 文に複数行を並べた
   INSERT(Bulk Insert)として `002-seed.sql` に書き出す。

生成物は決定的(timestamp は `dataset.FixedTime`、行順は主キー順)なので、
同じ入力からは常に同じ `002-seed.sql` が得られます。

## 現在のバージョン

Version 1 では管理者アカウント 1 件のみを作成します。

| ユーザー名 | パスワード |
|---|---|
| `admin` | `password1234` |

パスワードはアプリと同じく `hex(SHA-256(plain))` で保存されます。

## 出力の読み込み

`webapp/sql/init.sh` は `001-schema.sql` に続けて `002-seed.sql` を適用します。
`002-seed.sql` は単体でも `psql -v ON_ERROR_STOP=1 -f 002-seed.sql` で読み込めます。

## データを追加する

`internal/dataset/dataset.go` の `Build()` にテーブルを追加してください。
`generate` は `COPY`、`dump` はスキーマを動的に調べるため、通常はこのファイルの
変更だけで完結します。

`dump` 側には依存順のテーブル一覧 (`internal/dump/dump.go` の `tableOrder`)が
あります。DB に存在するテーブルがこの一覧に無い場合は、seed からの取りこぼしを
防ぐためエラーになります。

> 注意: `users.profile_image_id` → `image_objects.owner_user_id` は循環参照です。
> 現状はユーザーがプロフィール画像を持たないため問題ありませんが、プロフィール
> 画像を seed に含める場合は、`users` を `profile_image_id = NULL` で投入してから
> `image_objects` を入れ、最後に `UPDATE` で関連付ける必要があります。

## 開発

```sh
go test ./...   # エスケープ処理などのユニットテスト
go vet ./...
```
