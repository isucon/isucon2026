# Router data 側 HTTP(S) ブロックの一括適用

PR #367 より前のテンプレートで作られた CloudFormation 環境の Router に、`192.168.10.10` 宛ての 8080/443 を落とす nftables ルールを入れます。
ファイルの作成と unit の起動には、CloudFormation の Router UserData と同じコマンド（`main.go` の `blockCommand`）をそのまま使います。

## 手順

portal ディレクトリで実行します。

```sh
# 1. 本番 DB から対象を取り出す
./cmd/router-data-proxy-block/list-targets.sh > targets.tsv

# 2. 変更せずに各 Router の状態を確認する
go run ./cmd/router-data-proxy-block -targets targets.tsv

# 3. 1 チームだけ適用して結果を確認する
go run ./cmd/router-data-proxy-block -targets targets.tsv -teams 123 -apply

# 4. 残りに適用する
go run ./cmd/router-data-proxy-block -targets targets.tsv -apply
```

`list-targets.sh` は `portal-production` の portal サービスから DB の接続先、認証情報、ネットワーク、ロググループを読み取ります。
使い捨ての Fargate タスクで `targets.sql` を読み取り専用トランザクションとして 1 回実行し、終了後に task definition を deregister します。
CloudWatch Logs に `targets-complete` の行がなければ失敗として扱います。

## 接続

asia サーバー（number 1）の公開 IP に `isuadmin` で SSH し、ProxyJump で Router の `192.168.10.10` に入ります。
鍵は Secrets Manager の `portal-production/admin-ssh-key` から一時ディレクトリに書き出し、終了時に消します（`-key` でファイルも指定できます）。
asia サーバーは参加者が操作できるので、ssh-agent は使わず、エージェント転送もしません。
ホスト鍵は初回に記録し、`-state` ディレクトリの `known_hosts` にインスタンス ID ごとの別名で保存します。

## 判定と適用

まず各 Router を読み取りだけで調べ、次のどれかに分類します。

| 結果 | 意味 |
| --- | --- |
| `done` | ブロック、unit、baseline がすべて反映済み |
| `would-apply` | 未適用。`-apply` で適用する |
| `applied` | 今回適用した |
| `skipped` | タグ同期で登録された Router、asia サーバーが未登録・停止中、baseline が未記録など |
| `failed` | 接続できない、別のインスタンスにつながった、ruleset が baseline と食い違うなど。この Router は変更しない |

envcheck は起動時に記録した `/var/lib/isucon-envcheck/baseline.json` と、Router の nftables ruleset をテーブルの順番まで比べます。
起動時の順番は「ブロック → `isureal_router`」です。ブロックの unit は multi-user.target に入り、`isureal_router` は cloud-final の後に bootstrap が作るからです。
そのため `-apply` は次の順に処理します。

1. `blockCommand` を実行する。ブロックのテーブルは末尾に作り直される
2. `isureal_router` を 1 回の nft トランザクションで削除して作り直し、起動時と同じ順番にする。転送が止まる瞬間はない
3. ブロック以外の ruleset が baseline と一致することを確かめてから、baseline の Router の `nft` 項目だけを新しい ruleset に置き換える。ほかのバイトは変えず、元のファイルは `baseline.json.before-data-proxy-block` に残す
4. もう一度調べて、baseline と ruleset が期待どおりであることを確かめる

結果は標準出力と `-state` ディレクトリの `results.jsonl` に出ます。何度実行してもよく、済んだ Router は `done` になります。

## 注意

- 適用後に `isucon-data-proxy-block.service` を restart しないでください。ブロックのテーブルが末尾に移り、再起動するまで envcheck が不一致を報告します。
- 適用の確認のために envcheck を実行しないでください。結果がポータルに送信されます。
- `testdata/gen.sh` は、権限のないネットワーク名前空間でテスト用の ruleset を作り直すスクリプトです。
