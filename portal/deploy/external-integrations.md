# 外部連携と秘密値

公開前に、対象環境用の OAuth App と Discord Application を用意してください。
初期状態は `ISUXPORTAL_ADMIN_ONLY=1` で、確定していない競技時刻は設定しません。
平文の認証情報をリポジトリや Pulumi 変数に保存しないでください。

## Secrets Manager への登録

Pulumi は `<name>/application` と `<name>/admin-ssh-key` の入れ物だけを作ります。
`application` は環境変数名をキーとする JSON、`admin-ssh-key` は worker 用 SSH 秘密鍵の PEM 本文です。
次の例ではファイルをアクセス制限された作業場所に用意し、登録後に削除してください。

```sh
umask 077
# application.json に必要な JSON キーと実際の値を作成
aws secretsmanager put-secret-value \
  --secret-id portal-develop/application \
  --secret-string file:///secure/path/application.json
aws secretsmanager put-secret-value \
  --secret-id portal-develop/admin-ssh-key \
  --secret-string file:///secure/path/isucon_admin.key
```

`application` に必要な既定のキーは以下です。
Pulumi の `secretKeys` に含まれるキーが JSON に存在しないと、ECS はタスクを起動できません。
OAuth が未設定の検証環境ではその client secret を空文字にできますが、認証は利用できません。

| キー | 内容 |
|---|---|
| `ISUXPORTAL_SESSION_SECRET` | セッション署名用の十分に長いランダム値 |
| `ISUXPORTAL_BYPASS_SECRET` | 32 バイト以上のランダム値を base64 化 |
| `ISUXPORTAL_CHECKER_SECRET` | 32 バイト以上のランダム値を base64 化。checker 実行側にも配布 |
| `ISUXPORTAL_BENCH_TOKEN` | supervisor と共有するランダム token |
| `ISUXPORTAL_ADMIN_PASSWORD` | 運営ログイン用パスワード |
| `ISUXPORTAL_GITHUB_CLIENT_SECRET` | GitHub OAuth App の secret |
| `ISUXPORTAL_DISCORD_CLIENT_SECRET` | Discord OAuth2 の secret |

`openssl rand -base64 32` 等で環境ごとに別のランダム値を生成してください。
利用する場合は `ISUXPORTAL_DISCORD_BOT_TOKEN`、`ISUXPORTAL_SLACK_WEBHOOK_URL`、`ISUXPORTAL_VAPID_PRIVATE_KEY` も JSON と `secretKeys` の両方へ追加します。
VAPID private key の値は PEM を JSON 文字列化した値で、外側の secret JSON 内ではさらに文字列としてエスケープします。
例として Python の `json.dumps({"ISUXPORTAL_VAPID_PRIVATE_KEY": json.dumps(pem)})` が対応する形式です。

SSH 秘密鍵は worker だけに注入され、entrypoint が書き込み用 volume に mode 0600 で保存して環境変数から除去します。
対応公開鍵を競技 AMI の `isuadmin` に設定し、選手環境の TCP 22 に Pulumi output `natPublicIps` から接続できるようにしてください。
worker は外部接続に NAT を使います。
選手環境が private IP のみの場合は VPC peering 等の経路と Security Group を別途用意します。

## Pulumi environment の設定

非秘密値は Pulumi の stack 設定 の `environment` map に指定します。

| 連携 | 設定キーと確認対象 |
|---|---|
| GitHub | `ISUXPORTAL_GITHUB_CLIENT_ID`。callback は `https://<domain>/auth/github/callback`。ログイン、ログアウト、再ログインを確認 |
| Discord OAuth | `ISUXPORTAL_DISCORD_CLIENT_ID`。redirect は `https://<domain>/auth/discord/callback`。紐付けと差し替えを確認 |
| Discord OAuth callback compatibility | Set `ISUXPORTAL_DISCORD_CALLBACK_PATH` to `/auth/discord/complete/` in the Pulumi stack `environment` to retain the Django callback URL. The default is `/auth/discord/callback`; both routes remain available. The trailing slash on the Django path is required. |
| Discord bot | `ISUXPORTAL_DISCORD_SERVER_ID`、`ISUXPORTAL_DISCORD_CHANNEL_ID`、`ISUXPORTAL_DISCORD_CONTESTANT_ROLE_ID`、`ISUXPORTAL_DISCORD_ADMIN_ROLE_ID`、`ISUXPORTAL_DISCORD_SUPPORT_COMM_CHANNEL_ROLES`。intents、ロール順位、チャンネル権限を実接続で確認 |
| 選手 EC2 | `ISUXPORTAL_TEST_AMI_ID`、`ISUXPORTAL_QUALIFY_AMI_ID`、`ISUXPORTAL_CLOUDFORMATION_STAGING`。AMI の公開範囲、選手側 IAM、EC2 クォータ、作成と削除を確認 |
| Web Push | `ISUXPORTAL_VAPID_SUBJECT`。HTTPS で購読と配信を確認。再デプロイで鍵を変更しない |
| Sentry | `SENTRY_DSN`。SPA とバックエンドからのイベント受信を確認 |
| 公開文書 | `ISUXPORTAL_TERMS_URL`、`ISUXPORTAL_RULES_URL`、`ISUXPORTAL_DOCS_URL` |
| 参加上限 | `ISUXPORTAL_MAX_TEAMS`。既定 1000 |

AWS 認証は ECS task role を使います。
portal にはアバター prefix の `s3:PutObject`、worker には `ec2:DescribeInstances` を付与しています。
選手が実行する CloudFormation の IAM 権限は別途用意します。
S3 の bucket、CORS、公開 GET policy は Pulumi が管理します。
ブラウザの署名付き PUT と画像表示を実接続で確認してください。

競技時刻は RFC3339 で次の 7 キーに設定します。

- `ISUXPORTAL_TIMING_REGISTRATION_OPEN`
- `ISUXPORTAL_TIMING_REGISTRATION_CLOSE`
- `ISUXPORTAL_TIMING_REGISTRATION_INVITATION_CLOSE`
- `ISUXPORTAL_TIMING_REGISTRATION_UPDATE_CLOSE`
- `ISUXPORTAL_TIMING_CONTEST_START`
- `ISUXPORTAL_TIMING_CONTEST_FREEZE`
- `ISUXPORTAL_TIMING_CONTEST_END`

ドメイン変更時は、OAuth callback、Discord redirect、S3 CORS、Cookie、DNS、証明書を合わせて変更してください。
公開前の移行と負荷試験は [deploy README](README.md) にまとめています。

Bot トークン `ISUXPORTAL_DISCORD_BOT_TOKEN` は worker と、有効化した場合の bot タスクだけに注入します。所属確認は worker の REST API 呼び出しで実行でき、常駐 bot の起動は不要です。参加者ロール同期を使う場合は `ISUXPORTAL_DISCORD_CONTESTANT_ROLE_ID` に対象環境のロール ID を指定します。現在の同期対象は参加者ロールのみで、現地参加ロールは管理しません。
