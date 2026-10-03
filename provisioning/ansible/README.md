# Ansible

AMI の中身を作ります。Packer（`provisioning/packer`）から呼ばれます。

| playbook | 対象 | 内容 |
|---|---|---|
| `prepare.yml` | localhost | webapp のソース、frontend、datagen、Router、Portal、Envoy を `build/` に作る |
| `problem-server.yml` | `problem_servers` | app1〜3 と db1 で共用。isucon ユーザー、Go、PostgreSQL、nginx、webapp を入れ、初期データを投入する |
| `router.yml` | `routers` | Router、Envoy、検証用 Portal と、その設定 |

AWS のアドレスは固定なので、トポロジー（`group_vars/all.yml`）は AMI に焼き込みます。
起動時に決まるもの（app のリージョン、どのサービスを動かすか、Router の ENI と TLS 証明書）は
Pulumi の user-data が設定します。

## サーバー内の構成

- アプリ: `/home/isucon/webapp/{go,sql,public}`。`isureal-go.service` が `/home/isucon/env.sh` を読んで `:8080` で起動する
- ビルド: `/home/isucon/webapp/go` で `make`。Go は `/home/isucon/local/go`（`~/.profile` で PATH を通す）
- nginx: `:80` で `public/` を返し、`/api/` を `127.0.0.1:8080` へ転送する。Router の Envoy はここへ転送する
- PostgreSQL 16: 全台に入り、初期データも全台にある。接続情報は `isureal` / `isureal` 固定
- イメージでは isureal-go、nginx、PostgreSQL をすべて止めておき、app と db で必要なものを起動時に有効にする

初期データは本番の規模（datagen の `-scale large`）で、ビルド用インスタンス上で datagen の出力をそのまま
psql に流して投入します（seed は十数 GB あるので、ファイルにしません）。
