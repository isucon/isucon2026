# 画像バイナリ保存のローカル予備比較

PostgreSQL 16.15とMySQL 8.4.13で、画像相当のバイナリを書いて読み戻す。実機アプリの移植やISUCONスコアの比較ではない。各コンテナは2 CPU・1 GiB、DBバッファは256 MiB。512件×256 KiBを並行8件で処理し、読み取りでは全バイトを照合する。

3回反復の中央値は、書き込みがPostgreSQL 173.9 MiB/s、MySQL 70.2 MiB/s。読み取りはそれぞれ1,402.4 MiB/s、1,994.5 MiB/sだった。両DBともコンテナ再起動後の全バイト一致を確認した。データは128 MiBでキャッシュに収まる規模なので、コールドI/Oや実機の地域間遅延を評価する値ではない。

PostgreSQLはfsync、full_page_writes、synchronous_commitを有効化した。MySQLはinnodb_flush_log_at_trx_commit=1、doublewrite=ON。独立画像ストアの比較のため、PostgreSQLのWALはminimal、MySQLのbinlogは無効。全メタデータを移植する場合の複製コストやSQLの互換性は測っていない。

`run.sh`は専用コンテナを作り、実験終了時にそのコンテナとボリュームだけを削除する。同名コンテナがあれば中止する。公開待受けはlocalhostのみ。ローカル実験用のDBには認証用の秘密を置かず、本番のenvや接続先を使用しない。Rust 1.99.1とDockerを用意し、このディレクトリで実行する。

```sh
bash run.sh
```

測定結果は`results/`に保存した。`first`は初回、`repeat1`から`repeat3`は交互順での反復、`after-restart`は再起動後の読み取り。SQLには専用テーブル`astra_blob_compare`だけを作る。
