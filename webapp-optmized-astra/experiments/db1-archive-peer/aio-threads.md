# db1のファイル読み出しをNginxのスレッドへ移す比較

通常のsendfileでdb1へ読み取りを移した初回は174,918点でPASSだった。準備のみの対照188,226点に対し、Asiaの平均ディスク読み取りは16.38→1.54MB/sへ減ったが、画像取得の中央値は74→94msへ悪化した。HTTP経由の負荷移動は機能しているものの、スコア改善は確認できていない。

次の候補はdb1のprivate画像配信だけに`aio threads`を指定する。Nginx workerがファイル読み取りで止まる時間を減らせるか、既定のスレッドプールで比較する。3地域のファイル配信やアプリのコードは変更しない。設定はarchivePeer.aioThreadsへ記録し、配備後に実際のNginx設定も照合する。

[公式のaioドキュメント](https://nginx.org/en/docs/http/ngx_http_core_module.html#aio)は、Linuxでsendfileとaio threadsを併用し、読み取りと送信をスレッドへ移せると説明している。--with-threadsでのビルドが必要。実機ではnginx -Vとnginx -tで確認する。効果は未測定。
