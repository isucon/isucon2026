# 競技ポータルのデプロイ

さくらのクラウド is1c の Ubuntu 26.04 LTS VM に、Ansible で Go ポータルを配置する。
現在の開発 URL は `https://portal-dev.isucon.whywrite.it/`。
API キーがない場合はコンソールで VM / LB / DNS を作成し、静的 inventory で接続する。
レジストリを使わず、コミット済みのソースから VM 上でイメージをビルドできる。

## 構成

```text
Internet → Sakura LB (TLS 終端・80→443) → nginx :80 → Swarm ingress :8080
  Docker Swarm stack portal
    portal :8081 / worker / notifyloop / bot（トークン設定時）
    PostgreSQL 18.6   volume: portal_pgdata18
    Redis 8.2.9      volume: portal_redisdata82
```

DB と Redis は同居構成で、外部には公開しない。
VM の TCP 80 は LB の送信元だけに許可し、8080 はクラウド側のパケットフィルタで遮断する。
nginx は公式 stable 1.30.5。PostgreSQL / Redis のイメージは digest で固定する。
選手環境の EC2 / CloudFormation とアイコン保存先 S3 は AWS を使う。

## 手順と設定

- [Ansible の実行・秘密情報・バックアップ・更新手順](ansible/README.md)
- [外部連携に必要なアカウント・設定値・本番公開前の残タスク](external-integrations.md)
- [Terraform によるクラウドリソース作成](terraform/README.md)（API キーを用意できる場合）

既定では日時を未設定とし、運営限定モードで起動する。
開催日は **2026年10月31日（土）**。
開始、凍結、終了などの時刻は未設定であり、確定後に `portal_overrides.timing` に RFC3339 で設定する。
デプロイ例は Ansible README に集約している。

GitHub Actions からのレジストリ配布は任意。
`PORTAL_DEPLOY_ENABLED=true` と接続先・SSH ホスト鍵・各 secret を設定した場合に利用できる。
開発は main の CI 成功後に配布、本番はイメージ作成とデプロイを手動起動する。

## 本番投入前

登録データ移行、外部連携の実接続試験、900 チーム以上の負荷試験、別障害領域へのバックアップと復元試験が必要。
PostgreSQL のマネージド化は保留であり、この構成は HA を提供しない。
