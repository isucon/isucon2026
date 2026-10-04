# portal-devへのSupervisor適用記録

2026-10-03、AWSアカウント`124502390568`の`ap-northeast-1`へ適用しました。
接続先は`https://portal-dev.isucon.whywrite.it`です。
Supervisorの配備とポータル連携は確認できましたが、検証ベンチは既存アプリ側の問題で失敗しました。

## 配備したリソース

| 項目 | 値 |
|---|---|
| Pulumi project / stack | `isucon2026-trial-supervisor` / `portal-dev` |
| Pulumi backend | `s3://isucon2026-pulumi-state-124502390568/supervisor` |
| ECS cluster | `supervisor-cluster-1090218` |
| team1 service | `supervisor-service-1-bae472a` |
| team2 service | `supervisor-service-2-46e94a5` |
| team3 service | `supervisor-service-3-8c4ac5c` |
| タスクごとの資源 | X86_64 Fargate、4 vCPU、8 GiB、desiredCount=1 |
| AZ / ジョブ識別子 | `ap-northeast-1a` / `apne1-az4` |
| CloudWatch Log Group | `supervisor-logs-2e941a9`（保持7日） |
| 固定送信元IP | `54.248.191.93` |
| ECR | `124502390568.dkr.ecr.ap-northeast-1.amazonaws.com/isucon2026-supervisor-portal-dev` |
| イメージdigest | `sha256:a93b1d3b74afc367fa6a1cb09369fda62fb10cf9f52ee536f89ff1939b6b5bdc` |

設定の正本を再現する例は[Pulumi.portal-dev.yaml.example](Pulumi.portal-dev.yaml.example)です。
secretのARNだけを設定に含め、トークン本文は保存していません。
認証には既存の`portal-develop/application`の`ISUXPORTAL_BENCH_TOKEN`を直接参照します。
既存RouterのSecurity Group `sg-09663cf353f2246e7`に、固定送信元IPの`/32`からTCP/8080を許可するルールを追加しました。
既存のSSH許可と他の通信許可は維持しています。

ローカルにDocker実行環境がなかったため、一時CodeBuild環境で現行`supervisor/Dockerfile`をビルドしました。
コンテナに含めたソースはGitリビジョン`337b0f0`のSupervisor、bench、portal、datasetです。
GitHub上の最新main `acf0addb8fdfeb69e7631c6c2009c5e3c5a422f7`と比較し、bench、supervisor、datasetの内容が一致することを確認しました。
この2リビジョンの差分はポータルのSlack通知用デプロイ設定だけです。
Supervisorのフォーマット検査、単体テスト5件、結合テスト4件、全ターゲットの型検査を通過しました。
IaCのテスト11件、TypeScript型検査、Pulumi previewとupも通過しました。
ビルド後、一時CodeBuild project、S3 source bucket、IAM role、専用ビルドログを削除し、ポータル登録に使った一時task definitionをderegisterしました。

## ポータルへの登録と対象アプリ

登録前のチーム数は0件でした。
利用者の指示に従い、以下の3チームと9件のインスタンスを登録しました。
追加の指示に従い、各チームにGitHubアカウントを登録し、チームリーダーに設定しました。

| チーム | GitHub login | GitHub ID | Contestant ID |
|---|---|---|---|
| `trial-team1` | `sorah` | `29440` | 1 |
| `trial-team2` | `oribe1115` | `50443000` | 2 |
| `trial-team3` | `utgwkk` | `4530627` | 3 |

`isucon/isucon2026-portal-infra`のdevelop設定からGitHub App情報を復元し、既存Secrets Manager JSONの他のキーを保持してclient secretを登録しました。
ポータルのPulumi設定では`ISUXPORTAL_ADMIN_ONLY=0`とclient IDを反映し、同じイメージのECS task definition `portal-develop-portal:3`を配備しました。
2026-10-04、利用者がGitHub Appへcallback URL `https://portal-dev.isucon.whywrite.it/auth/github/callback`を追加しました。
ブラウザでGitHub認証からポータルへ戻り、`whywaita`のGitHubログイン状態が表示されることを確認しました。
`whywaita`は試し解きチームの登録対象ではないため、参加登録画面へ遷移しました。
`sorah`、`oribe1115`、`utgwkk`本人によるログインは未確認です。
秘密値はこの記録やGit管理ファイルに含めていません。
各チームの`availability_zone`は`apne1-az4`です。

| チームID | チーム名 | Router public IP | 登録したアプリ |
|---|---|---|---|
| 1 | `trial-team1` | `54.250.237.4` | team1のapp1〜3 |
| 2 | `trial-team2` | `54.199.99.27` | team2のapp1〜3 |
| 3 | `trial-team3` | `43.207.87.177` | team3のapp1〜3 |

`contestant_instances`には実EC2のinstance ID、private IP、public IPを保存しています。
ベンチ実行時だけ`targetOverrides`を使い、選択されたappのpublic IPをRouter public IPへ置き換えます。
Hostには`appN.teamT-aws.127.0.0.1.nip.io`を指定します。
このHostはHTTPヘッダーとして使い、DNSによるloopbackへの接続には使いません。
問題サーバーへ直接接続するSecurity Groupルールは追加していません。

## 実機検証結果

3タスクの稼働と、各チームに限定したポーリングを確認しました。
管理APIから以下のジョブを投入し、Supervisorによる受領と結果確定を確認しました。

| ジョブID | チーム | 対象 | 最終状態 | 結果 |
|---|---|---|---|---|
| 1 | team1 | app1 | ERRORED | 初期データのMemories件数不一致とregion_mismatch |
| 2 | team2 | app2 | ERRORED | DB `192.168.20.10:5432`への接続拒否 |
| 3 | team3 | app3 | ERRORED | DB `192.168.36.10:5432`への接続拒否 |

いずれもベンチの終了コード1、fail、0点が実ポータルに保存されました。
通信失敗ではなく、対象アプリからの検証エラーまたはDB接続エラーが返っています。
正常なスコア獲得までの受け入れ検証は未完了です。

検証後、既存のteam1〜3のdb1 EC2はすべて`terminated`であることを確認しました。
このSupervisor作業では、db1の停止や削除を実行していません。
既存アプリ環境の修復または構成変更後に、管理画面からベンチを再実行してください。

## 設定の復元と更新

```bash
export AWS_PROFILE=isucon2026
pulumi login s3://isucon2026-pulumi-state-124502390568/supervisor
cd provisioning/aws/supervisor
cp Pulumi.portal-dev.yaml.example Pulumi.portal-dev.yaml
pulumi stack select portal-dev
pulumi preview --diff
pulumi up
```

このstackは秘密値を設定に保存していません。
初期作成時のpassphrase providerを使う操作では、`PULUMI_CONFIG_PASSPHRASE=''`を設定してください。
既存secretの値を更新した場合は、各サービスで新タスクを起動し直して値を反映します。
更新と削除は実行中ジョブのない時点で行います。

## 2026-10-04のSupervisor設定修正と再検証

`benchmarkScale: large`を追加し、ベンチへ`--scale large`を渡すようにしました。
RouterへのHostを`app.teamN-aws.127.0.0.1.nip.io`へ変更し、国コードに応じて3リージョンへ振り分けるようにしました。
従来のsmall設定と固定app Hostが、team1の投稿件数不一致とregion_mismatchの原因でした。
team1のジョブ4はSupervisor経由でPASS、15,226点、終了コード0となり、実ポータルへの結果保存を確認しました。
team2・3にも設定を反映しましたが、Router停止中のため実ベンチは未確認です。
IaCのテスト15件と型検査を通過し、配備後のPulumi previewは25リソースすべて変更なしでした。

## NAT Gatewayの集約

2026-10-04、ポータルとSupervisorの出口をNAT `nat-0de08f47e08354915`、EIP `52.68.140.39`へ集約しました。
ポータルの2つのprivate subnetは同じNATを参照します。
SupervisorはポータルVPC `vpc-0e2be8c91c07a48f3`、private subnet `subnet-055a5ee88a1c5764b`へ移しました。
固定送信元IPは以前の`54.248.191.93`から変わり、RouterのTCP/8080許可も新しい`/32`に更新しました。
旧ポータルNAT、旧Supervisor NATと専用VPCは削除対象としました。
上の初回配備表にある固定送信元IPとNAT構成は、現在の設定ではありません。

集約後、利用可能なNAT Gatewayは1個であることをAWS APIで確認しました。
ジョブ5は共有NAT経由でPASS、16,123点、終了コード0となり、ポータルへの結果保存を確認しました。
東京リージョンのEIP quotaは、10チームのRouterと共通NATを想定して20への増枠を申請しました。
申請IDは`c216c5d77a774a40b26b0c46085dba3aavwx8FwV`で、送信後の状態はPENDINGです。

## team2・3のRouter復旧と受け入れ検証

2026-10-04、Supervisor stackでRouterのprimary ENIにEIPを割り当てました。
team2は`13.114.71.155`、team3は`13.193.253.167`です。
Supervisorの接続先はこのEIPから生成し、停止・起動によるIP変更を防ぎます。

初回のジョブ6・7は初期化HTTP 503で失敗しました。
Routerの停止・起動後にcloud-initがdata NICのDHCP設定を再生成し、既存の静的netplan設定と競合していました。
その結果、アプリ・DB subnetへの静的経路が失われ、アプリがDBへ接続できなくなっていました。
`provisioning/scripts/router_network.py`で重複するdata NIC設定を除去し、cloud-initのネットワーク再生成を無効化しました。
両Routerを再起動し、data NIC経由のアプリ・DB経路が維持されることを確認しました。
修復時の一時SSH ingressは検証後に削除しました。

| ジョブID | チーム | 最終状態 | 点数 | 終了コード |
|---|---|---|---:|---:|
| 8 | team2 | FINISHED / PASS | 227,226 | 0 |
| 9 | team3 | FINISHED / PASS | 16,487 | 0 |

両結果は実ポータルに保存されました。
上記の過去の未完了事項は、この再検証で解消しました。

## 参加者向け進行ログの公開

2026-10-04、Supervisor実行時のシナリオ進行ログをstdoutへ出力するbenchを配備しました。
team1〜3のtask definitionはrevision 5、イメージdigestは
`sha256:68668195782ae546705299028fc5f957afd90634ea639ad9cd64c9ff0f7de183`です。
詳細な結果JSONと内部エラーはstderrへ出力します。

team1のジョブ13はPASS、15,887点でした。
参加者APIでstdoutの31行に初期化・検証・負荷実行・投稿期間開始・最終検証の進行ログと最終結果が含まれることを確認しました。
詳細JSONは管理者APIのstderrに保存され、参加者APIのstderrは空でした。
Pulumi previewは19リソースすべて変更なしでした。
ビルド用の一時CodeBuild project、S3 source bucket、IAM roleは削除しました。

## 参加者サーバーの表示IP更新

2026-10-04、停止・起動で変わったteam2・3のapp1〜3の公開IPをportalへ反映しました。
team2は順に`3.112.190.233`、`54.249.185.225`、`3.112.234.132`、
team3は`54.238.146.111`、`13.112.120.193`、`52.193.198.127`です。
SupervisorのtargetOverridesにも新IPを追加し、既存のRouter EIPへの転送を維持しました。
更新後、全9台のportal表示IPとEC2の実IPが一致し、3つのSupervisorがrevision 6で稼働していることを確認しました。

## ベンチ用EC2の撤去

2026-10-04、Fargate Supervisorへの統一に伴い、停止中のCLIベンチ用EC2
`i-0057610e0acc5b38c`とroot EBS、専用IAM role・instance profile・policy、
Security GroupとRouterへの許可ルールをPulumiで削除しました。
途中まで作成していたteam2用資源も削除し、`trial`・`team2`・`team3`の
ベンチ用EC2スタックを削除しました。リポジトリからもEC2用定義・設定例・テストを除去しました。
EC2のterminated、EBSの消失、ベンチ用スタック一覧が空であること、
Fargate Supervisorが3チームとも1タスク稼働していることを確認しました。

## team4追加と共有Router ingressの復旧

2026-10-04、Portalにtrial-team4（moratorium08）とtrial-team5（メンバー未定）を登録し、
team4・5のFargate Supervisorを追加しました。
team5の参加者EC2作成は保留しました。
team4の参加者EC2は別セッションで作成されたため、このPRではそのIaCを管理しません。
こちらで試行した参加者EC2用スタックの未使用資源は削除しました。

team4のPortalには次の3台を登録しました。

| サーバー | Instance ID | 公開IP |
|---|---|---|
| app1 | i-0499a0a74e75d0fce | 54.238.192.11 |
| app2 | i-0f0214d51576abffc | 54.168.241.156 |
| app3 | i-0ae9e8673396aaa9e | 13.113.253.143 |

app1〜3とdb1の`isucon`ユーザーにmoratorium08のGitHub公開鍵3本を登録し、
各サーバーで登録内容・所有者・権限0600を確認しました。

10:26:04 JSTに共有Router Security Group `sg-09663cf353f2246e7`から
`52.68.140.39/32 → TCP 8080`が削除されたことをCloudTrailで確認しました。
team3のジョブ14・16は、この状態で初期化リクエストが30秒でタイムアウトしました。
許可を復元し、team1〜4の全Routerが同じSecurity Groupを使用することを確認しました。
既存Supervisor定義には許可ルールがあるため、READMEにスタック間の管理競合を記載しました。
参加者環境側の管理方式の変更は、このPRでは行っていません。

全チーム検証では、team1のRouter公開IP変更とteam4の接続先未登録も確認しました。
team1の転送先を`54.249.70.149`へ更新し、team4の3つのapp公開IPから
Router `52.195.195.61`への転送設定を追加しました。

復旧後の実行結果は次のとおりです。

| チーム | ジョブID | 結果 | 点数 |
|---|---:|---|---:|
| team1 | 22 | PASS | 16,212 |
| team2 | 20 | PASS | 303,736 |
| team3 | 18 | PASS | 16,311 |
| team4 | 23 | FAIL（context deadline exceeded） | 0 |

team4は初期化・負荷試験・最終検証まで進みましたが、全体の時間超過で失敗しました。
接続先未登録による失敗は解消済みです。時間超過の根本原因は未調査です。
team5は参加者環境がないため実行していません。
設定変更後、SupervisorのPulumi適用は5サービスの更新・5 task definitionの置換でした。
既存の型チェックと15件のテストは通過しました。
