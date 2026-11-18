# 運営向けインフラ

ISUCON2026 の大会運営で使用したインフラと、その構築・運用手順への索引です。
過去問として環境を再現する手順は [README](../../README.md) を参照してください。

## 構成

- 参加者の環境は、ポータルが発行する CloudFormation テンプレートで各チームの AWS アカウントに作成しました
- ベンチマーカーは、ポータルからジョブを受け取る Supervisor が ECS Fargate 上で実行しました
- 作問・検証用の環境は Pulumi で運営の AWS アカウントに作成しました

Pulumi がインフラとデプロイ設定を管理し、ecspresso が ECS サービスとタスク定義を管理します。
秘密情報は Secrets Manager に置き、Pulumi とデプロイ定義にはキー名と参照だけを記録します。

## ディレクトリ構成

| ディレクトリ | 内容 |
|---|---|
| `webapp/` | 問題アプリケーションの初期実装と SQL |
| `webapp/datagen/` | 初期データ生成プログラム |
| `frontend/` | 全言語共通のフロントエンド |
| `bench/` | ベンチマーカー |
| `dataset/` | 初期データの定義 |
| `guard/` | アプリケーションログを監視するプログラム |
| `router/` | 地域間の通信制御と、設定配信サービス `isureal-router-control` |
| `portal/` | 競技用ポータル |
| `supervisor/` | ベンチマークジョブの実行管理 |
| `proto/`, `gen/` | Protobuf 定義と生成コード |
| `provisioning/` | Ansible、Packer、Pulumi による環境構築 |
| `docs/` | マニュアル、設計資料、開発時の調査記録 |
| `scripts/` | 運営用スクリプト |

## 手順書

### 参加者環境

- [CloudFormation テンプレート](../../portal/internal/cloudformation/README.md)
- [Router の構成と isureal-router-control の配備](../../router/README.md)
- [既存の Router にデータ側の HTTP(S) ブロックを入れるツール](../../portal/cmd/router-data-proxy-block/README.md)

### ポータル

- [ポータル](../../portal/README.md)
- [ポータルのデプロイ](../../portal/deploy/README.md)

### ベンチマーカー

- [Supervisor の構成と配備](../../provisioning/aws/supervisor/README.md)
- [ベンチの単発実行](../../provisioning/aws/bench-runner/README.md)

### 作問・検証用の AWS 環境

- [AMI の作成と Pulumi の設定](../../provisioning/README.md)
  - envcheck AMI の作成、既存環境を保持した追加構築、競技後の環境検証を含みます
- [Ansible による AMI の構成](../../provisioning/ansible/README.md)

## 開発時の検証

```sh
make test
make build
make router-check
```

`make test` と `make build` は Go 初期実装とフロントエンドを対象にします。
フロントエンドの依存パッケージは事前に `make frontend-install` でインストールしてください。
各コンポーネントの追加テストは、それぞれの README を参照してください。
