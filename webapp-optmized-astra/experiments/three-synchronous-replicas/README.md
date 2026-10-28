# Asiaを含む3台同期の比較

3台同期は今回は採用せず、EU・USの2台同期と、Asiaの検索・フィードだけを非同期レプリカで読む構成へ戻した。切り戻し後は181,005点でPASS。3台同期では一部の読み取りが速くなったが、直前の2台同期185,395点・184,915点を上回る総合スコアを確認できなかった。

## 同じseedでの比較

| 同期対象 | Asiaのレプリカ読み取り | 初期画像の読み取り | スコア |
| --- | --- | --- | --- |
| EU・US、比較元 | フィード・検索、読み取り前確認あり | 地域内 | [185,395 PASS](../../results/20261028T191756Z-team10/result.json)、[184,915 PASS](../../results/20261028T192319Z-team10/result.json) |
| EU・US・Asia | 全読み取り、読み取り前確認なし | 地域内 | [151,126 PASS](../../results/20261028T194609Z-team10/result.json)、[0 FAIL](../../results/20261028T194923Z-team10/result.json) |
| EU・US・Asia | 全読み取り、読み取り前確認なし | db1、aio threads | [158,226 PASS](../../results/20261028T195416Z-team10/result.json) |
| EU・US・Asia | フィード・検索、読み取り前確認なし | db1、aio threads | [182,703 PASS](../../results/20261028T200121Z-team10/result.json) |
| EU・US・Asia | フィード・検索、読み取り前確認なし | 地域内 | [176,347 PASS](../../results/20261028T200747Z-team10/result.json)、[175,796 PASS](../../results/20261028T201116Z-team10/result.json) |
| EU・US、切り戻し後 | フィード・検索、読み取り前確認あり | 地域内 | [181,005 PASS](../../results/20261028T201755Z-team10/result.json) |

seedは12453240012606038961。各走行のソース、配布物SHA-256、ベンチイメージは結果ディレクトリに保存した。0点の走行は負荷前の登録タイムアウトで、性能の反復測定は成立していない。同種の準備失敗は2台同期でも発生しており、今回の構成だけが原因とは断定しない。この時間帯のアプリERRORログには該当する記録がなかった。

## 観測

全読み取りをAsiaへ移すと、Asiaのfriendsフィード中央値は159msから49ms、投稿詳細は194msから32msへ短縮した。一方で登録は565msから2,165msへ悪化した。負荷開始後60秒の平均I/O待ちはAsiaで10.81%から25.01%へ増え、主DBの平均CPU使用率は38.88%から20.65%へ下がった。Asia側の読み取りI/Oと同期反映の競合を疑うが、原因を確定する計測ではない。

db1の既存画像パックを読むと、全読み取り構成のAsia I/O待ちは20.77%へ下がった。読み取り範囲をフィード・検索へ絞ると8.06%になり、158,226点から182,703点へ改善した。ただし直前の採用構成より高いスコアは確認できなかった。画像を地域内へ戻した3台同期も176,347点・175,796点で、採用の根拠にはならなかった。単発の差だけで方式一般の優劣は断定しない。

[比較データ](comparison.json)には各走行の設定、AsiaのAPI応答時間、負荷開始後60秒のホスト指標を記録した。CPU使用率はidleとI/O待ちを除く値。

## 実装と配備

`asiaReplicaSynchronous`の既定値はfalseで、現在もfalse。trueでは既存Asiaレプリカとnative write fenceを必須にし、主DBを`FIRST 3 (astra_europe, astra_america, astra_asia)`へ切り替える。PostgreSQLの[同期対象設定](https://www.postgresql.org/docs/16/runtime-config-replication.html#GUC-SYNCHRONOUS-STANDBY-NAMES)と[remote_apply](https://www.postgresql.org/docs/16/runtime-config-wal.html#GUC-SYNCHRONOUS-COMMIT)を使い、更新応答までに3台へ反映する。Asiaの読み取り前確認は省ける。読み取り範囲はallとfeed_searchの両方を比較可能にした。

起動時は実レプリカのスロットと主DBの同期対象設定を照合し、2台同期のままAsiaを確認なしで読む構成を拒否する。`ISUREAL_WRITE_FENCE_HOSTS`は従来のpoll方式のEU・US接続先として維持し、native方式の反映対象は主DBの設定から決める。実機照合では稼働プロセスのwrite fence設定も検証する。

切替は互換版アプリ、主DBの3台同期化、Asia読み取りの順。切り戻しはAsiaの読み取り前確認を復元してアプリを配備した後、主DBを2台同期へ戻す。配備処理は逆順の切り戻しを拒否する。主DB設定ファイルは必要なレプリカのストリーミング確認後に置換する。変更はPulumi runtimeと配備コードで管理し、新しいAWSリソースは作っていない。db1の初期画像コピーは保持し、配信と読み取りは再び無効にした。

## 検証

既存回帰と3台目停止のRust releaseテストは53件PASS、性能測定用1件ignore。追加の実APIテスト1件もPASS。3台目だけ反映を止めると更新応答が待機し、再開後は3台とも更新を読めることを確認した。実APIでは2台同期での起動拒否、3台同期での登録・ログイン・読み取り、ログアウト応答後の401を検証した。

選択的読み取りを追加した後も、3台同期で検索が新しい主DBのWALを待たないことと、非同期構成で検索待機・ログアウト反映を維持する実APIテストを各1件実行した。Python経路設定5件、Clippy全target、TypeScript型検査、変更したシェルとPythonの構文確認も成功した。

3台同期・選択的読み取り・地域内画像の構成でGCと再起動を行い、3地域の計18画像の本文とJPEG型が一致した。Asiaの空き容量は8.7GiBへ戻り、全レプリカの未反映WALは0だった。[検証ログ](gc-restart-validation.txt)を保存した。切り戻し後もFIRST 2、Asiaの読み取り前確認、実プロセスの配布物・設定を照合した。

ローカルfixtureはPostgreSQL 16の既存主DBと3レプリカ。application_nameをastra_europe、astra_america、astra_asiaとし、3台目はポート26215、物理スロットastra_asiaで作成した。追加の環境変数は`ISUREAL_FENCE_REPLICA3_DSN=postgres://postgres@127.0.0.1:26215/postgres`と`ISUREAL_THIRD_REPLICA_HOST`。既存のFENCE/SELECTIVE用変数も設定する。

## 次の比較

アプリ側の改善後の負荷配分を踏まえ、Asiaの検索・フィードを主DBへ戻す構成も再評価する余地がある。まず読み取り先だけを変え、既存のレプリカは保持する。登録準備時のタイムアウトは未解消で、アプリの処理時間とHTTP受付時間の照合も必要。
