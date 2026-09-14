# 競技ポータルのデプロイ（さくらのクラウド 石狩第 3 ゾーン）

[isucon2026-portal-infra](https://github.com/isucon/isucon2026-portal-infra)（参加登録ポータルのインフラ）と
同じ流儀で組んである。terraform でサーバと LB を作り、ansible でホストを整え、
GitHub Actions がイメージを push してローリング更新する。

```
deploy/
├── Dockerfile              # portal / worker / bot / notifyloop + SPA を 1 イメージに同梱
├── docker-entrypoint.sh    # CMD の第 1 引数でサービスを選ぶ
├── terraform/              # app サーバ・パケットフィルタ・エンハンスド LB（ACME）・DNS
└── ansible/                # ホスト初期化・docker・nginx・swarm スタック・CI 用 github ユーザ
```

## 構成

```
インターネット ─ https ─▶ エンハンスド LB（TLS 終端・Let's Encrypt・80→443）
                              │ http（接続元は LB の IP 帯のみ許可）
                              ▼
                 app-001.contest[-dev].isucon.net（Ubuntu 26.04）
                   nginx :80 ─▶ 127.0.0.1:8080（swarm ingress）
                   docker swarm stack "portal"
                     ├── portal      :8081（PORTAL_AUTO_MIGRATE=1、start-first 更新）
                     ├── worker      （PostgreSQL の jobs テーブルを消費）
                     ├── notifyloop  （http://portal:8081/admin/long_running_check を 60 秒毎に叩く）
                     ├── bot         （Discord bot token があるときだけ）
                     ├── postgres:16 （volume pgdata、日次 pg_dump を /var/backups/portal に 7 日分）
                     └── redis:7     （volume redisdata、appendonly）
```

- ステージは develop（`contest-dev.isucon.net`）と production（`contest.isucon.net`）。
  FQDN は `terraform/00-main.tf` と `ansible/*/group_vars/all.yml` の両方で持つ
- 選手環境（EC2 / CloudFormation）とアイコンの S3 は AWS のまま。さくら側に持つのはポータル本体だけ
- 単一ホストなので PostgreSQL は HA ではない。ディスク障害に備えて日次ダンプを置いている

## 初回セットアップ

1. **terraform**（[terraform/README.md](terraform/README.md)）
   - `usacloud config create --name iqz41186_isucon`（API キーの権限は「作成・削除」）
   - `00-main.tf` の `ssh_key_ids` に登録済み公開鍵の ID を書く
   - `terraform workspace new develop && terraform apply`
2. **コンテナレジストリ**: `isucon.sakuracr.jp` に push 用（GitHub Actions）と pull 用（サーバ）のアカウントを用意する。
   push 用のパスワードは repo secret `SAKURACR_PASSWORD`、pull 用は group_vars の `docker.registry.password`
3. **イメージ**: Actions の「Build portal image」を手で 1 回動かして `:develop` を push しておく
   （ansible の初回 `docker stack deploy` が pull する）
4. **ansible**（[ansible/README.md](ansible/README.md)）
   - group_vars の `CHANGEME` を埋める（秘密値は ansible-vault）。埋め忘れは app role の assert で止まる
   - GitHub / Discord の OAuth callback は `https://<FQDN>/auth/github/callback` と
     `https://<FQDN>/auth/discord/callback`（`internal/handler/handler.go` のルート参照）
   - 初回は `-u ubuntu`、以降は `-u admin`
5. **デプロイ鍵**: `ssh-keygen -t ed25519` で作った鍵の公開鍵を group_vars の `github_deploy_authorized_keys` に、
   秘密鍵を repo secret `PORTAL_DEPLOY_SSH_KEY` に置く
6. ブラウザで `https://<FQDN>/site/sha` がリビジョンを返せば疎通完了。運営ログインは `/admin/session/new`

## 日常のデプロイ

- main に push → CI 緑 → 「Build portal image」が `:develop` と `:<sha>` を push → 「Deploy portal」が develop を更新
- production は Actions から手動で 2 段階: 「Build portal image」を `tag=production` で実行し、
  「Deploy portal」を `stage=production` で実行する
- ロールバックは `:<sha>` タグを指定して `docker service update --image` するか、
  group_vars の `app.image` を戻して ansible を流す

## ローカルでのイメージ確認

```sh
make image                      # docker build（portal/ がコンテキスト）
docker run --rm -p 8081:8081 --env-file .env isuxportal:local portal
docker run --rm isuxportal:local worker
```

## 未決事項

- PostgreSQL をデータベースアプライアンス（マネージド）に出すかどうか。当面は同居
- `/metrics`（Prometheus）は公開していない。必要なら `PORTAL_METRICS_LISTEN` を設定して
  監視側から到達できる経路を作る
