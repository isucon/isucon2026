# webapp-optmized-astra

Team 10向けのRust最適化実装。`webapp/rust`と`webapp/sql`を2335d5fから複製した。
作業期限は2026年10月28日09:00 JST。各ベンチ結果をこのディレクトリに記録し、コミットしてpushする。

## デプロイ

アプリとDBの実行設定は専用Pulumiプロジェクト`provisioning/aws/astra`の`dev`スタックで管理する。
共有インフラや他チームの設定は変更しない。EC2への反映にはPulumi管理のSSM Document/Associationを使う。
ベンチのFargateタスクは既存のecspresso経由で実行する。

```sh
webapp-optmized-astra/deploy/package.sh
export AWS_CONFIG_FILE="$PWD/provisioning/aws-sdk.config"
export AWS_SDK_LOAD_CONFIG=1
export PULUMI_BACKEND_URL=s3://isucon2026-pulumi-state
pulumi preview -C provisioning/aws/astra --stack dev --diff
pulumi up -C provisioning/aws/astra --stack dev
make aws-bench-run TEAM=10
```

アプリは各VMの`/home/isucon/webapp-optmized-astra`へ配置し、同名のsystemdサービスで実行する。
DB認証設定は既存VMの`/home/isucon/env.sh`を参照する。追加の秘密情報はSecrets Managerで管理する。
公開された成功レスポンスの後は全地域から更新結果が見える仕様を維持する。

## 検証

```sh
cargo test --manifest-path webapp-optmized-astra/rust/Cargo.toml
cd provisioning/aws
node_modules/.bin/tsc --noEmit -p astra
```

RustのDB接続を伴うキャンセル検証は`ISUREAL_SESSION_TEST_DSN`がなければスキップされる。
単体テストだけではAPI整合性は証明できないため、Team 10の実ベンチ結果を判定に使う。

## 実験予定

- 未最適化Rust版の基準測定。
- 投稿のロック、署名計算、通知、SQL往復の最適化。
- europe/app2、america/app3へのPostgreSQL配置。同期レプリカと地域内読み取りを検証し、性能と整合性を比較する。

計測結果は`results/`へ保存する。詳細なリソース統計の原本は`var/aws-bench/`と既存の結果用S3バケットに残る。

| 実行 | 変更 | 合否 | スコア |
| --- | --- | --- | ---: |
| 20261027T121146Z-team10 | Rust基準版、asia単一DB | PASS | 9,022 |
