# 開発環境（deploy/dev）

3 リージョンのアプリ（`app-asia`, `app-europe`, `app-america`）と共有 DB（`db-asia`）で構成される開発環境です。各アプリ内で nginx（80 番）がフロントエンド配信とリバースプロキシを行い、アプリ（8080 番）へ転送します。アプリは既定で Go 実装（`webapp/go`）です。ホスト側にはポート `18090` が公開されます。

## 基本コマンド

```bash
# 初期データの生成（webapp/sql/002-seed.sql。初回のみ）
make datagen

# 起動
make dev-up

# 動作確認（スモークテスト）
make dev-check

# 停止（DB データを維持）
make dev-down

# 停止（DB ボリュームを破棄して初期状態に戻す）
docker compose down -v
```

## アクセス URL

ポート `18090` 経由でアクセスします。

| URL | 説明 |
|---|---|
| `http://app.127.0.0.1.nip.io:18090` | `X-Country-Code`（`JP`→asia, `DE`→europe, `US`→america）に応じて振り分け |
| `http://asia.127.0.0.1.nip.io:18090` | asia リージョンへ直接アクセス |
| `http://europe.127.0.0.1.nip.io:18090` | europe リージョンへ直接アクセス |
| `http://america.127.0.0.1.nip.io:18090` | america リージョンへ直接アクセス |

Router の設定は、本番の Router AMI と同じく `isureal-router-control` が配信します。
`router-control` コンテナは Router と同じネットワーク名前空間の `127.0.0.1:18082` で待ち受け、ホストには公開しません。

## Ruby 実装で起動

`ISUREAL_WEBAPP_LANGUAGE=ruby` を付けて起動すると、アプリコンテナが Ruby 実装（`webapp/ruby`）になります。Go に戻すときは付けずに起動し直します。

```bash
ISUREAL_WEBAPP_LANGUAGE=ruby make dev-up
```

## Rust 実装で起動

`ISUREAL_WEBAPP_LANGUAGE=rust` を付けて起動すると、アプリコンテナが Rust 実装（`webapp/rust`）になります。Go に戻すときは付けずに起動し直します。

```bash
ISUREAL_WEBAPP_LANGUAGE=rust make dev-up
```

## Python 実装で起動

`ISUREAL_WEBAPP_LANGUAGE=python` を付けて起動すると、アプリコンテナが Python 実装（`webapp/python`）になります。Go に戻すときは付けずに起動し直します。

```bash
ISUREAL_WEBAPP_LANGUAGE=python make dev-up
```

## PHP 実装で起動

`ISUREAL_WEBAPP_LANGUAGE=php` を付けて起動すると、アプリコンテナが PHP 実装（`webapp/php`）になります。Go に戻すときは付けずに起動し直します。

```bash
ISUREAL_WEBAPP_LANGUAGE=php make dev-up
```

PHP 実装のコンテナでは、nginx が `127.0.0.1:8080` でも待ち受け、FastCGI で php-fpm に渡します（`deploy/dev/nginx-php.conf`）。

## Perl 実装で起動

`ISUREAL_WEBAPP_LANGUAGE=perl` を付けて起動すると、アプリコンテナが Perl 実装（`webapp/perl`）になります。Go に戻すときは付けずに起動し直します。

```bash
ISUREAL_WEBAPP_LANGUAGE=perl make dev-up
```

## Node.js 実装で起動

`ISUREAL_WEBAPP_LANGUAGE=nodejs` を付けて起動すると、アプリコンテナが Node.js 実装（`webapp/nodejs`）になります。Go に戻すときは付けずに起動し直します。

```bash
ISUREAL_WEBAPP_LANGUAGE=nodejs make dev-up
```

アプリのイメージは `deploy/dev/app-<言語>.Dockerfile`（`app-go.Dockerfile`、`app-ruby.Dockerfile`、`app-rust.Dockerfile`、`app-python.Dockerfile`、`app-php.Dockerfile`、`app-perl.Dockerfile`、`app-nodejs.Dockerfile`）でビルドします。実装を追加するときは同じ名前で Dockerfile を置き、`ISUREAL_WEBAPP_LANGUAGE` にその言語名を指定します。

## 変更反映と初期化

コードや SQL を変更した場合はアプリコンテナを再ビルドします。SQL 変更時は初期化 API の呼び出しも必要です。初期化は共有 DB 全体をリセットします。

```bash
# アプリの再ビルド
docker compose up -d --build app-asia app-europe app-america

# DB 初期化（共有 DB 全体を初期データへリセット）
curl -X POST -H 'Host: app.127.0.0.1.nip.io' http://127.0.0.1:18090/api/initialize
```

## 補足事項

- **フロントエンド**: Go API に対応。旧実装からの変更点は [frontend/README.md](../../frontend/README.md) を参照してください。
- **リソース・遅延設定**: CPU・メモリ上限やリージョン間遅延（既定 100ms）等の調整は `compose.yaml` を参照してください。
