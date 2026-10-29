# 登録・ログインのNginx計測

登録のタイムアウトが準備中にも発生している。既存のアプリ計測だけでは、Nginxへの到着からアプリに渡るまでの待ち時間と、クライアントへの応答送信を区別できない。既存のPulumi設定`authTimingEnabled`がtrueのときだけ、3地域のNginxでも登録・ログインの時間を記録する。通常の比較ではfalseのままにする。

記録するのは固定の操作名、時刻、HTTP状態、処理時間、通信量、完了状態。URL・クエリ文字列・ヘッダー・本文・ユーザー識別子は記録しない。ログは32KiBでバッファリングし、1秒ごとに書き出す。通常アクセスログが有効なら、その出力も維持する。

Nginxの`request_time`はリクエストの最初のバイトを読み込んでからログ記録までの経過時間、`upstream_response_time`は上流からの応答受信に要した時間であり、差分をそのままアプリの待ち時間とは扱わない。アプリの`AUTH_TIMING`と地域・操作・時刻を照合して原因を絞る。定義は[Nginxのログ仕様](https://nginx.org/en/docs/http/ngx_http_log_module.html#access_log)と[upstream変数](https://nginx.org/en/docs/http/ngx_http_upstream_module.html#variables)を参照。

## 使用方法

Pulumiの`authTimingEnabled`をtrueにしてapplicationを配備し、verify-runtime.pyでプロセスとNginxの両方を確認する。ベンチ後、書き出しを待ってから、AWSの認証設定を適用した環境で以下を実行する。

```sh
python3 webapp-optmized-astra/deploy/auth-ingress-stats.py \
  2026-10-28T20:00:00Z 2026-10-28T20:05:00Z /tmp/auth-ingress.json
```

出力は指定期間の3地域の計測値。現在のログファイルだけを読むため、ローテーション前に回収する。調査後は同じ設定をfalseに戻して配備し、スコア比較に計測負荷を持ち込まない。

## 検証

Nginx 1.28.3のローカルコンテナで、3地域×有効・無効の6設定と、通常アクセスログと併用する設定の計7設定が`nginx -t`を通過した。実際のHTTP通信でも、登録とログインだけが記録され、別のメソッド・経路は記録されないことを確認した。クエリ・ヘッダー・本文に入れたダミー値は出力に含まれず、回収スクリプトで時間と通信量を読み取れた。無効時は計測ログが出力されなかった。

実機はAWS再認証待ちで未配備・未測定。この変更によるスコア改善は未確認。直近の比較はAsia主DB読み取り176,830点・174,401点PASS。
