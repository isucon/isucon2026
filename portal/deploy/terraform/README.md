# terraform（競技ポータルのさくらのクラウド構成）

isucon2026-portal-infra の terraform と同じ流儀。ゾーンは石狩第 3（`is1c`）。

## 事前準備

- usacloud と terraform をインストールする

```bash
brew install sacloud/tap/usacloud   # または https://github.com/sacloud/usacloud/releases
brew install terraform
```

- usacloud のプロファイルを作る（iqz41186 アカウントの ISUCON プロジェクトの API キー。権限は「作成・削除」）

```bash
usacloud config create --name iqz41186_isucon
```

- `00-main.tf` の `predefined.*.ssh_key_ids` に、コントロールパネル「公開鍵」に登録した鍵の ID を書く
  （初期ユーザ `ubuntu` に入るための鍵。ansible の init role 実行後は `admin` ユーザに切り替わる）
- 初期ユーザの sudo パスワードは環境変数で渡す（リポジトリには置かない。ansible の初回実行でも同じ値を使う）

```bash
export TF_VAR_initial_password='<使い捨てのパスワード>'
export PORTAL_INITIAL_PASSWORD="$TF_VAR_initial_password"
```

## 使い方

develop / production は terraform workspace で切り替える。

```bash
terraform init
terraform workspace select develop   # 初回は terraform workspace new develop
terraform plan
terraform apply
```

作られるもの: app サーバ（Ubuntu 26.04）、パケットフィルタ、エンハンスド LB（Let's Encrypt 付き）、
さくらの DNS `isucon.net` ゾーンの `app-001.contest[-dev]` A レコードと `contest[-dev]` ALIAS レコード。

## state の置き場所

state はリポジトリに入れない（`.gitignore`）。複数人で運用するときは
さくらのオブジェクトストレージを S3 互換 backend にする:

```hcl
terraform {
  backend "s3" {
    bucket                      = "<bucket>"
    key                         = "isucon2026/contest-portal/terraform.tfstate"
    region                      = "jp-north-1"
    endpoints                   = { s3 = "https://s3.isk01.sakurastorage.jp" }
    skip_credentials_validation = true
    skip_region_validation      = true
    skip_requesting_account_id  = true
    use_path_style              = true
  }
}
```
