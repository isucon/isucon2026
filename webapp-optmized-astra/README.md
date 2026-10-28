# webapp-optmized-astra

Team 10向けのRust最適化実装。`webapp/rust`と`webapp/sql`を2335d5fから複製した。
作業期限は2026年10月28日23:00 JST。各ベンチ結果をこのディレクトリに記録し、コミットしてpushする。

<!-- astra-final-summary-start -->
## 現在の構成と検証状況

2026年10月28日23:00 JSTまで比較を継続中。全画像をファイル保存に移し、ログ出力と検索を改善した版の最高点は同一seedで[182,172点・PASS](results/20261028T090736Z-team10/result.json)、再実行は[172,644点・PASS](results/20261028T091113Z-team10/result.json)、検索改善版の別seedは[169,544点・PASS](results/20261028T091523Z-team10/result.json)。直前のSQL保存は[144,909点・PASS](results/20261028T061211Z-team10/result.json)。同じseedでもスコアの振れがあるため、単発の差だけで各変更の効果とは判断しない。全走行のソース、配布物SHA-256、ベンチイメージを各`experiment.json`に保存している。

AsiaアプリVMへの追加レプリカも比較中。配置のみは152,282点・161,378点、全読み取り分散は141,182点でPASSしたが、AsiaのI/O待ちが増えた。現在は検索・フィードだけをローカルレプリカへ送り、画像メタデータ・ZIPなどはプライマリを使う。選択的な分散は176,813点・174,050点、フィードの実行計画改善後は169,293点・179,625点でPASSした。

メタデータはAsiaのプライマリから欧州・米州へ複製し、新規の投稿画像は受信地域のファイルシステムへ永続化する。既存VMだけを利用し、AWSリソースは追加しない。秘密情報は非公開envをscpで共有する。SQLには画像の所有者・公開状態・保存先を残し、初期画像1,826,950枚・14,054,519,155バイトも25個の不変ファイルにまとめ、3地域へ配置済み。初期画像のSQL内の本体はゼロになった。従来のSQL画像を読む互換処理も残す。

Unicode検索は正規化した名前のGIN索引から候補を取得し、名前変更や派生行の欠落は同一トランザクションのdirty集合で補う。Unicodeと現在のアカウント・ブロック状態の判定を維持する。ZIP出力での検証済み画像キャッシュ再利用、登録・ログインの待ち行列、アクションログの割り当てを改善して比較している。現在は更新反映待ち8バッチ、小さな更新128件、反映待ち専用の接続プール。地域別の投稿期間開始では、地域単位の排他とユーザーのSHAREロックを併用し、通知対象を固定したまま異なる地域を並行処理する。

[Rustの全体検証は43件成功・失敗0件、Clippy成功](results/20261028T082244Z-team10/validation.txt)。同じ画像への20件の同時アクセスを1回のファイル読み込みに集約し、最初の読み込みが失敗した場合の再試行も検証した。[ファイルGCと全アプリ・Nginx・画像DBの再起動後、18画像の完全一致を確認](results/20261028T062146Z-team10/filesystem-validation.txt)。画像キャッシュは1GiB・16,384件、署名キャッシュは262,144件、Tokioは2スレッド。発行済みURLも現在の鍵と画像IDをキーに最大65,536件保持する。初回発行から24時間で失効し、残り60秒で更新する。URL再利用版は162,003点、直前の計測用対照は158,281点でPASSした。欧州・米州のURLキャッシュのヒット率は約82〜88%、Asiaは約49%で、継続比較中。AWS認証が復旧し、アプリログ`info`・Nginxアクセスログ無効を配備済み。ログイン失敗のアプリログと監視、リクエストバッファリングは維持する。ログ削減版は161,266点と166,822点でPASSしたが、間の再実行は負荷開始前の登録タイムアウトでFAILした。失敗時にCPU・I/Oの飽和はなく、終了後のレプリカは追従済みだった。原因は未確定であり、FAILも記録している。公開状態・現在の鍵・署名期限の検証と、更新成功応答前の両レプリカへの反映確認を維持する。

地域別画像DBのGCでは公開画像4,675件を保持して孤立画像93,622件を削除し、領域の詰め直し前後の18画像が一致した。Asiaの空き容量は3.7GiBから28.1GiBへ回復した。継続計測で再び増えるため、容量を確認して整理する。別seedの過去の初回実行には登録タイムアウトによるFAILが1件あり、同条件再実行は120,515点・PASS。原因は未確定。

構成比較とAsia db1の役割については[DB構成とVMの役割の比較](architecture-evaluation.md)を参照。
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

## ファイルによる画像保存

画像本体をSQLに保存する必要はない。`fileImageStore.enabled`で全アプリにファイル読み取りを配備し、その確認後に`writesEnabled`を有効にする。新規画像は受信地域の`/var/lib/astra-images/<先頭2文字>/<UUID>`に保存する。一時ファイルの内容をfsyncし、上書きしないhard linkで公開してディレクトリもfsyncしてから、SQLに`fs:<region>:<UUID>`を記録する。途中失敗は参照を公開せず、孤立ファイルは後で回収する。

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
- 画像配信は現在の閲覧権限と署名鍵を毎回確認する。画像データは最大1GiB・16,384件まで保持し、DB行の`xmin`と作成時刻が変われば取得し直す。キャッシュがある場合は既知の作成時刻で検索先の月別テーブルを絞り、見つからなければ全体を再検索する。移動・削除・同じIDでの再作成や所有者の鍵変更も検証する。
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
