# Ansible

AMI の中身を作ります。Packer（`provisioning/packer`）から呼ばれます。

| playbook | 対象 | 内容 |
|---|---|---|
| `prepare.yml` | localhost | webapp と isureal-guard のソース、frontend、datagen、Router、Portal、Envoy を `build/` に作る |
| `problem-server.yml` | `problem_servers` | app1〜3 と db1 で共用。isucon ユーザー、Go、Ruby、Rust、PostgreSQL、nginx、webapp、isureal-guard を入れ、初期データを投入する |
| `router.yml` | `routers` | Router、Envoy、検証用 Portal のバイナリと systemd unit |

同じ AWS アカウントに複数のチームの環境を作るので、アドレスは AMI に焼き込みません。
チームごとに違うもの（app のリージョンと DB の接続先、どのサービスを動かすか、Router の設定ファイル、
ENI、TLS 証明書）は Pulumi の user-data が設定します。

## サーバー内の構成

- アプリ: `/home/isucon/webapp/{go,ruby,rust,sql,public}`。`isureal-go.service`（Go 実装）、`isureal-ruby.service`（Ruby 実装）、`isureal-rust.service`（Rust 実装）のどれかが `/home/isucon/env.sh` を読んで `:8080` で起動する。user-data が有効にするのは Go 実装
- ビルド: `/home/isucon/webapp/go` で `make`。Go は `/home/isucon/local/go`（`~/.profile` で PATH を通す）
- Ruby 実装: `/home/isucon/webapp/ruby` で `make install`（依存 gem を `vendor/bundle` に入れる）。Ruby は `/home/isucon/local/ruby` にソースからビルドする（`~/.profile` で PATH を通す）
- Rust 実装: `/home/isucon/webapp/rust` で `make`。Rust は rustup で isucon ユーザーの既定の場所（`~/.rustup`、`~/.cargo`）に入れる（`~/.profile` で PATH を通す）
- nginx: `:80` で `public/` を返し、`/api/` を `127.0.0.1:8080` へ転送する。Router の Envoy はここへ転送する
- PostgreSQL 16: 全台に入り、初期データも全台にある。接続情報は `isureal` / `isureal` 固定
- 監視プログラム: ソースは `/opt/isureal-guard/src`（`_test.go` を除く）、バイナリは `/opt/isureal-guard/bin/isureal-guard`。
  `isureal-guard.service` が `/home/isucon/env.sh` を読み、`isureal-*.service`（`isureal-guard.service` を除く）のログを
  journalctl で読んで DB の `ip_bans` に BAN を書く。言語の実装を切り替えても、unit 名がこのパターンに合えば設定は要らない
- イメージでは isureal-go、isureal-ruby、isureal-rust、isureal-guard、nginx、PostgreSQL をすべて止めておき、app と db で必要なものを起動時に有効にする

初期データは本番の規模（datagen の `-scale large`）で、ビルド用インスタンス上で datagen の出力をそのまま
psql に流して投入します（seed は十数 GB あるので、ファイルにしません）。
