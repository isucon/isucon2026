# ISUCON2026 問題

ISUCON2026 の問題アプリケーション「ISUReal」、ベンチマーカー、競技環境の構築コードを収録しています。
ISUReal は、asia、europe、america の3地域で写真を共有する SNS です。

## マニュアル

- [当日マニュアル](docs/guide.md)
- [アプリケーションマニュアル](docs/manual.md)
- [ベンチマーカーの仕様と実行方法](bench/README.md)
- [Router の通信制御](docs/router.md)

## ディレクトリ構成

| ディレクトリ | 内容 |
|---|---|
| `webapp/` | 問題アプリケーションの初期実装と SQL |
| `frontend/` | 全言語共通のフロントエンド |
| `bench/` | ベンチマーカー |
| `dataset/` | 初期データの定義 |
| `webapp/datagen/` | 初期データ生成プログラム |
| `guard/` | アプリケーションログを監視するプログラム |
| `router/` | 地域間の通信制御と、本番用の設定配信サービス `isureal-router-control` |
| `portal/` | 競技用ポータル |
| `supervisor/` | ベンチマークジョブの実行管理 |
| `proto/`, `gen/` | Protobuf 定義と生成コード |
| `provisioning/` | Ansible、Packer、Pulumi による環境構築 |
| `docs/` | マニュアル、設計資料、開発時の調査記録 |
| `scripts/` | 運営用スクリプト |

初期実装は Go、Ruby、Rust、Python、PHP、Perl、Node.js です。
言語ごとの起動方法は `webapp/` 配下の各実装と [Ansible の構成](provisioning/ansible/README.md) を参照してください。

## 競技環境の構成

アプリケーションサーバー3台（各地域に1台）、共有 DB サーバー1台、通信を制御する Router 1台で構成されます。
初期状態では asia の DB に接続します。
ベンチマーカーはポータルから Supervisor を経由して実行します。

参加者環境のテンプレートは [portal/internal/cloudformation](portal/internal/cloudformation/README.md)、作問用 AWS 環境は [provisioning](provisioning/README.md) にあります。
インスタンスタイプ、AMI、リソース上限は構築方法と設定によって異なります。

## 初期データと実行環境

ツールのバージョンは [mise.toml](mise.toml) に記録しています。
初期データの生成には Go が必要です。

```bash
make datagen
```

生成した `webapp/sql/002-seed.sql` は git 管理対象外です。
本番規模のデータ生成と投入方法は [webapp/datagen/README.md](webapp/datagen/README.md) を参照してください。
アプリケーションの実行環境は、以下の AWS 構築手順を参照してください。
旧モックに依存していた Docker Compose 開発環境と Router E2E は削除しています。

## ベンチマーカーの実行

構築した競技環境の Router を対象に、リポジトリルートから実行します。
ベンチマーカーは開始時に対象アプリケーションのデータを初期化します。

```bash
go -C bench run . -target "http://ROUTER_IP:8080" -host "APP_HOSTNAME" -scale large
```

`-scale` は初期データを生成した規模に合わせてください。
本番規模の `large`、写真データ、負荷や検証のオプションは [bench/README.md](bench/README.md) を参照してください。

## AWS 上での環境構築

Packer と Ansible で AMI を作成し、Pulumi で環境を構築します。
AWS の認証情報、利用するリージョン、接続元 CIDR などを設定する必要があります。
運営用 stack の設定をそのまま適用せず、使用する AWS アカウントに合わせて構築してください。

- [AMI の作成と Pulumi の設定](provisioning/README.md)
- [Ansible による配布物とサービスの構成](provisioning/ansible/README.md)
- [ポータルのデプロイ](portal/deploy/README.md)
- [Supervisor のデプロイ](provisioning/aws/supervisor/README.md)

TLS 証明書の設定は構築方法に従ってください。
作問用 Pulumi 環境は証明書を指定しない場合、自己署名証明書を生成します。

## 開発時の検証

```bash
make test
make build
make router-check
```

`make test` と `make build` は Go 初期実装とフロントエンドを対象にします。
フロントエンドの依存パッケージは事前に `make frontend-install` でインストールしてください。
各コンポーネントの追加テストは、それぞれの README を参照してください。

README の項目構成は [ISUCON14 の README](https://github.com/isucon/isucon14/blob/main/README.md) を参考にしています。
