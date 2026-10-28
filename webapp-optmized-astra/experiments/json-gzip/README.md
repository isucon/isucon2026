# JSON応答の圧縮候補

現行Nginxでは`gzip_types application/json`がコメントアウトされている。APIのJSONを圧縮レベル1・最小1,024バイトで圧縮し、転送量と応答時間を比較する。JPEG・ZIP・SSEは対象に含めない。クライアントがgzipを受け付ける場合だけ圧縮する。実行設定`nginxJsonGzipEnabled`で比較し、既定は無効。AWSリソースの追加は不要。

ベンチのHTTPクライアントは標準Transportを複製し、自動圧縮を無効にしていない。採用前に実機のJSON一致、Content-Encoding、スコアとCPU・送信量を確認する。

設定の参照: [Nginx公式gzipモジュール](https://nginx.org/en/docs/http/ngx_http_gzip_module.html)。
