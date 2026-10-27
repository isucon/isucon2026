# webapp-optmized-astra

Team 10向けのRust最適化実装。`webapp/rust`と`webapp/sql`を2335d5fから複製した。
作業期限は2026年10月28日09:00 JST。各ベンチ結果をこのディレクトリに記録し、コミットしてpushする。

## デプロイ

アプリとDBの実行設定は`deploy/runtime.json`に記録し、Pulumiプロジェクト`provisioning/aws/astra`の`dev`スタックから参照する。
ISUCONルールに合わせて、新しいAWSリソースは作成しない。既存のTeam 10 VMへSSH/scpで反映する。
接続には既存のEC2 Instance Connect Endpointを使い、共有インフラや他チームの設定は変更しない。
ベンチのFargateタスクは既存のecspresso経由で実行する。

```sh
webapp-optmized-astra/deploy/package.sh
export AWS_CONFIG_FILE="$PWD/provisioning/aws-sdk.config"
export AWS_SDK_LOAD_CONFIG=1
export PULUMI_BACKEND_URL=s3://isucon2026-pulumi-state
pulumi preview -C provisioning/aws/astra --stack dev --diff
webapp-optmized-astra/deploy/apply.sh
webapp-optmized-astra/deploy/benchmark.sh "experiment label"
```

アプリは各VMの`/home/isucon/webapp-optmized-astra`へ配置し、同名のsystemdサービスで実行する。
DB認証設定は既存VMの`/home/isucon/env.sh`を参照する。レプリケーションの認証情報はgit管理外の
`var/astra/.replication.env`から対象VMの`/home/isucon/.astra-replication.env`へscpで共有し、権限600で保存する。
Secrets Managerは使用しない。秘密情報をコミットしない。初期に作成したAWS配備リソース16個は削除操作を完了し、Pulumiの状態には設定用スタックだけが残る。
Secretの物理削除はAWSの非同期処理に従う。アプリやDBの実行・再起動はそのSecretに依存しない。
公開された成功レスポンスの後は全地域から更新結果が見える仕様を維持する。

## 検証

```sh
cargo +1.99.1 test --manifest-path webapp-optmized-astra/rust/Cargo.toml
cd provisioning/aws
node_modules/.bin/tsc --noEmit -p astra
```

投稿用DB関数の結合テストは、専用の`astra_post_test`データベースに対して実行する。
このテストは専用DBのテーブルを作り直す。並行投稿、重複投稿、途中失敗時のロールバック、期間切り替えとの競合を検証する。

```sh
ASTRA_POST_TEST_DSN=postgres://postgres@127.0.0.1:26210/astra_post_test \
  python3 webapp-optmized-astra/tests/post_function.py
```

RustのDB接続を伴うキャンセル検証と通知の結合テストは`ISUREAL_SESSION_TEST_DSN`がなければスキップされる。
通知テストは一時スキーマを作り、接続時刻、通知順序、地域変更・退会・不正な署名鍵、接続切断、ポーラーの再起動を確認する。
単体テストだけではAPI整合性は証明できないため、Team 10の実ベンチ結果を判定に使う。

## DB複製の切り替え

`runtime.json`で`replicasEnabled=true`、`replicasSynchronous=false`、`replicaReads=false`として
`deploy/apply.sh replication`を実行し、両レプリカのベースバックアップと追従を確認する。
次に`replicasSynchronous=true`へ変更して同じコマンドで`remote_apply`を有効にする。
最後に`replicaReads=true`として`deploy/apply.sh application`で読み取りを地域内のDBへ切り替える。
アプリは起動時に同期設定とレプリカのリカバリー状態を検証する。

単一DBへ戻す際は、先に`replicaReads=false`として`deploy/apply.sh application`で全台を切り替え、
その完了後に同期レプリケーションを解除する。初期AMIのDBは各VMの`16/main`に保持する。

## 最適化の対象

- 未最適化Rust版の基準測定。
- 投稿のロック、署名計算、通知、SQL往復の最適化。
- JPEGは成功した検証結果を最大4096件保持し、初回は圧縮データ全体を検査しながら画素展開量を減らす。
- SSE通知は地域内DBごとに100ミリ秒間隔で最大512接続ずつ一括確認する。接続ごとの通知時刻とユーザー状態は毎回確認する。
- europe/app2、america/app3へのPostgreSQL配置。同期レプリカと地域内読み取りを検証し、性能と整合性を比較する。

計測結果は`results/`へ保存する。詳細なリソース統計の原本は`var/aws-bench/`と既存の結果用S3バケットに残る。

| 実行 | 変更 | 合否 | スコア |
| --- | --- | --- | ---: |
| 20261027T121146Z-team10 | Rust基準版、asia単一DB | PASS | 9,022 |
| 20261027T121933Z-team10 | indexes, shared posting locks and cached HMAC | PASS | 16,363 |
| 20261027T122322Z-team10 | repeat indexed Rust with warm database | PASS | 14,156 |
| 20261027T130427Z-team10 | synchronous Europe and America PostgreSQL replicas with local reads | PASS | 8,742 |
| 20261027T131543Z-team10 | distributed DB with one-round-trip post creation | PASS | 12,945 |
| 20261027T133107Z-team10 | distributed DB with bounded cached JPEG validation | PASS | 13,910 |
| 20261027T133643Z-team10 | distributed DB with shared batched SSE notification polling | PASS | 21,027 |
