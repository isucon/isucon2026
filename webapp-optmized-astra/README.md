# webapp-optmized-astra

Team 10向けのRust最適化実装。`webapp/rust`と`webapp/sql`を2335d5fから複製した。
作業期限は2026年10月29日10:00 JST。各ベンチ結果をこのディレクトリに記録し、コミットしてpushする。

<!-- astra-final-summary-start -->
## 現在の構成と検証状況

2026年10月29日10:00 JSTまで比較を継続中。最高は[195,259点・PASS](results/20261028T172332Z-team10/result.json)、同じ設定の反復は[162,498点・PASS](results/20261028T172715Z-team10/result.json)。以前の構成では別seedも[185,577点・PASS](results/20261028T155749Z-team10/result.json)。単発の差を各変更の効果とは扱わず、FAILを含む全走行のソース・配布物SHA-256・ベンチイメージを各experiment.jsonに記録する。

メタデータはdb1を主としてEU・USへ物理複製し、更新成功応答前に両地域への反映を確認する。Asiaアプリの追加レプリカは検索・フィードだけに使う。画像本体は3地域のファイルへ保存し、SQLには所有者・公開状態・保存先を残す。初期画像1,826,950枚・14,054,519,155バイトは25パックへ移行済みで、SQL内の画像本体はゼロ。新規画像もファイルとディレクトリの永続化後に公開する。AWSリソースは追加せず、秘密情報は非公開envと署名ファイルをscpで共有する。

画像キャッシュは512MiB・16,384件、発行済みURLは現在の鍵と画像IDをキーに最大262,144件保持し、事前計算した署名も再利用する。元の有効期限を維持し、残り60秒で更新する。時計巻き戻り専用の処理はない。現在の鍵・画像の有効性・閲覧権限は取得ごとに検証する。通常SQL接続枠は16、Tokioは2スレッド、画像本文の重み付き処理枠は64、反映待ちを含む画像要求全体も64件まで。本文処理後に重み付き枠を返し、応答前の両地域への反映確認は維持する。db1の共有バッファ1GiBは改善を確認できず512MiBへ戻した。

Unicode検索のGIN索引、JPEG検証、ZIP出力、接続再利用、通知の期間別配列保存とSQLの不要な走査・ソート削減、ThinLTOを適用した。アプリログはerror、Nginxアクセスログは無効。失敗ログイン401はログレベルと無関係に記録して監視を維持する。Nginxのリクエストバッファリングは有効。本文バッファは1MiBへ増やし、Nginxのmalloc閾値を128KiBへ固定した。反復時のNginx平均RSS合計はAsiaで約403→66MiBへ減り、固定後のスコアは174,417点・174,674点でPASS。スコア改善は未確定。ログ削減版の実測は34行・4,104バイトで、すべて失敗ログインだった。

[画像保存のファイル操作削減](experiments/direct-image-files/README.md)は178,502点・181,905点でPASS。最終名を排他的に作成し、永続化が完了してからSQLに公開する。[アプリのmalloc閾値128KiB](experiments/application-malloc/README.md)は192,384点・164,303点でPASS。反復時のAsiaアプリ平均RSS合計は約1,203→886MiBへ減ったが、初回スコアは再現しなかった。メモリ削減として維持し、スコア改善とは扱わない。GC・再起動後の18画像と実行設定は一致した。

[登録・ログインの段階別計測](experiments/auth-timing/README.md)では、Asiaの遅い要求で受付待ちが支配的だった。[認証枠を分ける比較](experiments/auth-admission/README.md)は128＋128枠で166,616点、64＋64枠で177,380点でPASSしたが、全体性能の改善を確認できず、元の共有128枠へ戻した。診断無効での復帰は179,960点・PASS。

[画像本文処理と反映待ちの枠を分けた比較](experiments/image-response-admission/README.md)は、画像要求全体64件で185,395点・184,915点、どちらもPASS。Asiaの投稿中央値は506ms・538msだった。256件上限よりスコアが高く、64件を維持する。認証・投稿の診断ログは無効にしている。

[db1に初期画像を複製する比較](experiments/db1-archive-peer/README.md)では、準備のみのローカル読み取りが188,226点、db1経由が174,918点、aio threads併用が179,400点・187,288点でPASS。Asiaのディスク読み取りは減ったが画像応答は遅くなり、改善を確認できなかったためローカル読み取りへ戻した。db1のコピーは保持し、private配信は無効にした。

[releaseのRustテスト52件とコピー処理5件が成功](experiments/db1-archive-peer/validation.json)。性能測定用1件は除外。実PostgreSQLとレプリカを使い、画像API・ZIP本文、キャッシュ後の鍵変更・削除などを検証した。db1配信を含む再起動後の18画像と75範囲も一致した。準備時の登録・画像検証タイムアウト、負荷中の通知購読503は未解決で、失敗記録を残している。

[DB構成とVMの役割の比較](architecture-evaluation.md)に、独立画像DB、MySQL予備比較、地域別DBへの分割、db1の追加用途を記録する。
<!-- astra-final-summary-end -->

## Team 12への移設（2026-10-29）

Team 10のVMを置き換えたため、同じ最終構成をTeam 12へ配置した。問題サーバーAMIはami-07743933837cd476f（10-27までの初期データを焼き込み済み）、ルーターAMIはami-02ec75ef123258866。app1/asiaはi-055f64431d3006a5f・192.168.177.10、app2/europeはi-0a062905e0bb9ebb7・192.168.178.10、app3/americaはi-03c92149bdc1fcfe1・192.168.179.10、db1はi-000e45b5dad0a1b39・192.168.180.10。`deploy/runtime.json`、`deploy/ssh.sh`、Pulumiの`astra`スタック、各デプロイスクリプト、Rustの接続元検証とテストのアドレスをこの値へ書き換えた。`deploy/benchmark.sh`はTeam 12と現行mainのベンチイメージ（sha256:55649c0a…）を使い、結果を記録するだけでコミットもpushもしない。

mainの2335d5f以降の変更から、友達の友達フィードのおすすめ順（8e12918）と、10月の画像パーティションを10-28 06:00 UTCで`image_objects_202610`と`image_objects_20261028`に分けるデータ（9b10bb8）を取り込んだ。友達の友達フィードは、表示できる候補すべてに`10 × E + 3 × M + min(C, 30)`を閲覧判定と同じスナップショットのSQLで付け、`(score, postedAt, id)`の降順で51件を取る。`beforeScore`は3つの続きの値が揃わなければ400、ほかのフィードでは無視する。時刻順のフィードは従来どおり投稿期間ごとに51件へ絞る。画像の検索ヒントは分割後の時刻を`image_objects_20261028`へ向ける。reset.sqlはパーティション名で比較するので、分割後の区画もTRUNCATEされ、変更は要らなかった。

新しいVMでは次の順に反映した。各段階の事前確認は前の段階で動いているプロセスの設定を見るため、まとめて`all`では通らない。

1. 書き込みを無効にした読み取り構成で`database`、`image-store`、`replication`（非同期、2台のベースバックアップに約12.5分）、`application`を実行する。`disable-asia-replica.sh`は、Asiaのスタンバイを作ったことがないVMでは何もせずに終わるよう直した。
2. `replicasSynchronous=true`で`replication`、続けて`replicaReads=true`で`application`。
3. 書き込みワーカーとnative fenceを先に`application`で配置し、そのあと画像・ファイル・地域別の書き込みを有効にして`application`。書き込みの事前確認は稼働中のワーカーを要求するので、同時には有効にできない。
4. `replicasSynchronous=false`で`replication`。署名ファイルを生成して最終構成で`application`し、Pulumiの`astra`スタックを更新する。
5. 初期画像を`migrate-image-archive.py`で区画ごとにパックへ移す。1,826,950枚・14,054,519,155バイトが25ファイルになり、SQL内の初期画像本体はゼロになった（[coverage](results/archive-complete-team12/coverage.json)）。所要は約41分。`image_objects_202608`ではビルド完了後にEC2 Instance Connectの接続が1時間の上限で切れたため、3地域のファイルを確かめてから`--resume`で公開した。

署名ファイル`var/astra/signatures.bin`はTeam 12のdb1から作り直した。有効期限は2026-10-30 09:19:56 UTCで、それを過ぎると通常の署名計算に戻る。作り直すときは`cache-signatures`を実行して`apply.sh application`で配る。生成器は全コアで計算するようにした。

ベンチは初回（冷えた状態、パック移行前）が[163,617点・PASS](results/20261029T093255Z-team12/result.json)、パック移行後が[181,755点・PASS](results/20261029T102234Z-team12/result.json)と[159,007点・PASS](results/20261029T102856Z-team12/result.json)。間の[167,869点・PASS](results/20261029T102534Z-team12/result.json)はメモリ9,790MiBの遅いベンチホストに当たったので比較から外した。

## デプロイ

アプリとDBの実行設定は`deploy/runtime.json`に記録し、Pulumiプロジェクト`provisioning/aws/astra`の`dev`スタックから参照する。
ISUCONルールに合わせて、新しいAWSリソースは作成しない。既存のTeam 12 VMへSSH/scpで反映する（2026-10-29にTeam 10から移設）。
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

## ファイルによる画像保存

画像本体をSQLに保存する必要はない。`fileImageStore.enabled`で全アプリにファイル読み取りを配備し、その確認後に`writesEnabled`を有効にする。新規画像は受信地域の`/var/lib/astra-images/<先頭2文字>/<UUID>`に保存する。create_newで最終名を排他的に作成し、内容とディレクトリをfsyncしてから、SQLに`fs:<region>:<UUID>`を記録する。途中失敗は参照を公開せず、孤立ファイルは後で回収する。

SQLには所有者と公開状態、保存先を残す。画像APIは毎回、現在の公開状態・鍵・署名期限を検証する。他地域の画像本体は既存VMのプライベートIP・8082番ポートのNginxから取得し、外部向けルートでは直接配信しない。従来のSQL画像も引き続き読める。書き込みだけをSQLへ戻すことはできるが、ファイル参照が残る間は読み取り機能を外せない。

`verify-file-peers.py`は3地域間の画像一致とアクセス制限を確認する。`apply.sh image-gc`は全アプリ停止中の参照一覧を検証し、SQLとファイル両方の孤立画像を整理する。参照先の欠落や不明なファイルがあれば処理を中止する。

## 初期画像のアーカイブ

初期画像は`pack:<archive UUID>:<offset>:<length>`で参照し、各VMの`/var/lib/astra-images/packs/`から必要な範囲を読む。APIとZIPは現在のSQLメタデータで閲覧権限を確認する。[全件の件数・容量](results/archive-complete/coverage.json)と区画ごとの移行記録を`results/archive-*/`に保存した。各ベンチの`experiment.json`には、実際に参照中のアーカイブIDとハッシュも記録する。

`migrate-image-archive.py`は全アプリを止め、3地域のファイルと参照一覧のハッシュを照合してからSQLを切り替える。元の行バージョンが変わっていれば切り替えは失敗する。移行後はレプリカの反映と古い行を回収できる状態を待ってSQL領域を縮小し、アプリを再起動して公開画像を照合する。

```sh
python3 webapp-optmized-astra/deploy/migrate-image-archive.py image_objects_202609
# ファイル作成後、SQL公開前に接続が切れた場合
python3 webapp-optmized-astra/deploy/migrate-image-archive.py image_objects_202609 --resume ARCHIVE_UUID_HEX
# SQL公開済みで領域整理だけを再開する場合
python3 webapp-optmized-astra/deploy/migrate-image-archive.py image_objects_202609 --reclaim ARCHIVE_UUID_HEX
# SQL本体へ復帰する場合。公開中のアーカイブを手動削除しない
python3 webapp-optmized-astra/deploy/migrate-image-archive.py image_objects_202609 --restore ARCHIVE_UUID_HEX
```

小さい区画でSQLへの復帰と全バイト一致を実機確認した。移行時にSSH接続が切れた区画も、作成済みファイルを再検証して復帰できた。GCは公開参照を残し、未参照のアーカイブだけをファイル単位で削除する。

## 地域別の独立画像DB

以下はファイル保存導入前の構成と、SQL画像への復帰に備えた互換設定。現在の新規画像は前節のファイルへ保存する。

`regionalImageStoresEnabled`で欧州・米州の既存アプリVMにもポート5433の独立PostgreSQLを用意する。既存5432のメタデータレプリカは維持する。`apply.sh image-store`でクラスタを作成し、`apply.sh database`で画像参照列を64文字へ広げ、書き込み無効のまま`apply.sh application`で全プロセスへ読み取り対応を配置する。

採用アプリを配置する前に`apply.sh database`で013の`astra_create_post_asset`、014の正規化検索テーブル、015の検索候補索引とdirty集合を追加してからアプリを配備する。投稿と画像の保存先参照を1回のDB呼び出しで確定し、失敗時は同時にロールバックする。従来の投稿関数も残してある。

その後`regionalImageWritesEnabled=true`を反映すると、欧州・米州の投稿画像は受信地域でJPEG検証と永続化を行い、Asiaの更新ワーカーへ署名付き参照だけを転送する。Asiaは通常どおり認証・地域・公開条件を確認し、メタデータのコミットと両レプリカへの反映完了後に応答する。プロフィールとリアルモジは従来の保存方式を使う。

地域間の認証鍵はgit管理外の`var/astra/.regional-upload.env`に生成し、既存3台へ`/home/isucon/.astra-upload.env`としてscpで配る。いずれも権限600。Pulumiにはファイルパスと機能フラグだけを記録し、AWSリソースは追加しない。

参照は`europe:<UUID>`または`america:<UUID>`。従来のUUID参照はAsiaへ接続する。書き込みを無効化しても地域別の読み取り対応と保存DBは残す。公開中の地域別参照がある状態で読み取りを外すデプロイは拒否する。旧バイナリへの復帰は、対応バイナリで初期化して地域別参照がなくなったことを確認した後に行う。`apply.sh image-gc`は全アプリを停止して公開参照を地域別に検証し、各独立DBの孤立画像だけを削除する。未知の地域・不正な参照があれば削除を中止する。

地域別の永続化・取得・ZIP・削除確認は、独立した3個目のローカルPostgreSQLを`ISUREAL_REGIONAL_ASSET_TEST_DSN`で指定して実行する。署名は本文、認証ヘッダー、Content-Type、クライアントIP、URL、画像参照を束ね、実際の接続元IPと地域が一致した場合だけ受け入れる。

## 検証

現行版は[Rust 43件成功・失敗0件、Clippy成功](results/20261028T082244Z-team10/validation.txt)。[再起動確認の記録](results/20261028T014704Z-team10/storage-restart-verification.txt)には18件の画像一致、実行中バイナリと設定、レプリカの追従、ディスク容量を残した。

リポジトリのルートから`python3 webapp-optmized-astra/deploy/verify-runtime.py`で稼働設定を照合できる。ベンチ停止中に`python3 webapp-optmized-astra/deploy/verify-regional-restart.py`を実行すると、18件の画像を取得してから3地域のアプリと画像DBを再起動し、内容の一致と稼働設定を確認する。署名付きURLはVM上のroot専用ファイルに一時保存し、確認後に削除する。


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
更新の実行枠は、リクエスト本文256KiBあたり1枠・最低1枠で、プロセスごとに64枠とする。
16MiB以上または長さが不明な本文は全枠を使い、SQLコミット後も両レプリカへの反映確認まで保持する。
64KiB以下の画像以外の更新には32枠、管理APIには4枠を別に用意し、小さな操作が画像アップロードの後ろで待ち続けるのを避ける。各枠は両レプリカへの反映確認まで保持する。GET/HEADはこの枠を使わない。
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
- JPEGはSOI/SOFを含むヘッダと寸法を確認する。マニュアルで認められた範囲に検証を絞り、圧縮データ全体のデコードと検証結果のキャッシュを省く。保存・取得する画像のバイト列は変えない。
- 画像のDB保存は`STORAGE EXTERNAL`を使い、TOASTの圧縮試行を省く。計測で`pglz_compress`がプライマリCPUの26.55%を占め、抽出した画像300件はいずれも圧縮されていなかった。画像は引き続きPostgreSQLへ保存し、取得するバイト列も維持する。この設定は今後の書き込みに適用され、既存行は書き直さない。[PostgreSQL 16の保存方式](https://www.postgresql.org/docs/16/sql-altertable.html#SQL-ALTERTABLE-DESC-SET-STORAGE)に従い、既存の圧縮済み行と新規行の読み取りをローカルDBで確認した。
- 画像配信は現在の閲覧権限と署名鍵を毎回確認する。画像データは最大512MiB・16,384件まで保持し、DB行の`xmin`と作成時刻が変われば取得し直す。キャッシュがある場合は既知の作成時刻で検索先の月別テーブルを絞り、見つからなければ全体を再検索する。移動・削除・同じIDでの再作成や所有者の鍵変更も検証する。
- SSE通知は地域内DBごとに100ミリ秒間隔で最大512接続ずつ一括確認する。接続ごとの通知時刻とユーザー状態は毎回確認する。
- 通知の検索は開いている投稿期間を列挙し、期間・ユーザーの一意索引から該当通知を探す。各期間の最初の通知から全体の最初の1件を選ぶため、履歴を毎回走査せずに従来の順序を保つ。ユーザー削除時の外部キー確認用には、ユーザーIDだけの小さな索引を残す。
- 通知のユーザー・投稿期間ペアの一意制約は、索引の先頭を投稿期間に変更する。同じ期間の通知をまとめて書き込み、一意性と全通知の保存を維持しながら履歴索引へのランダムアクセスを減らす。
- 通知IDは標準のUUIDv7で生成し、対象ユーザー順に一括登録する。対象者の判定、通知時刻、ロック、トランザクションは維持し、主キーの書き込みも時系列でまとまるようにする。
- フィードは閲覧条件・友人関係・ブロック・投稿済み条件を1回の問い合わせで評価し、現在の投稿期間ごとに最大51件へ絞り、全地域の上位51件だけを取得する。従来のRustの判定との比較テストでページ送りと状態変更を検証する。
- 投稿詳細とコメント・リアクションの権限判定は、対象投稿に必要な友人・2段階の関係・ブロック・投稿済み条件を1回で確認する。友人グラフ全体をアプリへ転送せず、従来のRust判定との比較で地域変更、投稿期間終了、退会、削除、本人の投稿を含む条件を検証する。
- 新規登録はユーザー名の一意制約と単一INSERTで処理する。同名の並行登録は1件だけ成功し、別名の登録は互いをブロックしない。
- ユーザー検索はASCII候補をDB側で絞り込み、Unicodeを含む候補はRustで照合する。ASCII文字列を簡単な小文字変換で処理し、Unicode正規化の結果を元の文字列ごとに最大16,384件保持する。名前・ブロック・退会状態はDBから毎回読み、検索条件や権限の結果はキャッシュしない。NFC・case folding・NFCの順序と部分一致の意味は維持する。
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
| 20261028T004433Z-team10 | regional durable post images with signed reference handoff | PASS | 112,367 |
| 20261028T004825Z-team10 | regional images; reuse recently active DB connections | PASS | 119,333 |
| 20261028T005258Z-team10 | regional images; local preflight with authoritative writer reauthentication | PASS | 121,353 |
| 20261028T005720Z-team10 | documented JPEG header validation without pixel decoding | PASS | 120,988 |
| 20261028T010144Z-team10 | Asia upstream HTTP keepalive with regional images and header validation | PASS | 129,521 |
| 20261028T010533Z-team10 | info action logs; preserve login guard and server errors | PASS | 118,394 |
| 20261028T011022Z-team10 | atomic post and image-reference insertion; debug logs restored | PASS | 127,015 |
| 20261028T011428Z-team10 | stream uploads through nginx without request temp files | PASS | 117,664 |
| 20261028T011841Z-team10 | 262144 signatures with inline cache keys; request buffering restored | PASS | 126,761 |
| 20261028T012306Z-team10 | ASCII search prefilter with full Unicode and literal semantics | PASS | 133,279 |
| 20261028T012958Z-team10 | repeat full candidate after application and independent image DB restarts | PASS | 128,427 |
| 20261028T013453Z-team10 | repeat 129521-point application paths; same regional storage and HTTP settings | PASS | 128,375 |
| 20261028T013924Z-team10 | final selected regional application; atomic publication, signature cache and ASCII search | PASS | 122,278 |
| 20261028T014307Z-team10 | selected regional application with alternate seed 20261028 | FAIL | 0 |
| 20261028T014704Z-team10 | repeat alternate seed after registration timeout investigation | PASS | 120,515 |
| 20261028T044719Z-team10 | 133279-point application control after regional image compaction | PASS | 117,126 |
| 20261028T045250Z-team10 | normalized Unicode search text with atomic updates and stale-name fallback | PASS | 129,685 |
| 20261028T045638Z-team10 | repeat normalized Unicode search candidate | PASS | 124,030 |
| 20261028T050352Z-team10 | reuse metadata-validated image bytes in ZIP exports | PASS | 128,791 |
| 20261028T050801Z-team10 | eight replica visibility batches with unchanged small-write admission | PASS | 133,754 |
| 20261028T051306Z-team10 | 128 bounded small writes with eight replica visibility batches | PASS | 147,184 |
| 20261028T051739Z-team10 | remove duplicate login lock and session read queries | PASS | 137,453 |
| 20261028T052149Z-team10 | repeat reduced login queries before log serialization comparison | PASS | 120,577 |
| 20261028T052757Z-team10 | reduce action-log allocations while preserving debug output and login guard fields | PASS | 147,864 |
| 20261028T053233Z-team10 | dedicated replica visibility connection pool | PASS | 127,110 |
| 20261028T054615Z-team10 | parallel regional posting-window creation with stable recipient snapshots | PASS | 147,352 |
| 20261028T055622Z-team10 | buffer Nginx access log writes while preserving application debug logs | PASS | 147,769 |
| 20261028T061211Z-team10 | filesystem reader control with PostgreSQL image writes | PASS | 144,909 |
| 20261028T061638Z-team10 | durable regional filesystem images instead of PostgreSQL blob writes | PASS | 80,972 |
| 20261028T062146Z-team10 | filesystem image writes with corrected regional reference validation | PASS | 150,931 |
| 20261028T062953Z-team10 | repeat filesystem images after GC and full process restart | PASS | 163,405 |
| 20261028T063417Z-team10 | filesystem images with alternate seed 20261028 | PASS | 164,533 |
| 20261028T065922Z-team10 | small initial-image archive with original 128-connection listen backlog | PASS | 157,618 |
| 20261028T070427Z-team10 | 4096 application listen backlog; same small historical archive | PASS | 157,098 |
| 20261028T081316Z-team10 | all initial images in locally replicated immutable files; metadata and authorization remain SQL | PASS | 149,107 |
| 20261028T081730Z-team10 | repeat all initial image archives before additional application changes | PASS | 162,724 |
| 20261028T082244Z-team10 | coalesce concurrent image reads and use consistent reaction image timestamp hints | PASS | 163,428 |
| 20261028T085118Z-team10 | info action logs and Nginx access logging off; login guard preserved | PASS | 161,266 |
| 20261028T085431Z-team10 | repeat reduced logging; info action logs and Nginx access off | FAIL | 0 |
| 20261028T085903Z-team10 | recheck reduced logging after preparation registration timeout; configuration unchanged | PASS | 166,822 |
| 20261028T090736Z-team10 | indexed normalized search candidates; transactional dirty fallback and bounded pages | PASS | 182,172 |
| 20261028T091113Z-team10 | repeat indexed normalized search candidates with reduced logging | PASS | 172,644 |
| 20261028T091523Z-team10 | indexed search and reduced logging; alternate seed 20261028 | PASS | 169,544 |
| 20261028T092907Z-team10 | Asia async replica staged; Asia reads remain on primary; topology control | PASS | 152,282 |
| 20261028T093243Z-team10 | repeat staged Asia standby control; primary Asia reads unchanged | PASS | 161,378 |
| 20261028T093848Z-team10 | Asia local metadata reads with fresh per-request WAL barriers | PASS | 141,182 |
| 20261028T100354Z-team10 | selective Asia replica reads for feed and search; image metadata and ZIP stay on primary | PASS | 176,813 |
| 20261028T100711Z-team10 | repeat selective Asia feed and search replica reads | FAIL | 0 |
| 20261028T101058Z-team10 | selective read recheck after isolated initial login timeout; unchanged runtime | PASS | 174,050 |
| 20261028T101558Z-team10 | adaptive friends-of-friends query with selective Asia reads | PASS | 169,293 |
| 20261028T101921Z-team10 | repeat adaptive friends-of-friends query with selective Asia reads | PASS | 179,625 |
| 20261028T102321Z-team10 | signature cache counters; duplicate coalescing disabled control | PASS | 158,281 |
| 20261028T103148Z-team10 | reuse issued image URLs; preserve original 24-hour expiry and fresh permissions | PASS | 162,003 |
| 20261028T103720Z-team10 | repeat issued URL cache after image GC and all application restart | PASS | 162,842 |
| 20261028T104152Z-team10 | issued image URL cache capacity 262144 entries | PASS | 152,824 |
| 20261028T104946Z-team10 | gzip JSON responses at level one; image and SSE formats unchanged | PASS | 160,343 |
| 20261028T110140Z-team10 | regional notification eligibility locks; unrelated user writes remain concurrent | PASS | 161,596 |
| 20261028T110815Z-team10 | repeat regional notification eligibility locks and issued URL caching | PASS | 161,074 |
| 20261028T113246Z-team10 | precomputed image signatures from actual current DB owners | PASS | 163,334 |
| 20261028T113727Z-team10 | repeat precomputed signatures after image GC and restart verification | PASS | 176,753 |
| 20261028T114208Z-team10 | 512 MiB image byte cache with precomputed signatures | PASS | 177,049 |
| 20261028T114538Z-team10 | repeat 512 MiB image byte cache with precomputed signatures | FAIL | 0 |
| 20261028T115320Z-team10 | recheck 512 MiB cache after preparation registration timeout audit | PASS | 182,703 |
| 20261028T115839Z-team10 | primary Asia reads with optional third metadata standby stopped | PASS | 164,094 |
| 20261028T120334Z-team10 | repeat primary Asia reads with the third metadata standby stopped | PASS | 149,467 |
| 20261028T121106Z-team10 | restore selective Asia reads and verify restart after topology comparison | PASS | 173,705 |
| 20261028T121807Z-team10 | vacuum deleted metadata and refresh statistics during initialization | PASS | 170,756 |
| 20261028T122308Z-team10 | repeat initialization vacuum and planner statistics refresh | PASS | 176,341 |
| 20261028T122920Z-team10 | sixteen ordinary metadata connections with separate visibility barriers | PASS | 173,249 |
| 20261028T140619Z-team10 | repeat sixteen ordinary metadata connections | PASS | 183,359 |
| 20261028T141246Z-team10 | 128 upload admission slots with durable regional files | PASS | 168,825 |
| 20261028T141650Z-team10 | repeat 128 upload admission slots | PASS | 172,721 |
| 20261028T142236Z-team10 | restore 64 upload slots after paired wider admission comparison | PASS | 181,962 |
| 20261028T143942Z-team10 | notification snapshot readers with legacy writes control | PASS | 185,418 |
| 20261028T144408Z-team10 | immutable notification batches with cached recipients and current-state checks | PASS | 169,421 |
| 20261028T144755Z-team10 | repeat immutable notification batches | FAIL | 0 |
| 20261028T145429Z-team10 | notification batches recheck after preparation registration timeout and cleanup | PASS | 174,707 |
| 20261028T145843Z-team10 | repeat notification batches without restarting application caches | PASS | 187,187 |
| 20261028T150625Z-team10 | stream uploads without nginx request temp files on regional file storage | PASS | 156,485 |
| 20261028T151345Z-team10 | repeat unbuffered uploads with regional file storage | FAIL | 0 |
| 20261028T151839Z-team10 | restore buffered uploads after unbuffered regression | PASS | 176,344 |
| 20261028T152510Z-team10 | ThinLTO release with one codegen unit | PASS | 178,621 |
| 20261028T152857Z-team10 | repeat ThinLTO release with warm application caches | PASS | 180,392 |
| 20261028T153837Z-team10 | remove redundant notification sorting and force bounded user lookups | PASS | 172,478 |
| 20261028T154235Z-team10 | repeat notification query optimization without process restart | PASS | 183,498 |
| 20261028T154938Z-team10 | error-level action logging with failed-login guard events preserved | PASS | 190,094 |
| 20261028T155335Z-team10 | repeat minimal action logging with guard events intact | PASS | 177,132 |
| 20261028T155749Z-team10 | verify logging and notification improvements with alternate seed 20261028 | PASS | 185,577 |
| 20261028T160310Z-team10 | 1 GiB primary metadata shared buffers after application optimizations | PASS | 188,372 |
| 20261028T160653Z-team10 | repeat 1 GiB primary shared buffers without database restart | PASS | 176,506 |
| 20261028T161234Z-team10 | restore 512 MiB metadata cache after 1 GiB comparison and verified GC | PASS | 179,455 |
| 20261028T164045Z-team10 | db1 archive copies prepared with local Asia reads control | PASS | 188,226 |
| 20261028T164517Z-team10 | Asia initial archive reads from existing db1 peer | PASS | 174,918 |
| 20261028T164832Z-team10 | repeat Asia archive reads from db1 peer without restart | FAIL | 0 |
| 20261028T165446Z-team10 | db1 archive peer with threaded Nginx file reads after GC | PASS | 179,400 |
| 20261028T165836Z-team10 | repeat threaded db1 archive peer without restart | PASS | 187,288 |
| 20261028T170519Z-team10 | restore local archive reads after db1 peer comparison | PASS | 183,790 |
| 20261028T171110Z-team10 | 256 KiB Nginx upload buffer with request buffering enabled | PASS | 188,737 |
| 20261028T171505Z-team10 | repeat 256 KiB upload buffer without restart | PASS | 169,140 |
| 20261028T172332Z-team10 | 1 MiB Nginx upload buffer after verified GC and restart | PASS | 195,259 |
| 20261028T172715Z-team10 | repeat 1 MiB upload buffer without restart | PASS | 162,498 |
| 20261028T173911Z-team10 | 1 MiB upload buffer with fixed Nginx malloc thresholds | PASS | 174,417 |
| 20261028T174335Z-team10 | repeat fixed Nginx malloc thresholds without restart | PASS | 174,674 |
| 20261028T175325Z-team10 | direct immutable image files before SQL publication | PASS | 178,502 |
| 20261028T180156Z-team10 | repeat direct immutable image files without restart | PASS | 181,905 |
| 20261028T180707Z-team10 | application fixed malloc thresholds at 128 KiB | PASS | 192,384 |
| 20261028T181011Z-team10 | repeat application fixed malloc thresholds without restart | PASS | 164,303 |
| 20261028T182059Z-team10 | diagnose slow auth admission handler and replay phases | PASS | 188,002 |
| 20261028T182924Z-team10 | separate authentication admission from general small writes | PASS | 166,616 |
| 20261028T183654Z-team10 | split small-write admission into 64 auth and 64 general slots | PASS | 177,380 |
| 20261028T184239Z-team10 | restore shared small-write admission and disable auth timing | PASS | 179,960 |
| 20261028T185322Z-team10 | diagnose post admission storage SQL and replay phases | PASS | 180,913 |
| 20261028T190234Z-team10 | release image body slots before bounded replay response wait | PASS | 177,680 |
| 20261028T190842Z-team10 | image body and response admission separated with diagnostics disabled | PASS | 171,801 |
| 20261028T191154Z-team10 | repeat image body and response admission separation without restart | FAIL | 0 |
| 20261028T191756Z-team10 | bound overlapping image processing and replay responses to 64 requests | PASS | 185,395 |
| 20261028T192319Z-team10 | repeat 64 bounded image requests without restart | PASS | 184,915 |
| 20261028T194609Z-team10 | use three synchronous replicas and all local Asia reads | PASS | 151,126 |
| 20261028T194923Z-team10 | repeat three synchronous replicas and local Asia reads without restart | FAIL | 0 |
| 20261028T195416Z-team10 | offload Asia archive reads to db1 with three synchronous replicas | PASS | 158,226 |
| 20261028T200121Z-team10 | use three synchronous replicas with selective Asia reads and db1 archives | PASS | 182,703 |
| 20261028T200747Z-team10 | use selective three-replica reads with local archives after GC and restart | PASS | 176,347 |
| 20261028T201116Z-team10 | repeat selective three-replica reads with local archives without restart | PASS | 175,796 |
| 20261028T201755Z-team10 | restore two-replica acknowledgement after three-replica comparison | PASS | 181,005 |
| 20261028T203054Z-team10 | read all Asia metadata from the primary after application improvements | PASS | 176,830 |
| 20261028T203453Z-team10 | repeat all Asia primary reads without restart | PASS | 174,401 |
| 20261029T093255Z-team12 | team 12 cold warm-up before initial image packs; FoF ranking port | PASS | 163,617 |
| 20261029T102234Z-team12 | team 12 final runtime with all initial images in 25 packs | PASS | 181,755 |
| 20261029T102534Z-team12 | team 12 repeat final runtime without restart | PASS | 167,869 |
| 20261029T102856Z-team12 | team 12 repeat final runtime; previous run hit a 9,790 MiB bench host | PASS | 159,007 |
| 20261029T112444Z-team12 | Team 12 baseline before grouped export image reads; fixed seed 1 | PASS | 164,297 |
