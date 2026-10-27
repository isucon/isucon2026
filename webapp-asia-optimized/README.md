# webapp-asia-optimized

team 7（作問者の最強版）の実装を、2026-10-27にteam 12のサーバーから写したものです。AIが2026-10-19から20にかけて旧ベンチで最適化したGoの実装で、旧ベンチでは約125万点でした。新しいベンチではteam 12で4.5万〜5.6万点、JPEGをヘッダだけ検証する版で9万〜10万点です。名前のとおり、全リージョンの要求をasiaのapp1で処理する構成（全部asia）で、海外で応答する構成と比べる基準として残します。

## 構成

- app1（asia）。`isureal`が:80を直接listenし、nginxは止めます。:8081と:8082でeuropeとamericaの要求も受け、:8090で署名表を受け取ります。初期データの画像182万枚は`~/imgcache`（36 GB、IDの先頭2文字で256ディレクトリ）にDBから展開して、ディスクから返します。
- app2とapp3（europeとamerica）。nginxが`/api/`をapp1の:8081と:8082に転送し、`X-Isureal-Region`を付けます。`isureal-go`とguardは止めます。`sigworker`が署名表`~/imagekeys.bin`（87 MB）を分担して計算し、app1に送ります。
- db1。`conf/postgresql-isucon.conf`、`sql/indexes.sql`（索引の追加と外部キーの削除）、`sql/tune2.sql`（`image_objects.data`を`STORAGE EXTERNAL`に、不要な索引の削除）を当てます。`pg_stat_statements`を使います。
- 作業の経過は`worklog.md`（team 7の作業ログ）と`deploy/gitlog.txt`（39コミットの履歴）にあります。

## ディレクトリ

- `go/`、`sql/`、`public/`、`conf/`、`worklog.md`。team 7の`~/webapp`をそのまま写しました。ビルド済みのバイナリ、`.git`、macOSの`._`ファイルは除いています。
- `deploy/`。2026-10-27にteam 12のサーバーにあった設定です。systemdのユニット（`isureal-go.service`と`override`、`sigworker.service`）、app2のnginx、PostgreSQLの`conf.d`、DBの索引と設定の一覧、元の構成に戻すSQL（`db-revert.sql`）、各サーバーの`env.sh`。team 12への複製で変えたのは`sigworker`の送り先IPだけです。

## 含めないもの

- `isureal`と`sigworker`のバイナリ。`go/`で`go build -ldflags=-checklinkname=0 ./cmd/isureal`と`./cmd/sigworker`でビルドします。`sigworker`の送り先IPは`go/cmd/sigworker/main.go`に埋め込みで、team 7の値（192.168.97.10）のままです。
- `imagekeys.bin`。`sigworker`が作ります。
- `imgcache`。DBから作ります（約10分）。
- `.git`込みの生のtarは`var/team12-backup/2026-10-27/`とS3の`team12-backup/2026-10-27/`にあります。

`env.sh`の`ISUREAL_DB_PASSWORD`は伏せてあります。provisioningが設定する値に置き換えてください。

## 新しいベンチでの注意

- 律速はJPEGの全体の復号で、app1のCPUの8割を使います。ヘッダだけの検証にすると約2倍になります。
- `sql/reset.sql`は画像を`DELETE`で消すので、走行で書いた量に比例して初期化が遅くなります。月ごとのパーティションにしたDB（webappの`001-schema.sql`）に乗せるときは、`reset.sql`をwebappのものに合わせてください。
