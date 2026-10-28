# webapp-optmized-astra

Team 10向けのRust最適化実装。`webapp/rust`と`webapp/sql`を2335d5fから複製した。
作業期限は2026年10月28日11:00 JST。各ベンチ結果をこのディレクトリに記録し、コミットしてpushする。

<!-- astra-final-summary-start -->
## 09:00時点の採用構成と確認

最高点は[**95,017点・PASS**](results/20261027T234159Z-team10/result.json)。採用構成を再配置した最終確認は[93,597点・PASS](results/20261027T235343Z-team10/result.json)だった。各走行のソース、配布物のSHA-256、ベンチイメージは対応する`experiment.json`に記録している。

メタデータはAsiaのプライマリから欧州・米州へ複製し、新規画像のバイト列は既存AsiaアプリVMの独立PostgreSQLへ保存する。欧州・米州では投稿画像とリアルモジを受信時に保持し、Asiaでも投稿画像を保持する。画像キャッシュは1GiB・16,384件、署名計算のキャッシュは65,536件、Tokioは2スレッド。画像の公開状態・現在の鍵・有効期限と、更新応答前の両レプリカへの反映確認を維持する。

3地域での再起動前後の画像一致を確認済み。構成比較と未計測の候補は[DB構成とVMの役割の比較](architecture-evaluation.md)にまとめた。
<!-- astra-final-summary-end -->

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
nginxの接続上限は`runtime.json`の`nginxWorkerConnections`と`nginxWorkerRlimitNofile`で管理する。
検索改善版の負荷試験で従来の768接続を使い切り、AsiaでEOF・502が増えてFAILしたため、
各ワーカー8,192接続・65,536ファイル記述子へ変更する。反映前に`nginx -t`で検証する。

## 地域別の独立画像DB

`regionalImageStoresEnabled`で欧州・米州の既存アプリVMにもポート5433の独立PostgreSQLを用意する。既存5432のメタデータレプリカは維持する。`apply.sh image-store`でクラスタを作成し、`apply.sh database`で画像参照列を64文字へ広げ、書き込み無効のまま`apply.sh application`で全プロセスへ読み取り対応を配置する。

その後`regionalImageWritesEnabled=true`を反映すると、欧州・米州の投稿画像は受信地域でJPEG検証と永続化を行い、Asiaの更新ワーカーへ署名付き参照だけを転送する。Asiaは通常どおり認証・地域・公開条件を確認し、メタデータのコミットと両レプリカへの反映完了後に応答する。プロフィールとリアルモジは従来の保存方式を使う。

地域間の認証鍵はgit管理外の`var/astra/.regional-upload.env`に生成し、既存3台へ`/home/isucon/.astra-upload.env`としてscpで配る。いずれも権限600。Pulumiにはファイルパスと機能フラグだけを記録し、AWSリソースは追加しない。

参照は`europe:<UUID>`または`america:<UUID>`。従来のUUID参照はAsiaへ接続する。書き込みを無効化しても地域別の読み取り対応と保存DBは残す。公開中の地域別参照がある状態で読み取りを外すデプロイは拒否する。旧バイナリへの復帰は、対応バイナリで初期化して地域別参照がなくなったことを確認した後に行う。既存GCはAsiaの孤立画像だけを対象とし、地域別DBのデータは削除しない。

地域別の永続化・取得・ZIP・削除確認は、独立した3個目のローカルPostgreSQLを`ISUREAL_REGIONAL_ASSET_TEST_DSN`で指定して実行する。署名は本文、認証ヘッダー、Content-Type、クライアントIP、URL、画像参照を束ね、実際の接続元IPと地域が一致した場合だけ受け入れる。

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

RustのDB接続を伴うキャンセル検証と通知・並行登録・画像配信・フィードの結合テストは`ISUREAL_SESSION_TEST_DSN`がなければスキップされる。
通知テストは一時スキーマを作り、接続時刻、通知順序、地域変更・退会・不正な署名鍵、接続切断、ポーラーの再起動を確認する。
単体テストだけではAPI整合性は証明できないため、Team 10の実ベンチ結果を判定に使う。

読み取り前のWAL確認は、専用のローカルプライマリと物理レプリカで検証する。
`ISUREAL_FENCE_PRIMARY_DSN`と`ISUREAL_FENCE_REPLICA_DSN`を指定してRustテストを実行すると、
レプリカを一時停止し、並行リクエストの待機、最新値の取得、時間切れ時の503、削除済み利用者への通知抑止を確認する。
テスト終了時にはレプリカの反映を再開する。

`ISUREAL_FENCE_REPLICA2_DSN`も指定すると、更新レスポンスの反映待ちを2台の物理レプリカで検証する。
片方を停止してレスポンスが待機する間も、同じ行への別トランザクションが更新できること、
反映を再開すると両レプリカから更新結果を読めることを確認する。


## DB複製の切り替え

`runtime.json`で`replicasEnabled=true`、`replicasSynchronous=false`、`replicaReads=false`として
`deploy/apply.sh replication`を実行し、両レプリカのベースバックアップと追従を確認する。
次に`replicasSynchronous=true`へ変更して同じコマンドで`remote_apply`を有効にする。
最後に`replicaReads=true`として`deploy/apply.sh application`で読み取りを地域内のDBへ切り替える。
アプリは起動時に同期設定とレプリカのリカバリー状態を検証する。
レプリカの設定ファイルが変わらずDBも稼働している場合は、設定の再適用で再起動しない。
同期方式の比較時にレプリカの共有バッファを保持する。初回配置・設定変更・停止中の場合は起動し直す。

同期反映待ちを読み取り側へ移す場合は、先に`readFenceEnabled=true`として
`deploy/apply.sh application`で地域内アプリにWAL確認を有効化する。
その後`replicasSynchronous=false`として`deploy/apply.sh replication`を実行する。
同期反映へ戻す場合は、先に`replicasSynchronous=true`をDBへ反映してから読み取り側の確認を無効にする。
デプロイ時には、地域内読み取りが有効なのに同期反映もWAL確認もない状態を拒否する。

WAL確認は、読み取り要求が届いた後にプライマリの永続化済みLSNを取得し、地域内レプリカがその位置まで
反映したことを確認してから認証・閲覧判定を始める。同時に到着済みの要求だけを一括確認し、以前の結果は使い回さない。
通知ポーリングも同じ確認を行う。10秒以内に追従しなければ、古いデータを返さず503または通知接続の終了とする。

反映待ちを更新レスポンス側へ移す場合は、まず`readFenceEnabled=true`を維持したまま
`writeFenceEnabled=true`として`deploy/apply.sh application`を実行する。
全地域の通常アプリとasiaの内部更新ワーカーに設定が入った後で、`readFenceEnabled=false`として
もう一度アプリを反映する。スクリプトは全更新プロセスの稼働と実際の設定を確認してから読み取り側の確認を外す。
更新ハンドラのコミット後に両レプリカのWAL反映を待つため、地域間の反映待ち中にはDBのロックを保持しない。
GET/HEAD以外はエラーレスポンスも含めて確認し、30秒以内に追従しない場合は503とする。
クライアントが切断しても更新処理と反映確認は継続する。確認が失敗した場合、更新自体はコミット済みの場合がある。
この方式を解除するときは、先に同期レプリケーションか全読み取り側の確認を有効にする。

`writeFenceMode=native`では、通常更新とは別の小さなトランザクションで`remote_apply`の完了を待つ。
まず`replicasSynchronous=true`をDBへ反映し、`deploy/apply.sh database`で007の空テーブルを追加する。
同期設定を維持したまま`writeFenceMode=native`として全アプリを反映した後、
`replicasSynchronous=false`をDBへ反映する。この場合は両同期スタンバイ名を残して通常のコミットを
`local`へ変更し、レスポンス待ち専用トランザクションだけが`remote_apply`を使う。
通常更新のロックは保持せず、専用テーブルへの挿入・削除とそのコミット記録で、それ以前の更新の反映を確認する。
テーブルは各トランザクション完了後に空になる。`poll`へ戻す場合も先に同期設定を復旧する。
反映確認はプロセスごとに最大4組を並行させ、到着済みの更新を1ミリ秒まとめて確認する。
更新の実行枠は、リクエスト本文256KiBあたり1枠・最低1枠で、プロセスごとに32枠とする。
8MiB以上または長さが不明な本文は全枠を使い、SQLコミット後も両レプリカへの反映確認まで保持する。
これにより、画像更新が複製の速度を上回って大量に先行するのを抑える。GET/HEADはこの枠を使わない。
実行待ち中はDBトランザクションを開始せず、反映待ち中もアプリの行ロックを保持しない。
ローカルDBへの永続化とトランザクションごとの設定変更は
[PostgreSQL 16の同期設定](https://www.postgresql.org/docs/16/runtime-config-wal.html#GUC-SYNCHRONOUS-COMMIT)に従う。

単一DBへ戻す際は、先に`replicaReads=false`として`deploy/apply.sh application`で全台を切り替え、
その完了後に同期レプリケーションを解除する。初期AMIのDBは各VMの`16/main`に保持する。

## 更新APIの地域間転送

`runtime.json`の`writeProxyEnabled=true`では、europeとamericaのnginxがGET/HEADを地域内アプリへ、
それ以外のAPIをasia/app1の内部ワーカー`192.168.145.10:8081`へ転送する。
更新トランザクションをプライマリDBの近くで実行し、SQLごとの地域間往復を減らす。
転送は既存ルーターのデータ通信経路を使い、固定リージョン、遅延、帯域の設定は維持する。

内部ワーカーはapp2/app3の接続元IPだけを受け付け、元の受付地域とユーザーの所属地域を比較する。
外部から渡された地域ヘッダーは判定に使わない。各VMの`ISUREAL_REGION`も変更しない。
設定・systemd・nginxの変更は`deploy/apply.sh application`で反映する。
`writeProxyEnabled=false`に戻すと、先に各地域の更新先をローカルへ戻してから内部ワーカーを停止する。

## 最適化の対象

- 未最適化Rust版の基準測定。
- 投稿のロック、署名計算、通知、SQL往復の最適化。
- JPEGは成功した検証結果を最大4096件保持し、初回は圧縮データ全体を検査しながら画素展開量を減らす。
- 画像のDB保存は`STORAGE EXTERNAL`を使い、TOASTの圧縮試行を省く。計測で`pglz_compress`がプライマリCPUの26.55%を占め、抽出した画像300件はいずれも圧縮されていなかった。画像は引き続きPostgreSQLへ保存し、取得するバイト列も維持する。この設定は今後の書き込みに適用され、既存行は書き直さない。[PostgreSQL 16の保存方式](https://www.postgresql.org/docs/16/sql-altertable.html#SQL-ALTERTABLE-DESC-SET-STORAGE)に従い、既存の圧縮済み行と新規行の読み取りをローカルDBで確認した。
- 画像配信は現在の閲覧権限と署名鍵を毎回確認する。画像データは最大128MiB・4096件まで保持し、DB行の`xmin`と作成時刻が変われば取得し直す。キャッシュがある場合は既知の作成時刻で検索先の月別テーブルを絞り、見つからなければ全体を再検索する。移動・削除・同じIDでの再作成や所有者の鍵変更も検証する。
- SSE通知は地域内DBごとに100ミリ秒間隔で最大512接続ずつ一括確認する。接続ごとの通知時刻とユーザー状態は毎回確認する。
- 通知の検索は開いている投稿期間を列挙し、期間・ユーザーの一意索引から該当通知を探す。各期間の最初の通知から全体の最初の1件を選ぶため、履歴を毎回走査せずに従来の順序を保つ。ユーザー削除時の外部キー確認用には、ユーザーIDだけの小さな索引を残す。
- 通知のユーザー・投稿期間ペアの一意制約は、索引の先頭を投稿期間に変更する。同じ期間の通知をまとめて書き込み、一意性と全通知の保存を維持しながら履歴索引へのランダムアクセスを減らす。
- 通知IDは標準のUUIDv7で生成し、対象ユーザー順に一括登録する。対象者の判定、通知時刻、ロック、トランザクションは維持し、主キーの書き込みも時系列でまとまるようにする。
- フィードは閲覧条件・友人関係・ブロック・投稿済み条件を1回の問い合わせで評価し、現在の投稿期間ごとに最大51件へ絞り、全地域の上位51件だけを取得する。従来のRustの判定との比較テストでページ送りと状態変更を検証する。
- 投稿詳細とコメント・リアクションの権限判定は、対象投稿に必要な友人・2段階の関係・ブロック・投稿済み条件を1回で確認する。友人グラフ全体をアプリへ転送せず、従来のRust判定との比較で地域変更、投稿期間終了、退会、削除、本人の投稿を含む条件を検証する。
- 新規登録はユーザー名の一意制約と単一INSERTで処理する。同名の並行登録は1件だけ成功し、別名の登録は互いをブロックしない。
- ユーザー検索はASCII文字列を簡単な小文字変換で処理し、Unicode正規化の結果を元の文字列ごとに最大16,384件保持する。名前・ブロック・退会状態はDBから毎回読み、検索条件や権限の結果はキャッシュしない。NFC・case folding・NFCの順序と部分一致の意味は維持する。
- エクスポートのZIP生成は最大2本のブロッキングワーカーで処理する。DBの行を4件までのチャネルで渡し、画像全件と完成ZIPを同時に保持する負荷を抑える。
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
| 20261027T134205Z-team10 | distributed DB with concurrent single-statement registration | PASS | 16,665 |
| 20261027T134837Z-team10 | distributed DB with versioned image cache and single-query authorization | PASS | 18,075 |
| 20261027T135518Z-team10 | distributed DB with bounded asynchronous export pipeline | PASS | 17,555 |
| 20261027T140308Z-team10 | regional reads with HTTP write forwarding near primary DB | PASS | 45,790 |
| 20261027T140625Z-team10 | repeat regional reads and HTTP write forwarding | PASS | 38,158 |
| 20261027T142139Z-team10 | bounded regional feed queries with SQL visibility filtering | PASS | 42,375 |
| 20261027T145324Z-team10 | asynchronous replicas with fresh batched WAL read fences | PASS | 32,193 |
| 20261027T151601Z-team10 | post-commit write response fences release locks before replica replay | PASS | 35,586 |
| 20261027T153400Z-team10 | native replica response barriers with durable local application commits | PASS | 36,101 |
| 20261027T154339Z-team10 | prune image cache lookups by creation timestamp with fresh permission checks; restore synchronous commits | PASS | 37,148 |
| 20261027T155128Z-team10 | cluster notification uniqueness checks by posting window | PASS | 45,932 |
| 20261027T160125Z-team10 | generate ordered UUIDv7 notification batches while preserving recipient transactions | PASS | 45,956 |
| 20261027T161141Z-team10 | probe open windows for notifications and replace historical polling index with compact user index | PASS | 45,394 |
| 20261027T163124Z-team10 | single-query post and interaction permissions; CPU and lock profiling | PASS | 44,935 |
| 20261027T165020Z-team10 | retest native response barriers after notification and permission optimizations; preserve standby caches | PASS | 50,018 |
| 20261027T165726Z-team10 | cache exact Unicode search normalization with ASCII fast path | FAIL | 0 |
| 20261027T170518Z-team10 | raise nginx worker connection capacity after observed EOF and 502 saturation | PASS | 43,902 |
| 20261027T171057Z-team10 | skip ineffective TOAST compression of image bytes while preserving PostgreSQL storage | PASS | 38,237 |
| 20261027T204142Z-team10 | bound pending mutation bytes and pipeline replica response barriers | PASS | 50,680 |
| 20261027T205127Z-team10 | fetch full search profiles only for matched users | PASS | 50,565 |
| 20261027T205643Z-team10 | reserve bounded write capacity for small mutations | PASS | 59,727 |
| 20261027T210236Z-team10 | prune export image partitions using post timestamps with fallback | PASS | 58,813 |
| 20261027T210922Z-team10 | reuse post timestamps as bounded cold image partition hints | PASS | 59,748 |
| 20261027T211543Z-team10 | expand bounded image byte cache from 128 to 512 MiB | PASS | 59,774 |
| 20261027T212850Z-team10 | independent image store capable build with replicated post image writes as control | PASS | 61,313 |
| 20261027T213422Z-team10 | store new post image bytes in independent Asia application PostgreSQL | PASS | 74,309 |
| 20261027T214135Z-team10 | reserve bounded posting window control admission with independent image storage | PASS | 76,087 |
| 20261027T215923Z-team10 | warm immutable post image bytes in the owning region cache | PASS | 72,343 |
| 20261027T220520Z-team10 | disable regional cache pushes to compare network contention | PASS | 75,948 |
| 20261027T221041Z-team10 | avoid full owner scans and directly query known image partitions | PASS | 83,899 |
| 20261027T221656Z-team10 | reuse durable identical images and cache immutable assets by storage key | PASS | 80,221 |
| 20261027T222341Z-team10 | widen upload admission to 16MiB and restore login guard journal matching | PASS | 85,575 |
| 20261027T222843Z-team10 | disable ineffective asset content cache while keeping 16MiB upload admission | PASS | 82,055 |
| 20261027T223517Z-team10 | store new profile and realmoji images independently from metadata replicas | PASS | 82,378 |
| 20261027T224052Z-team10 | increase permission-checked image byte cache from 512MiB to 1GiB | PASS | 83,654 |
| 20261027T224746Z-team10 | retain recently accessed image bytes with bounded CLOCK eviction | PASS | 85,570 |
| 20261027T225742Z-team10 | Cache uploaded post images at regional ingress; retain fresh metadata authorization | PASS | 90,104 |
| 20261027T230256Z-team10 | Retain regional post and realmoji upload images; fresh immutable-key validation | PASS | 87,734 |
| 20261027T232844Z-team10 | Increase image cache entry limit to 16384 within 1 GiB after orphan cleanup | PASS | 87,642 |
| 20261027T233258Z-team10 | Separate small image uploads from account mutation admission slots | PASS | 89,592 |
| 20261027T233712Z-team10 | Retain Asia post upload bytes locally without cross-region cache push | PASS | 89,306 |
| 20261027T234159Z-team10 | Expand pure image signature memoization from 8192 to 65536 entries | PASS | 95,017 |
| 20261027T234548Z-team10 | Compare four Tokio workers per application with unchanged validation and write fences | PASS | 93,852 |
| 20261027T234936Z-team10 | Compare regional post-only ingress caching with two workers and larger signature cache | PASS | 92,594 |
| 20261027T235343Z-team10 | Final verification of selected two-worker regional image and signature caching configuration | PASS | 93,597 |
| 20261028T004046Z-team10 | regional image DB readers; Asia-write control | PASS | 92,830 |
