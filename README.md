# ISUReal

## Docker Composeで起動

### Requirements

 * Docker / Docker Compose

### 起動方法

```bash
docker compose up
```

初回はコンテナをbuildし、PostgreSQLの起動後にmigrationとminimal fixtureの投入を行ってから、API、worker、frontendを依存順に起動します。

- Web: <http://localhost:5173>
- API readiness: <http://localhost:8080/health/ready>
- seed user: `alice` / `password`（`bob`、`carol`、`dave`も同じパスワード）
- seed administrator: `root` / `admin-password`

バックグラウンドで起動する場合は`docker compose up -d`、停止は`docker compose down`です。通常の再起動ではDBと画像のvolumeを保持し、minimal fixtureを再投入しません。ローカルデータも削除して完全に初期化する場合だけ、意図を確認したうえで`docker compose down --volumes`を使用してください。

既定ポートが使用中の場合は、次の環境変数で変更できます。

```bash
ISUREAL_FRONTEND_PORT=15173 ISUREAL_API_PORT=18080 ISUREAL_POSTGRES_PORT=55432 docker compose up
```

## Vagrantでの起動

### Requirements

 * Docker / Docker Compose
 * VirtualBox
 * Vagrant

### 起動・終了方法

```
# 起動
make vm-up
# 終了
make vm-down
```

起動後は http://127.0.0.1:15174/ からポータルに、https://app.team1-local.127.0.0.1.nip.io:18443 からアプリケーションにアクセスする。app1〜3 は共有 PostgreSQL の db1（AP 固定）を利用する。

ベンチマーカーは Vagrant の HTTP Client forward と Router を経由して実行する。
実行すると initialize phase で対象アプリのデータが初期化される。

```bash
make vm-benchmark

# 開発用の固定 1 サイクル
make vm-benchmark BENCH_ARGS='-users 2 -cycles 1'
```
