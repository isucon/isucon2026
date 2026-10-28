# 通常SQLの接続数の比較候補

既存VMは各2 CPUで、負荷後半にDB側のCPUが飽和する。各アプリの通常SQLプールは現在30接続で、待ち列と同時実行数の配分を実機で比較するため設定可能にする。

`metadataPoolConnections` は4〜64で、既定の比較元は30。`ISUREAL_METADATA_POOL_CONNECTIONS` を通常のプライマリ・読み取り用プールに反映する。応答前の複製反映確認に使う別プールと、その8バッチは変えない。

16接続で既存の実DB・レプリカを使うHTTPルーター結合テストが成功した。Clippy、TypeScript、シェル/Pythonの構文確認も成功。実機はこれから比較する。
