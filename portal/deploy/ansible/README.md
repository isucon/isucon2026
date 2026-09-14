# ansible（競技ポータルのホストセットアップ）

isucon2026-portal-infra の ansible を踏襲。terraform が作ったサーバのタグ（`class=app` など）を
dynamic inventory で拾い、Docker Swarm（単一ノード）にスタック `portal` を配置する。

## 必要なツール

- usacloud（`brew install sacloud/tap/usacloud`）
- Python 3.12+ と [uv](https://docs.astral.sh/uv/)
- 1Password CLI（`op`）。vault のパスワードを環境変数で渡すなら不要

## 事前準備

```bash
usacloud config create --name iqz41186_isucon   # terraform と同じプロファイル
uv sync
./develop/inventory.sh                          # 動作確認（初回はプロファイル切替の確認に y）
```

初回（`-u ubuntu`）の sudo パスワードは terraform に渡したものと同じ値を環境変数で渡す:

```bash
export PORTAL_INITIAL_PASSWORD='<TF_VAR_initial_password と同じ値>'
```

vault のパスワードは `vault-pass.sh` が返す。次のどちらかを設定する:

```bash
export PORTAL_VAULT_OP_PATH='op://<vault>/<item>/password'   # 1Password から読む
export ANSIBLE_VAULT_PASSWORD='...'                            # または直接渡す（CI 等）
```

## 実行

まず `-C`（check）で確認してから流す。terraform 直後の初回だけ初期ユーザ `ubuntu` で入り、
init role が `admin` ユーザを作って `ubuntu` を無効化する。

```bash
# 初回
uv run ansible-playbook --vault-password-file ./vault-pass.sh -u ubuntu -b -i develop/inventory.sh -CD playbook.yml
uv run ansible-playbook --vault-password-file ./vault-pass.sh -u ubuntu -b -i develop/inventory.sh -D playbook.yml

# 2 回目以降
uv run ansible-playbook --vault-password-file ./vault-pass.sh -u admin -b -i develop/inventory.sh -CD playbook.yml
uv run ansible-playbook --vault-password-file ./vault-pass.sh -u admin -b -i develop/inventory.sh -D playbook.yml
```

production は `-i production/inventory.sh`。

## 設定値（group_vars）

- `CHANGEME` の項目は必須。app role の assert で埋め忘れを止める
- 秘密値は vault で暗号化して書く:

```bash
uv run ansible-vault encrypt_string --vault-password-file ./vault-pass.sh '<値>' --name 'password'
```

- `portal.*` は `ISUXPORTAL_*` 環境変数に 1:1 で対応する（`roles/app/templates/env`）。
  意味は `portal/internal/config/config.go` を参照
- `portal.discord.bot_token` が空なら bot サービスを起動しない
- `portal.admin_only: true` で運営限定モード（公開前のリハーサル用）

## 配置されるもの

| パス | 内容 |
|---|---|
| `/opt/portal/env` | portal / worker / bot が読む環境変数（0600） |
| `/opt/portal/docker-compose.yml` | `docker stack deploy` 用（変更があれば handler が再デプロイ） |
| `/etc/nginx/sites-enabled/portal` | LB からの HTTP を swarm ingress（8080）へ。`/admin/long_running_check` と `/debug/` は外から閉じる |
| `/opt/bin/github.sh` | CI が `sudo.ws` 経由で叩くローリング更新スクリプト |
| `/var/backups/portal/` | 日次 pg_dump（7 日分） |
