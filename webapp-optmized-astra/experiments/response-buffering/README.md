# Nginxの応答バッファを無効にする比較

アプリは画像を既存の不変ファイルから読み、ZIPを64 KiB・最大2チャンクで送っている。次の比較ではNginxのproxy_bufferingだけをoffにし、応答の二重バッファや一時ファイルへの退避を減らせるか確認する。要求本文のproxy_request_buffering、gzip、認証、公開範囲、ログ設定は維持する。

[Nginx公式仕様](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_buffering)では、応答バッファは既定で有効で、メモリに収まらない応答の一部を一時ファイルへ書く場合がある。無効時は受け取った応答を順次クライアントへ送る。このため、遅いクライアントではアプリのZIP送信8枠を長く占有する可能性もあり、得点とI/Oを比較する。実機で一時ファイル書き込み量をまだ分離測定していないため、削減効果は仮説。

nginxResponseBufferingをPulumi astra・nginx-runtime.sh・実設定照合へ反映。実機と同じNginx 1.28.3のローカルコンテナでon/offと反復適用の4設定をnginx -tで確認した。TypeScript・シェル・Python構文チェックも成功。

基準は利用者別export画像配置200,330点PASS。単一画像の配置共有189,772点と全フィードUS分散194,157点は撤回済み。今回の応答バッファ無効は未測定。

edf8fe51、20261030T133033Z-team12は197,797点PASS。基準200,330点に対しexport得点21,580→21,080、Asiaの平均CPU63.5→68.1%、ディスク読み取り37.6→41.7 MB/sだった。総合改善を確認できず、nginxResponseBufferingをtrueへ戻す。
