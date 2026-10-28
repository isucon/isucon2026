# 登録・ログインの待ち時間の計測

反復ベンチで登録・ログインのタイムアウトが発生したため、authTimingEnabledで一時的な診断を有効にする。250ms以上のPOST /api/auth/registerとPOST /api/auth/loginだけ、受付枠の待ち時間、next.runの処理時間、レプリカ反映待ちを記録する。next.runには本文取得、IP制限などのmiddleware、SQL処理を含む。HTTPの到着前や応答の転送時間は含まない。

診断の項目は時刻、register/login、HTTPステータス、3段階の所要時間だけ。ユーザー名、鍵、トークン、本文は記録しない。auth-timing-stats.pyもこれらの項目だけをVMから取得する。通常のログレベルと失敗ログイン監視は変えず、調査後はフラグで無効にする。

計測用の変更であり、スコア向上の変更とは扱わない。実機ベンチと読み取り専用のpg_stat_activityサンプルを照合して原因を絞る。
