# Django / TiDB から登録情報を移行する

`export_registration.py` は移行元の Django 環境で SELECT とストレージ読み取りだけを行い、ログイン用の秘密情報を除いた JSON とアイコンの実体を出力します。
`cmd/import-registration` は JSON を検証し、空の PostgreSQL に登録情報を取り込みます。
既存データとのマージや差分取り込みは行いません。

この手順は登録情報の移行用です。
問い合わせ、競技結果、お知らせ、環境チェック結果のいずれかが非空なら、取り込みを中止します。
対象モデルの一覧が欠けている場合も中止します。
非空のデータがある場合は、その意味と変換方法を確認してから変換処理と合成データのテストを追加してください。
`other` の件数をゼロに書き換えて検証を通してはいけません。

## 保存先と変換規則

| 元の情報 | Go 版の保存先と扱い |
|---|---|
| チーム ID、所属するユーザー ID、GitHub 数値 ID | `teams.id`、`contestants.id`、`contestants.github_id` を維持 |
| 代表者と所属 | `teams.leader_id` と `contestants.team_id`。代表者の所属が不正なら中止 |
| Discord 未連携 | 空文字を保持。非空 ID だけ部分一意インデックスで重複を拒否 |
| 規約、個人情報、スポンサー提供の同意と撤回、法定代理人の同意 | `registration_records`。元のタイムゾーンが示す時刻、マイクロ秒、NULL、false を維持 |
| 個人メールアドレス、Tシャツサイズ | `registration_records.email`、`tshirt_size`。代表者メールは `teams.email_address` にも保存 |
| 学生区分 | 個人の値を保持。チームはメンバーが1人以上かつ全員学生の場合だけ true |
| 現地参加の確定 | `Team.is_local_participation` を所属メンバーの `in_person` に展開 |
| 現地参加希望、元の辞退日時、失格日時、環境チェック完了日時、クーポン同意日時 | `registration_source_records` のチーム記録に元の値を保存。運営 API で参照 |
| 辞退、失格、ゲスト | `withdrawn`、`disqualified`、`is_hidden` に変換。ゲストは通常順位表から非表示になる |
| 辞退で所属を失った代表者 | 元の owner ID はチーム記録に保存。現在の代表者や所属を捏造しない |
| 所属のないユーザー | 元のユーザー記録だけを保存。参加者や運営アカウントには変換しない |
| AWS とさくらのクラウドのクーポン | 1チームの `coupons.code` にカンマ区切りで保存。元の提供元別コードはチーム記録にも保存。`coupon_agreed_at` が非NULLなら activate=true |
| 登録招待クーポン | `registration_source_records` の `authentication.RegisterCoupon` に ID、宛名、トークン、関連チーム、作成・更新・使用日時を保存。Go 版の招待としては利用できず、クラウド利用クーポンにも変換しない |
| チームパスワード | 取り込まず、ランダムな招待トークンを生成 |
| 参加確定 | 単一開催の登録処理に合わせて `final_participation=true` |
| Discord サーバー所属状態 | 未確認として false。移行後に通常の同期処理で確認 |
| アイコン | オブジェクトを読み、内容の SHA-256 をファイル名にしてコピー。期限のない HTTPS URL に変換 |

ユーザー、チーム、登録招待クーポンの元データは、列を明示して抽出した JSON として `registration_source_records` にも保存します。
パスワード、OAuth トークン、旧環境チェックトークン、Django のセッションや権限は含めません。
標準 Django 認証用の氏名、最終ログイン、登録日時、superuser フラグは移行対象外です。
無効化された所属ユーザーは Go 側で同等のログイン禁止を表現できないため、取り込みを中止します。
登録招待トークンを含むため、スナップショットと元記録は機密情報として扱い、公開リポジトリに保存しないでください。
登録招待クーポンの件数は `other` と `registration_coupons` の一致を検証します。チームを指定した抽出では、そのチームに関連するクーポンだけを対象にします。
既知の除外列以外に移行元の列が増えた場合、エクスポートは中止します。

移行先にデータがある場合は中止します。
取り込み中はアプリケーションのデータテーブルをロックし、全レコードの挿入と採番シーケンスの補正を同一トランザクションで行います。
失敗時に部分的なチームや同意記録は残りません。
新規採番は、所属のないユーザーも含む移行元の最大 ID より後から始まります。

## 同意記録の更新と参照

`registration_records` は通常のアプリケーションモデルです。
移行用 JSON の退避だけに依存しません。
新規登録と招待参加の画面では規約と個人情報の取扱いに明示的なチェックを求め、日時はサーバーで記録します。
旧クライアントが `registration_record` を送らない場合は同意を補完せず、記録なしとして扱います。
その場合も同意済みとして表示しません。

スポンサー提供の状態は移行元の `User.set_sponsor_provide_agreement` に合わせます。

| 操作 | 同意日時 | 撤回日時 |
|---|---|---|
| 未同意から同意 | 操作時刻 | NULL |
| 同意から撤回 | NULL | 操作時刻 |
| 撤回から再同意 | 操作時刻 | NULL |
| 同じ状態の再送 | 変更なし | 変更なし |

これは現在の状態を表すモデルです。
すべての同意と撤回を蓄積するイベント履歴ではありません。
規約と個人情報の取扱いの同意日時は、既に記録があれば再送しても変更しません。
これらの false への更新は拒否し、スポンサー提供の撤回だけを受け付けます。
省略した項目は変更せず、法定代理人同意の false と未記録も区別します。
参加辞退で参加者行が削除されても同意記録と登録時の GitHub ID は残り、運営 API から参照できます。

| API | 権限 | 応答 |
|---|---|---|
| `GET /api/registration/record` | 本人。失格後も参照可能 | `RegistrationRecord` protobuf |
| `PUT /api/registration/record` | 本人と CSRF トークン。登録締切と競技開始後も使用可能 | `RegistrationRecordInput` を受け取り、更新後の protobuf |
| `GET /api/admin/contestants/{id}/registration_record` | Go 版の運営ログイン | `RegistrationRecord` protobuf |
| `GET /api/admin/registration_source/{id}?model=authentication.User` | Go 版の運営ログイン | 抽出したユーザーの JSON |
| `GET /api/admin/registration_source/{id}?model=authentication.Team` | Go 版の運営ログイン | 抽出したチームの JSON |

通常のチーム、観客、参加者一覧 API に個人の同意記録を追加しません。
参照 API は `Cache-Control: no-store` を返します。
本人がチームを辞退した後の変更は運営への問い合わせで受け付けてください。
個人情報の取扱いの文面は、移行元の `privacy.html` を Go 版の `/privacy.html` に移しています。

## 事前のリハーサル

1. 移行元のデータを変更しない SELECT 専用 DB アカウントと、オブジェクトを読み取るストレージ権限を用意します。
2. dev の整合したスナップショットを用意し、Django 環境で次のエクスポートを実行します。稼働中 DB を使う場合は書き込みを停止し、TiDB の同一スナップショットから全テーブルを読みます。
3. アイコンの配信先を先に用意し、出力されたファイルを永続領域へ配置します。通常の再デプロイで削除されるビルド成果物には置かないでください。
4. 隔離した PostgreSQL と Redis で取り込み、項目の照合と認証確認を行います。

移行元の Django 作業ディレクトリで実行します。
`PORTAL_EXPORT_DIR` は未作成の絶対パスを指定してください。
スクリプトは repeatable read のトランザクションで読み取り、Django のクエリラッパーで SELECT 以外を拒否します。
TiDB の read-only 指定は構文上の互換機能なので、書き込み防止には SELECT 専用アカウントも使用してください（[TiDB の公式仕様](https://docs.pingcap.com/tidb/stable/sql-statement-set-transaction/)）。
出力ファイルを管理するディレクトリは 0700、JSON は 0600 です。

```sh
export PORTAL_EXPORT_DIR=/secure/portal-export-20260928
export PORTAL_AVATAR_BASE_URL=https://portal.example.invalid/migration-avatars
python manage.py shell -c 'exec(compile(open("/path/to/export_registration.py").read(), "export_registration.py", "exec"), {"__name__": "__main__"})'
```

`avatars/` の各ファイルを、指定した HTTPS パスから同じバイト列で配信します。
ファイル名は SHA-256 と画像形式の拡張子で、PNG、JPEG、GIF、WebP に対応します。
10 MiB を超える画像と未対応形式は拒否します。
署名付き URL の文字列は出力せず、移行元の storage.open で画像実体を取得します。
移行先 URL にクエリや有効期限を含めると取り込み検証で拒否します。
配信先で適切な画像 Content-Type と `X-Content-Type-Options: nosniff` を設定してください。

Go 側は、まず DB 接続なしで検証できます。

```sh
cd portal
GOCACHE=/tmp/portal-go-cache go run ./cmd/import-registration -snapshot /secure/portal-export-20260928/snapshot.json
```

取り込み先を明示した上で `-apply` を付けます。
このコマンドは初期スキーマと未適用のバージョン付きマイグレーションも実行します。
既存 DB のスキーマ更新だけなら、通常起動時の `PORTAL_AUTO_MIGRATE=1` でも同じ処理を実行できます。

```sh
export ISUXPORTAL_DATABASE_URL='postgres://USER:PASSWORD@HOST/EMPTY_DATABASE?sslmode=require'
go run ./cmd/import-registration -snapshot /secure/portal-export-20260928/snapshot.json -apply
```

旧 DB の Discord インデックスは `001_registration_records.sql` で削除して作り直します。
既存の `CREATE INDEX IF NOT EXISTS` による更新には依存しません。
適用済みファイル名は `schema_migrations` に記録し、起動時に advisory lock で直列化して一度だけ適用します。
ロールバックには変更前のバックアップを使用します。
空文字が複数ある状態では旧一意インデックスに戻せません。

## 件数と項目の照合

次の SQL は移行先で実行します。
ユーザー全体と参加者の件数は、所属のないユーザーの分だけ異なります。
照合結果やダンプには個人情報が含まれるため、GitHub に添付しないでください。

```sql
SELECT count(*) FROM teams;
SELECT count(*) FROM contestants;
SELECT model, count(*) FROM registration_source_records GROUP BY model ORDER BY model;
SELECT count(*) FROM registration_records;
SELECT contestant_id, github_id FROM registration_records ORDER BY contestant_id;
SELECT count(*) FROM contestants WHERE discord_id = '';

-- 0 行であること。辞退チームの代表者 NULL は意図した変換。
SELECT t.id FROM teams t LEFT JOIN contestants c ON c.id = t.leader_id
WHERE NOT t.withdrawn AND (c.id IS NULL OR c.team_id <> t.id);
SELECT c.id FROM contestants c LEFT JOIN teams t ON t.id = c.team_id WHERE t.id IS NULL;
```

JSON のチーム件数、所属ユーザー件数、元レコード件数を照合します。
各 ID について、代表者、所属、GitHub ID、Discord ID、名前、学生区分、個人メール、代表者メール、同意5項目、Tシャツサイズ、現地参加希望と確定を比較します。
`payload` だけでなく、`registration_records` と本人用 API の値も照合してください。
日時は UTC に正規化して比較し、NULL と false を分けます。
アイコンは取得した配信レスポンスの SHA-256 がファイル名と一致することを全件確認します。
元 URL の7日間の期限が過ぎても、移設先の期限なし URL から同じ画像を取得できることを確認します。

## 最終切り替えと復旧

1. Django の登録、辞退、連携、同意変更、運営更新、バックグラウンド書き込みを停止します。読み取り専用の案内ページに切り替え、再開条件を記録します。
2. TiDB とオブジェクトストレージの復元可能な最終バックアップを作成します。停止後の同一スナップショットを抽出し、件数、取得時刻、対象 DB、コードのコミットを保存します。
3. Go 側の全サービスを停止した状態で、新しい空の DB に取り込みます。アイコン配信、上記の照合、DB ダンプの別 DB への復元を確認します。取り込み失敗時は書き込み停止を維持して原因を直します。
4. GitHub OAuth App の callback URL を `https://実際のホスト/auth/github/callback`、Discord を `/auth/discord/callback` に設定します。Discord の旧 callback `/auth/discord/complete/` を継続する場合は、Pulumi stack の `environment` に `ISUXPORTAL_DISCORD_CALLBACK_PATH: /auth/discord/complete/` を設定し、Pulumi 適用後に ecspresso で再デプロイします。同じホストと Discord Application を使う場合、旧 callback の登録を継続できます。ホストを変更する場合は新しい URL の登録が必要です。Go の公開ホストとリバースプロキシの `X-Forwarded-Proto: https` を照合します。
5. DNS またはプロキシを切り替える前に、新しい Cookie で実 GitHub OAuth を使い、代表者と一般メンバーが元のチームへ戻ることをブラウザーで確認します。Discord 未連携ユーザーの追加連携と、既存 ID の重複拒否も確認します。
6. Django の運営アカウントは引き継がれません。Go の運営ログインを Secret 管理で設定し、同意記録と元データを参照できること、一般ユーザーには参照できないことを確認します。
7. Django のセッションは引き継がれないため、全員に GitHub での再ログインを案内します。チームパスワードは廃止し、代表者が新ポータルの登録画面から招待 URL を取得してメンバーへ伝える手順を案内します。旧環境チェックトークンも廃止し、Go 版の環境チェック案内を再取得します。
8. 切り替え後に登録情報の読み取りと同意変更を確認してから書き込みを再開します。問題があれば Go 側の書き込みを停止します。再開前なら旧 DB と旧ルーティングへ戻し、再開後なら Go 側の更新を保全して差分を照合するまで旧側の書き込みを再開しません。

再案内文の例：

> 登録ポータルを更新しました。GitHub で再ログインすると、登録済みのチーム情報を確認できます。
> 旧チームパスワードによる参加は終了しました。メンバーを追加する場合は、代表者が登録画面の招待 URL を共有してください。
> Discord 未連携の方は登録画面から連携してください。スポンサーへの情報提供の同意状態は「同意記録」で確認、変更できます。

## 自動テストと未検証事項

合成データで、変換前検証、既存インデックスの置換、複数の Discord 未連携ユーザー、非空 ID の重複拒否、トランザクションのロールバック、再取り込み拒否、採番、同意の保存と更新、本人と運営の参照権限を検証します。
辞退、失格、ゲスト、現地参加、2種類のクーポンも含みます。
通常モードの認証処理に対して外部 OAuth 応答だけを模擬し、移行後の GitHub ログイン、セッション維持、JOINED 応答、Discord の後からの連携を検証します。

実 TiDB に対するこのエクスポーターの実行、実 OAuth App の設定、実ブラウザーでのログイン、実配信先での7日後の画像取得、本番ダンプの復元は、この変更の自動テストでは検証していません。
本番切り替え前に上記のリハーサルで確認してください。
問い合わせや競技結果など非空の未対応モデルがある環境への移行も、この PR の完了条件には含めません。

## 1チームだけの移行検証

`PORTAL_EXPORT_TEAM_ID=<TEAM_ID>` を指定すると、そのチームと所属ユーザー、元の代表者だけを抽出します。
未対応モデルの件数は移行元全体で数え、非空なら通常どおり中止します。

取り込みコマンドは非公開の `s3://bucket/key` も読み取れます。
ECS の migrate タスクには、設定バケットの `rehearsal/*/snapshot.json` に限った読み取り権限を Pulumi で付与しています。
スナップショットには個人情報が含まれるため、公開バケットには配置しないでください。
このパスには登録用スナップショットだけを保存し、旧パスワードやセッションを含む復元用バックアップには別の名前と保存先を使ってください。

`-apply -create-rehearsal-database rehearsal_NAME` は、設定済み PostgreSQL サーバーに新しい検証用 DB を作成して取り込みます。
名前は `rehearsal_` で始まる小文字・数字・アンダースコアだけに制限し、同名 DB が存在したら中止します。
元のアプリケーション DB は変更しません。
取り込み後は ID、所属、同意、アイコン URL、元データの保存内容を照合します。
`-verify-only` は既存の取り込み結果を読み取りだけで照合します。

Go dev を検証用 DB に向ける場合は、Pulumi の `applicationDatabase` と `redisDatabase` を設定して適用し、ecspresso で各サービスを再デプロイします。
RDS 自体の初期 DB 名は変更しません。
元に戻すときも同じ手順で `applicationDatabase=isuxportal`、`redisDatabase=0` を設定します。
検証用 Redis DB には元のログインセッションを移さず、実際の GitHub 再ログインで同じ参加者に戻ることを確認します。

## 旧 GitHub callback を継続する場合

Go 版は `/auth/github/callback` に加えて、旧 Django の `/complete/github/` も受け付けます。
OAuth 開始時の callback を旧パスにする場合は、Pulumi stack の `environment` に `ISUXPORTAL_GITHUB_CALLBACK_PATH: /complete/github/` を設定して再デプロイします。
トークン交換には実際に受信した callback パスを使用するため、設定変更前に開始した Go 版の認証も受け付けます。
旧 Django の認証途中の state とログインセッションは引き継ぎません。
同じドメインと GitHub Application を使う場合は既存の callback 登録を継続できます。
