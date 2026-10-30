# Go最適化版とTeam 12 Rust版の差

同じ固定ベンチイメージ（sha256:55649c0a…）・seed 1・large・merged.zipの既存走行を比較する。Go側は既存ログのみを使用し、他チームへの配備や新しいベンチは行っていない。現行Rust版はf9aeafc9、通常走行179,991点PASS。Team 12だけで同じ実装のSQLプロファイル付き走行を追加し、160,707点PASSだった。比較対象のmeta.jsonのcommitはベンチ実行側のチェックアウトであり、配備済みGoバイナリのコミットを保証しない。

| 実装・環境 | スコア（すべてPASS） | Asia app / db1 | router |
|---|---:|---|---|
| Go・Team 11 | 323,803 / 325,916 | c7i.large / c6in.large | c6in.large |
| Go・Team 9 | 304,765 / 320,614 | c5.large / c5.large | c6i.large |
| Rust・Team 12 | 179,991 | c5.large / c5.large | c6in.large |

全例でEU・US appはc7i.large、ベンチは4 vCPU。Team 9はapp/dbの型とEBSのIOPS・帯域設定がTeam 12と同じで、ルーターの型は異なる。VM実体・走行時刻・キャッシュ状態・データの同一性までは揃っていないため、差分をそのまま変更による改善見込みとは扱わない。

Team 9の320,614点との差140,623点のうち、月別履歴42,054点・日別履歴8,840点・export 33,720点で84,614点（60.2%）を占める。比較したGoの4走すべてで、履歴とexportの寄与は点差の60.2〜63.4%。署名検証やZIP出力だけでは、この差は埋まらない。

Asiaの中央値はGo Team 9 / Rust Team 12で、Memories 4.0 / 187.9ms、日別履歴2.7 / 156.4ms、画像7.4 / 90.0ms、自分の情報17.6 / 240.4ms、友達の友達フィード19.8 / 215.0ms。投稿は逆に716.7 / 457.3msでRustの中央値が低い。全APIが一律に遅いのではなく、読み取り、とくにAsiaの初期履歴で差が大きい。

Team 12のAsia app→db1のidle時pingは4回平均0.487ms（0.466〜0.502ms）。Memoriesの188msを固定ネットワーク遅延だけでは説明できない。APIの時間にはSQL以外に接続取得・アプリ処理・通信も含まれるため、SQL単体が188msかかるという意味ではない。

## 変更せずに測ったSQLの占有と待ち

[20261029T150301Z-team12](../../results/20261029T150301Z-team12/)は160,707点PASS。実行バイナリ・設定は通常走行と同じで、primary・EU・USのメタデータDBから500msごとにpg_stat_activityを読み取った。拡張の導入・統計リセット・設定変更はしていない。通常走行179,991点からの低下には走行ごとの変動と観測負荷が混在し、この1走だけでは切り分けられない。

負荷開始15:03:48.465 UTCから最終確認開始15:04:54.053 UTCまでの65.588秒を集計した。全期間を含むサンプルがprimary 131、EU 132、US 131個ある。「接続サンプル」は各時点の該当接続数の合計であり、SQLの実行回数でもCPU使用時間でもない。

| 観測 | primary | EU | US |
|---|---:|---:|---:|
| activeの接続サンプル | 2,058 | 226 | 127 |
| idle in transactionの接続サンプル | 610 | 44 | 30 |
| SyncRep待ち | 1,432 | 0 | 0 |
| 待ちイベントなし | 310 | 212 | 113 |
| おすすめフィードSQLの接続サンプル | 163 | 155 | 65 |
| うち待ちイベントなし | 156 | 150 | 65 |

primaryのactiveサンプルの69.6%がSyncRep待ちで、同時に待っていた接続は最大16本だった。ただし[server.rs](../../rust/src/server.rs)は反映待ちに専用プールを割り当て、[native_write_fence.rs](../../rust/src/native_write_fence.rs)は通常の書き込み完了後に別トランザクションでremote_applyを待つ。通常のメタデータプールがこの待ちに直接占有されているとは判断できない。サンプラーはapplication_nameを採取していないため、接続単位の帰属も未確認。書き込み応答の待ちを調べる根拠にはなるが、履歴の遅さをこれだけで説明してはいけない。

EUでは待ちイベントのない接続サンプル212のうち150（70.8%）、USでは113のうち65（57.5%）がおすすめフィードSQLだった。primaryでも310のうち156（50.3%）。[feed_api.rs](../../rust/src/feed_api.rs)のfof_posts・mutual・connection・engagedを組み合わせるSQLが明確な調査対象になる。「待ちなし」は実際にCPUで実行中の場合とCPU割り当てを待つ場合などを区別しないので、これをCPU消費の割合とは呼ばない。

この走行のpostgres使用量はprimary 1.23 cores、EU 0.58 cores、US 0.47 cores。EU・USのCPU p95はともに100%で、アプリとDBがCPUを共有している。一方、startupプロセスの観測はEU 132回中122回、US 131回中120回がRecoveryWalStreamだった。海外のpostgres負荷をすべてWAL再生と見なす根拠はなく、少なくとも読み取りSQLの負荷を別に扱う必要がある。再生処理そのもののCPU時間は未計測。

primaryのエクスポートSQLは44接続サンプル、観測時点の最大query ageは259msだった。これはSQL完了時間の最大値でもAPI全体の時間でもない。500ms間隔では短いSQLを多数見落とすため、履歴SQLの出現が少ないことを「履歴キャッシュは不要」の根拠にはできない。アプリのプール取得待ち、ZIP同時実行枠の待ち、画像読み取り時間は今回のSQLサンプルには含まれない。

## 未導入の最適化と次に調べる順序

比較はリポジトリ内の現在のGo実装とRust実装を読んだ結果。前述のとおり、過去のGo走行とのソースSHAの一致は保証できていない。

| 優先 | Goにある仕組み | 現行Rustと次の作業 |
|---|---|---|
| 1 | [history_store.go](../../../webapp-optimized/go/history_store.go)に初期履歴をロードし、変更された投稿はSQLへ戻す。エクスポートのメタデータも共有 | [memory_api.rs](../../rust/src/memory_api.rs)は非空ページで認証・投稿・リアクション・コメントの4クエリ、[export_api.rs](../../rust/src/export_api.rs)も毎回メタデータSQL。まず履歴とエクスポートを共通の読み取りキャッシュで返せる構造を検討する |
| 1の前提 | [session_batch.go](../../../webapp-optimized/go/session_batch.go)のhotSessionUserと[hotstate.go](../../../webapp-optimized/go/hotstate.go)が認証・ユーザー状態を保持し、[sync.go](../../../webapp-optimized/go/sync.go)で変更を配信 | [session.rs](../../rust/src/session.rs)は毎回認証SQL。単なるレスポンスTTLではなく、ログアウト・鍵更新・アカウント削除・投稿削除・コメント等の変更を反映し、不明な状態はSQLへ戻す設計が必要 |
| 2 | hotstateのfeed/recommendがグラフと現在の投稿・反応からフィードを組み立てる | SQLサンプルでおすすめフィードが目立つ。友人関係・ブロック・反応で変わる順位とページ送りを保ち、計算をアプリへ移せるか検討する。先に現在のSQLの実行計画・候補数を測る選択肢もある |
| 3 | [image_api.go](../../../webapp-optimized/go/image_api.go)のhot.imageLiveとhistoryImageObjectで画像の有効性・所有者を解決 | Rustは画像本体・署名がキャッシュされても[image_api.rs](../../rust/src/image_api.rs)で有効性のSQLが残る。初期画像の参照索引と現在のユーザー・投稿状態を結び付ける |
| 4 | [notification_api.go](../../../webapp-optimized/go/notification_api.go)は状態変更イベントを使ってSSEへ通知 | Rustの[notification_api.rs](../../rust/src/notification_api.rs)は100msごとのバッチSQL。変更配信ができた段階でポーリングを置き換え、残る通知503も調べる |

画像の有効性キャッシュには、投稿削除後5秒以内に無効化する仕様だけでなく、鍵・アカウント・リアクションの変更も反映する。Goの3秒TTLだけを切り出して移植する方針ではない。所有者順パックとまとめ読みは既に導入済みだが、Rustは25か月分のパックに分かれ、Goの単一initial.packとは読み取り回数が異なる。初期ユーザー50人ではRustの5,929画像が625読み取り範囲にまとまる。メタデータSQLの削減後に、この残るI/Oとエクスポート同時実行2枠の待ちを測る。

細かい差として、GoのIP BANリロードは200msのバックグラウンド処理、Rustは200msごとに先着リクエストがSQLを実行する。独立した小変更として検討できる。一方、Rustのアプリログは既にerror、Nginxアクセスログは64KiB・1秒バッファなので、ログ高速化が未着手という状態ではない。通常走行のNginx使用量は3台合計0.28 coresで、direct HTTP化は候補だが履歴の点差60%を説明する根拠にはならない。

物理レプリケーションと独立DBの比較は、読み取りをどれだけアプリの状態に移せるかを見た後に行う。Goも更新を他地域へ配信しており、物理レプリカをなくすだけで整合性のための待ちがなくなるわけではない。今回の結果だけではMySQL移行・別個DB化の優劣は決められない。Asiaのdb1も用途を固定する必要はないが、現状は主DBが負荷を持つため、先にアプリ側の読み取りSQLを減らしてから再配置の余地を評価する。

次の実装候補は履歴・エクスポート共通キャッシュと、それに必要な状態更新の仕組み。約6割という値は現状の得点差の内訳であり、実装すれば6割を回収できるという予測ではない。この調査ではアプリや配備設定は変更していない。

## 証跡と再計算

比較コードは[compare.py](compare.py)、結果は[comparison.json](comparison.json)、Goの元資料はbenchmarks/、Rust通常走行は[results/20261029T131830Z-team12](../../results/20261029T131830Z-team12/)にある。

SQLの採取コードは[sample_metadata.py](sample_metadata.py)。元サンプルはactivity-20261029T150301Z-team12.json.gz、負荷区間と元ログの時刻は[activity-window.json](activity-window.json)、集計結果は[activity-summary.json](activity-summary.json)。SQLの文字列リテラルは採取時に伏せ、バインド値は採取していない。PostgreSQLの設定によってSQL末尾が切れているものがあり、識別できないSQLは断定的にAPIへ分類していない。

```sh
python3 webapp-optmized-astra/experiments/optimized-gap/compare.py
python3 webapp-optmized-astra/experiments/optimized-gap/analyze_activity.py
```

## 履歴のループに残るログイン待ち（2026-10-30）

bench/internal/scenario/history.goのrevisitは、履歴・エクスポート・古い画像を読む前に毎回ログインする。履歴APIだけの短縮では、この一連の操作の完了数が増えない場合がある。初期履歴を使う20261030T083137Z-team12では、Asiaログイン4,686回、p50 268.5ms、p90 2,005.7ms。履歴専用プールの走行では月履歴p50を80.0msから11.3msへ短縮できたが、月履歴件数は3,180→3,184、ログインp50は289.9msだった。

Go Team 9の20261029T103826Z-team9では、Asiaログイン23,161回、p50 7.9ms、p90 300.1ms。同じ利用者の一連の操作には、読み取り以外にも大きな差がある。配備ソースSHAや実行環境の差については前述の制限が残る。

現行Goのdb_write.goは、自地域のログイン・ログアウトをローカルに適用した後、他地域への通知を非同期で行う。hotstate.goは他地域の利用者のセッションを信用せず主DBで確かめる。一方Rustはログイン・ログアウトも毎回すべてのレプリカのremote_applyを待ってから応答する。この認証の配置の差は、これまでの認証キャッシュや履歴キャッシュとは別に検討する必要がある。

次の候補は、Asiaの主DBで認証する利用者を固定し、それらのセッションは他地域でも主DBで検証する方式。地域変更によって認証先が変わると同時更新時に古いセッションを受け付ける余地があるため、現在のregionだけで切り替えない。この条件を満たすログイン・ログアウトだけレプリカ待ちを省き、その他の変更の反映待ちは維持する。現時点では調査と設計であり、この方式の測定結果はまだない。
