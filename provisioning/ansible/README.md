# Ansible

AMI の中身を作ります。Packer（`provisioning/packer`）から呼ばれます。

| playbook | 対象 | 内容 |
|---|---|---|
| `prepare.yml` | localhost | webapp と isureal-guard のソース、frontend、datagen、Router、Portal、Envoy を `build/` に作る |
| `problem-server.yml` | `problem_servers` | app1〜3 と db1 で共用。isucon ユーザー、Go、Ruby、Rust、PHP、PostgreSQL、nginx、webapp、isureal-guard を入れ、初期データを投入する |
| `router.yml` | `routers` | Router、Envoy、検証用 Portal のバイナリと systemd unit |

同じ AWS アカウントに複数のチームの環境を作るので、アドレスは AMI に焼き込みません。
チームごとに違うもの（app のリージョンと DB の接続先、どのサービスを動かすか、Router の設定ファイル、
ENI、TLS 証明書）は Pulumi の user-data が設定します。

## 配布するソースから取り除くもの

`prepare.yml` は webapp と isureal-guard のソースを tar で固めたあと、`scripts/sanitize_archive.py` で
次のものを取り除きます。すべての言語の実装に同じ規則を使うので、実装ごとの設定は要りません。

- テスト: `test/`、`tests/`、`testdata/`、`t/`、`spec/`、`__tests__/` の各ディレクトリの中身と、
  `*_test.go`、`*_test.rb`、`*_spec.rb`、`test_*.py`、`*_test.py`、`conftest.py`、`*Test.php`、`*.t`、
  `*.test.{ts,js}`、`*.spec.{ts,js}`
- `[作問]` コメント: 行コメント（`//`、`#`、`--`）の直後に `[作問]` を書いたもの。コメントだけの行は消し、
  コードの後ろに書いたものはコメントの部分を切り落とす。消した跡が残らないよう、前後の空行は 1 行にまとめ、
  書き換えた Go のファイルは gofmt で整える（コントローラーの PATH に `gofmt` が要る）

取り除いたあとも「作問」という文字列が残るファイル（ブロックコメントや docstring に書いたものなど）があれば、
ファイル名と行番号を出して `prepare.yml` が失敗します。規則のテストは次で実行します。

```sh
python3 -m unittest discover -s provisioning/ansible/tests -p 'test_sanitize_archive.py'
```

## サーバー内の構成

- アプリ: `/home/isucon/webapp/{go,ruby,rust,php,sql,public}`。`isureal-go.service`（Go 実装）、`isureal-ruby.service`（Ruby 実装）、`isureal-rust.service`（Rust 実装）、`isureal-php.service`（PHP 実装の php-fpm）のどれかが `/home/isucon/env.sh` を読んで起動する。PHP 実装以外は `:8080` で待ち受ける。user-data が有効にするのは Go 実装
- ビルド: `/home/isucon/webapp/go` で `make`。Go は `/home/isucon/local/go`（`~/.profile` で PATH を通す）
- Ruby 実装: `/home/isucon/webapp/ruby` で `make install`（依存 gem を `vendor/bundle` に入れる）。Ruby は `/home/isucon/local/ruby` にソースからビルドする（`~/.profile` で PATH を通す）
- Rust 実装: `/home/isucon/webapp/rust` で `make`。Rust は rustup で isucon ユーザーの既定の場所（`~/.rustup`、`~/.cargo`）に入れる（`~/.profile` で PATH を通す）
- PHP 実装: `/home/isucon/webapp/php` で `make install`（Composer で `vendor/` に入れる）。PHP は `/home/isucon/local/php` にソースからビルドし、Composer も `/home/isucon/local/php/bin` に置く（`~/.profile` で PATH を通す）。nginx の `:8080` の設定（`/etc/nginx/sites-available/isureal-php.conf`）は置くだけで、有効にしない
- nginx: `:80` で `public/` を返し、`/api/` を `127.0.0.1:8080` へ転送する。Router の Envoy はここへ転送する
- PostgreSQL 16: 全台に入り、初期データも全台にある。接続情報は `isureal` / `isureal` 固定
- 監視プログラム: ソースは `/opt/isureal-guard/src`（`_test.go` を除く）、バイナリは `/opt/isureal-guard/bin/isureal-guard`。
  `isureal-guard.service` が `/home/isucon/env.sh` を読み、`isureal-*.service`（`isureal-guard.service` を除く）のログを
  journalctl で読んで DB の `ip_bans` に BAN を書く。言語の実装を切り替えても、unit 名がこのパターンに合えば設定は要らない
- イメージでは isureal-go、isureal-ruby、isureal-rust、isureal-php、isureal-guard、nginx、PostgreSQL をすべて止めておき、app と db で必要なものを起動時に有効にする

初期データは本番の規模（datagen の `-scale large`）で、ビルド用インスタンス上で datagen の出力をそのまま
psql に流して投入します（seed は十数 GB あるので、ファイルにしません）。
