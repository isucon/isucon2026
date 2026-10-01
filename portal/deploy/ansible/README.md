# 競技ポータルのデプロイ

Ubuntu 26.04 LTS の単一 VM に nginx、Docker Swarm、PostgreSQL 18、Redis 8.2、Go ポータルを配置する。
開発環境は `https://portal-dev.isucon.whywrite.it/`。
クラウド API キーやコンテナレジストリがなくても、SSH と手元の Git checkout からデプロイできる。

## 事前条件

- コントローラ: Git、Python 3.12 以上、uv、VM に接続できる SSH 鍵。
- VM: Ubuntu 26.04、SSH 接続と sudo 権限、外向き HTTPS、DNS、NTP 通信。
- VM / LB / DNS / 証明書 / パケットフィルタは作成済みであること。Ansible はクラウドリソースを作成しない。
- 開発 VM は `157.17.49.200`。`develop/hosts.yml` を使う。本番は別の inventory を用意する。
- SSH ホスト鍵をコンソール等の別経路で照合し、コントローラの `known_hosts` に登録する。ホスト鍵検証は無効にしない。

LB は HTTPS を終端し、HTTP を HTTPS にリダイレクトする。
バックエンドは VM の TCP 80、ヘルスチェックは Host ヘッダ付き `HEAD /site/sha`。
VM の TCP 80 は LB 送信元 `153.125.175.224/27` のみ許可する。
Docker Swarm の公開ポート 8080 は全インターフェースで待ち受けるため、**クラウド側で外部からの 8080 を拒否する**。
5432、6379、Swarm 管理用ポートも外部公開しない。
nginx の送信元信頼範囲は `elb_source_cidrs` で指定する。

## 実行

```bash
cd portal/deploy/ansible
uv sync --frozen
uv run python -m unittest discover -s tests -v
uv run ansible-playbook --syntax-check -i develop/hosts.yml playbook.yml
uv run ansible-playbook -i develop/hosts.yml -K --check playbook.yml
uv run ansible-playbook -i develop/hosts.yml -K playbook.yml
```

`-K` は sudo パスワードの対話入力。Ubuntu の既存 SSH アクセスは維持する。
追加の運用ユーザーは `users` に公開鍵とともに指定する。共有 sudo グループの権限は変更しない。
初回の check mode は未導入のパッケージ・ディレクトリの状態に依存し、完全な適用予測にはならない。
check mode は鍵生成、ビルド、スタック更新を行わない。

ローカルビルドは既定で **コミット済み HEAD** の `portal/` を `git archive` する。
未コミット変更は含まれない。特定コミットへ固定する場合は `app_overrides.revision` を指定する。
イメージ名は `isuxportal:<完全な commit SHA>`。既存の同名イメージがあれば再ビルドしない。
Go ビルドはメモリ目標 1 GiB・並列数 1。2 Core / 4 GiB では初回に時間がかかる。
ビルド時のメモリ目標はプロセス全体のハード上限ではない。

## 設定と秘密情報

共通値は `vars/defaults.yml`、環境別値は `<stage>/group_vars/all.yml`。
上書きには `app_overrides`、`postgres_overrides`、`redis_overrides`、`portal_overrides` を使う。
入れ子も再帰的にマージする。既存の `portal:` 等の直接指定は使用しない。

初回に内部共有鍵、管理者パスワード、DB パスワードを生成し、`/opt/portal/secrets.json`（0600）に保持する。
既存の `/opt/portal/env` がある場合は内部共有鍵と DB パスワードを引き継ぐ。
再実行で鍵は変わらない。ファイル破損時は停止する。
外部連携や日時はこのファイルから取り込まないため、適用前に overrides へ転記する。
既存 DB のパスワードを overrides だけで変更しても DB ロールのパスワードは変わらない。更新は別途計画する。

外部の秘密情報は Git 管理外の Ansible Vault ファイルで渡す。
シェル引数へ秘密値を直接書かない。

```bash
uv run ansible-vault create /安全な保存先/portal-develop.vault.yml
uv run ansible-playbook -i develop/hosts.yml -K --ask-vault-pass \
  -e @/安全な保存先/portal-develop.vault.yml playbook.yml
```

Vault 内の例（実値は [外部連携一覧](../external-integrations.md) を参照）:

```yaml
portal_overrides:
  github:
    client_id: 登録したアプリのID
    client_secret: 登録したアプリの秘密値
```

開催日は **2026年10月31日（土）**。
開始、凍結、終了などの時刻は未設定。
確定後に `portal_overrides.timing` の 7 項目を RFC3339 で指定する。
それまでは `admin_only: true` を維持する。日時の空欄だけでは参加登録を安全に閉じられない。
公開モードへの変更には少なくとも登録開始・終了日時の明示を要求する。
募集上限 `max_teams`、規約 URL、ルール URL、ドキュメント URL も公開前に確定値へ更新する。

## 配置と運用

| 対象 | 配置・動作 |
|---|---|
| PostgreSQL | 18.6、digest 固定、`portal_pgdata18:/var/lib/postgresql` |
| Redis | 8.2.9、digest 固定、AOF、`portal_redisdata82:/data` |
| nginx | 公式 stable APT、1.30.5、署名鍵の fingerprint 照合 |
| Docker | Ubuntu 26.04 の docker.io / docker-buildx / docker-compose-v2 |
| アプリ | portal / worker / notifyloop。Discord bot はトークン設定時のみ |
| 設定 | `/opt/portal/env`、`docker-compose.yml` は 0600、親ディレクトリは 0700 |
| nginx site | `/etc/nginx/conf.d/portal.conf`。内部監視と debug を外部から遮断 |
| バックアップ | `/opt/portal/backup.sh`、UTC 18:30、7 日保持、失敗時に正常ファイルを保持 |

バージョン更新は defaults の変更と検証を経て行う。メジャー更新を自動適用しない。
Docker / nginx の OS パッケージは Ubuntu セキュリティ更新と公式配布元の保守情報も確認する。
ホストのタイムゾーンは UTC に設定する。

適用後は Ansible が nginx 経由の HEAD ヘルスチェックを確認する。
加えて外部 HTTPS、管理者ログイン、SPA アセット、`/debug/` と `/admin/long_running_check` の 404、全サービスの replicas を確認する。

```bash
sudo.ws docker stack services portal
curl -fsSI https://portal-dev.isucon.whywrite.it/site/sha
sudo.ws /opt/portal/backup.sh
```

バックアップは VM ローカルのみ。**本番投入前に別障害領域への暗号化コピーと復元試験が必要**。
DB ダンプには登録者情報が含まれる。`secrets.json`、Vault、Redis のセッション保持方針も含めて保管権限を決める。
復元試験では独立した PostgreSQL 18 へ SQL を投入し、件数・制約・登録データ・ログインを確認する。

既存 PostgreSQL サービスが 18 系・`<stack>_pgdata18` 以外なら停止する。
16 系等からの移行は書き込み停止、旧版で pg_dump、18 の別ボリュームへ restore、件数照合、接続先切替の順で行う。
旧ボリュームを削除しない。ボリューム名だけ変更すると空の DB が作られるため禁止する。
アプリの起動時マイグレーションがあるので、旧イメージへ戻せるかはスキーマ互換性を確認する。

## レジストリ・GitHub Actions を使う場合

`app_overrides.mode: registry` と `app_overrides.image` を設定し、必要なら Vault に `registry.host/username/password` を置く。
`github_deploy_authorized_keys` を設定すると専用 github ユーザーと更新スクリプトを配置する。
ローカルビルドでは GitHub Actions による更新を使わない。
GitHub 側に必要な変数・秘密値は [外部連携一覧](../external-integrations.md) に記載した。
API キーが入手できた場合、従来の `<stage>/inventory.sh` とタグ `class=app` の動的 inventory も利用できる。
