# 開発環境（deploy/dev）

3 リージョンのアプリ（`app-asia`, `app-europe`, `app-america`）と共有 DB（`db-asia`）で構成される開発環境です。各アプリ内で nginx（80 番）がフロントエンド配信とリバースプロキシを行い、アプリ（8080 番）へ転送します。アプリは既定で Go 実装（`webapp/go`）です。ホスト側にはポート `18090` が公開されます。

## 基本コマンド

```bash
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
| `http://127.0.0.1.nip.io:18082` | 開発用 Portal API |

## Perl 実装で起動

`ISUREAL_WEBAPP_LANGUAGE=perl` を付けて起動すると、アプリコンテナが Perl 実装（`webapp/perl`）になります。Go に戻すときは付けずに起動し直します。

```bash
ISUREAL_WEBAPP_LANGUAGE=perl make dev-up
```

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
- **リソース・遅延設定**: CPU・メモリ上限やリージョン間遅延（既定 200ms）等の調整は `compose.yaml` を参照してください。
