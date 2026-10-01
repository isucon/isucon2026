# 本番公開までに用意する外部連携

Ansible の既定値は外部連携を未設定とし、運営限定モードで起動する。
外部連携を使う機能の受け入れ確認は、実際の認証情報と接続先を設定した後に行う。
秘密値は Ansible Vault または GitHub Secrets へ保存し、平文でリポジトリに含めない。
設定方法は [Ansible README](ansible/README.md) を参照。
開催日時は確定済みの値を後日入力する。この文書では日時を仮置きしない。

## アカウント・設定値

以下の `portal.*` は Vault 内で `portal_overrides.*` として指定する。

| 連携 | 用意するもの・設定先 | 確認する動作 |
|---|---|---|
| GitHub OAuth | GitHub の OAuth App。`portal.github.client_id/client_secret`。callback は `https://<公開FQDN>/auth/github/callback` | 実アカウントでログイン、ログアウト、再ログイン。開発と本番のアプリを分離 |
| Discord OAuth | Developer Portal の Application。`portal.discord.client_id/client_secret`。redirect は `https://<公開FQDN>/auth/discord/callback` | Discord アカウントの紐付け・差し替え。アプリに登録した redirect URL と完全一致 |
| Discord bot | 同 Application の bot token、対象サーバー。`portal.discord.bot_token/server_id/channel_id/contestant_role_id/admin_role_id/support_comm_channel_roles` | メンバー同期、ロール付与、サポートチャンネル作成・閲覧制御、投稿。Bot のロールを付与対象より上に配置。利用する intents と権限を実処理に合わせて確認 |
| AWS 認証 | 専用 IAM principal の access key。`portal.aws.region/access_key_id/secret_access_key` | さくら VM から AWS API へ到達。キーの有効性・ローテーション手順を確認 |
| S3 アイコン | `portal.aws.avatar_bucket/avatar_base_path`。対象プレフィックスへの `s3:PutObject`、ブラウザの PUT を許可する CORS、画像の読み出し方針 | 署名付き PUT と表示。現実装は `https://<bucket>.s3.<region>.amazonaws.com/<key>` を返すため、その URL で読める設計が必要 |
| EC2 / CloudFormation | `portal.aws.test_ami_id/contest_ami_id/cloudformation_staging`。ポータル側 IAM は `ec2:DescribeInstances`。選手側には生成テンプレートに必要な権限・クォータを別途準備 | AMI のリージョン・公開範囲、生成テンプレートからの作成、環境情報の同期、削除。ポータルの IAM と選手が実行する IAM は別 |
| 選手環境 SSH | `portal.admin_ssh_private_key` に isuadmin 用の秘密鍵。対応公開鍵を競技 AMI に配置。worker から各競技 VM の TCP 22 へ到達可能にする | envcheck・運営コマンド実行。鍵は root 管理ファイルを worker に read-only mount |
| ベンチマーカー | `/opt/portal/secrets.json` の `portal.bench_token`、checker 用 `checker_secret`、公開鍵取得用 `ssh_key_api_secret` を対応する実行側へ安全に配布 | ジョブ取得、結果送信、envcheck、公開鍵取得の認証。トークンをログに出さない |
| Slack（利用時） | 通知先の Incoming Webhook、`portal.slack_webhook_url` | 失敗通知・運営通知をテスト。未設定なら Slack 通知なし |
| Web Push（利用時） | VAPID 鍵ペア。`portal.vapid.private_key_json` は PEM を JSON 文字列にした値、`portal.vapid.subject` は運用連絡先 | HTTPS で購読、配信、失効購読の処理。再デプロイ時に鍵を変えない |
| Sentry（利用時） | プロジェクトの DSN、`portal.sentry_dsn` | 実装上は SPA の meta に DSN を渡す。バックエンド監視を DSN 設定だけで完了扱いにしない |

開発の公開 FQDN は `portal-dev.isucon.whywrite.it`。
本番 FQDN が変わる場合は OAuth の callback、S3 CORS、Cookie、DNS、証明書、LB の Host をまとめて確認する。

## 配布・デプロイ連携（任意）

手元の Git からビルドする既定構成では以下は不要。
GitHub Actions から配布するときに準備する。

- コンテナレジストリと push / pull 用アカウント。ビルド workflow の `github` アカウント用 Secret `SAKURACR_PASSWORD` を設定する。
- `app_overrides.mode: registry`、`app_overrides.image`、Vault の `registry` を設定する。
- デプロイ用 SSH 公開鍵を `github_deploy_authorized_keys` へ、秘密鍵を GitHub Secret `PORTAL_DEPLOY_SSH_KEY` へ設定する。
- `PORTAL_DEPLOY_KNOWN_HOSTS` に別経路で照合した SSH ホスト鍵を登録する。
- Repository Variables `PORTAL_DEPLOY_ENABLED=true`、`PORTAL_DEPLOY_HOST_DEVELOP`、`PORTAL_DEPLOY_HOST_PRODUCTION` を設定する。ホストは LB の公開名ではなく VM の SSH 接続先。
- registry タグと環境の対応を確認し、開発で更新・失敗時の復帰を試す。本番は workflow_dispatch で実行する。

## 本番公開の前提

- PR のレビュー・マージ、同じコミットの再現可能なデプロイ。
- Django 側の登録データを PostgreSQL 用にマッピングし、移行をリハーサルする。件数、ユーザーとチームの関係、権限、重複、日時を照合する。
- 確定済みの開催日時 7 項目、参加上限、規約・ルール・案内 URL を設定して、時刻境界をテストする。
- 必須の OAuth、Discord、AWS、競技環境 SSH と bench/checker を実接続で確認する。
- 900 チーム以上の負荷試験、ジョブ滞留・再試行、再起動時の復旧、LB の監視を確認する。
- 外部監視、通知先、ディスク監視、バックアップの別障害領域へのコピー、復元試験、切り戻し条件と担当者を決める。
- 本番 DB を同居にするかマネージドへ移すかを決定する。マネージド化は今回の Ansible に含めない。
- 切替直前に旧環境の書き込みを止め、最終移行と確認を行い、その後 `admin_only: false` にする。
