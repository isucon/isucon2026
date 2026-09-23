# Application deploy validation — 2026-09-23

## 2026-09-23: 現行5 VMでの実機検証

**5 VM起動・runtime配備・conformance・変更なしのvm-deploy・短いベンチ: PASS。**
VMとPortalは動作確認後も起動状態で保持した。

| 項目 | 結果 |
|---|---|
| ホスト | KVM上のLinux x86_64、20 vCPU、RAM 17979 MiB |
| VM割当 | app1〜3/db1: 各2 vCPU・1536 MiB、Router: 2 vCPU・1024 MiB |
| イメージ | r2026.08.20.2の既存3 boxをSHA-256照合して再利用 |
| runtime Ansible | 全5ホストでfailed=0、unreachable=0 |
| `make vm-validate` | PASS、2026-09-23T03:39:24Z、errors=[] |
| `make vm-deploy` | PASS、app1〜3すべてchanged=0、サービス再起動なし |
| deploy時間 | 全体88.378秒、ビルド6.117秒、配布確認48.835秒、疎通確認3.669秒 |
| 短いベンチ | `-users 2 -cycles 1`: pass=true、score=29、cycles=1、errors=[] |
| ホスト余裕 | 検証後の利用可能RAM約8.9 GiB、ホストswapなし、ディスク空き約12 GiB |

`vm-deploy`前後でAPI/worker/nginx/PostgreSQL/Router/Envoyの起動時刻、
全VMのCPU・メモリ割当、users全行から計算したデータ指紋が一致した。
各ゲストの採取時点のswap使用は0 MiB、利用可能RAMはapp各約1.1 GiB、
db1約1.0 GiB、Router約0.58 GiBだった。

このベンチは動作確認用であり、高負荷時の容量やスコアの安定性は未検証。
実VMでソース変更を伴うbackend/frontend更新、および従来更新経路との同条件比較は未実施。
88秒はビルドキャッシュ未登録・配備内容変更なしでの単発値である。

### このホストで必要だった復旧・設定

- 旧作業ディレクトリ由来の同じPortalコンテナを停止して15174番の競合を解消し、現在のComposeで再作成した。volume削除はしていない。
- 実体ファイルが存在しない旧server1〜3/RouterのVirtualBox登録を、設定XML退避後に`unregistervm`で解除した。VMファイルを削除する`--delete`は使用していない。
- `/home/nomeaning/isucon/isucon2026/provisioning/.state/images/r2026.08.20.2`に既存boxがあり、indexのSHA-256と照合後に現在のstateへハードリンクした。新規ISOビルドは中断した。
- Docker内から継承DNSに到達できなかったため、PortalビルドだけCompose overrideで`build.network: host`を使用した。実行時ネットワークと公開アドレスは元の設定を維持した。
- Tailscaleのtable 52がhost-only宛てとPortal bridge宛てに優先されていたため、以下の限定的なpolicy ruleを追加した。VPN全体の設定は変更していない。

```bash
sudo ip rule add priority 5000 to 192.168.60.64/28 lookup main
sudo ip rule add priority 5001 to 172.20.0.0/16 lookup main
```

このruleはこのホストで確認したCIDR用であり、永続化していない。再起動後は`ip rule show`と
`ip route get 192.168.60.69`を確認し、既存ruleを重複追加しないこと。
Docker subnetは`docker network inspect isureal-isureal-local-portal_default`で確認する。
検証環境を使わなくなった場合は、追加した同じ宛先・priorityのruleを`ip rule del`で解除できる。

Portalの一時overrideは`provisioning/.state/portal-build-network.yml`に保存した。
必要な場合の再ビルド手順は以下。

```bash
docker compose --project-name isureal-isureal-local-portal \
  --env-file provisioning/.state/isureal-local/portal.env \
  -f portal-mock/compose.yaml -f provisioning/.state/portal-build-network.yml \
  build --provenance=false --sbom=false
```

`make vm-up`は上記の環境依存問題で途中停止したため、Portal起動とinventory生成、
既存の`runtime.yml`を個別に実行して配備を完了し、最後に`make vm-validate`を通した。
`make vm-up`の一括実行成功とは区別する。現在の空き約12 GiBは構築用preflightの20 GiB基準を
下回るため、`vm-up`/`vm-provision`再実行前には空き容量確保が必要。
継続開発には現在のディスクを30〜50 GiB程度増やす余裕を見込み、拡張後のfilesystem容量を確認する。

### 証跡

- `provisioning/.state/runtime-validation.log`
- `provisioning/.state/isureal-local/reports/latest.json`
- `provisioning/.state/isureal-local/deploy/20260923T033931.755912Z/report.json`
- `provisioning/.state/isureal-local/before-deploy.json` / `after-deploy.json`
- `provisioning/.state/benchmark-validation.log`
- `provisioning/.state/benchmark-host-vmstat.txt`
- `provisioning/.state/status-validation.log`


### 2026-09-23: 通常60秒ベンチの追加検証

`make vm-benchmark`（既定8ユーザー、負荷走行の設定60秒、固定サイクル数なし）を実行し、
initialize・pretest・loadすべて成功。結果は`pass=true, score=1241, cycles=7, errors=[]`。
initialize開始は04:24:34 UTC、load開始は04:24:43 UTC。
負荷走行中の採取時点で、ホストの利用可能RAMは約8.8 GiBだった。
単発の通常ベンチ合格であり、高負荷限界や繰り返し実行でのスコア安定性は未検証。
ログ: `provisioning/.state/benchmark-60s-validation.log`。

以下は2026-09-22時点のローカル検証記録として保持する。

## 2026-09-22: 対象と結果

現行構成はapp1〜3＋db1のサービス4 VMとRouter 1 VM（計5 VM）。既定compactはサービス各
2 vCPU / 1536 MiB、Router 2 vCPU / 1024 MiB。旧`quickstart-results.md`の
server1〜3＋Router構成の結果とは区別する。

**ローカル実装検証: PASS。現行5 VMでの配布・ベンチ・性能比較: 未実施。**
この作業環境には既存のVM stateがなく、`VBoxManage list runningvms`も空だった。
`make vm-deploy`はVMを作成せず、`make vm-up`が必要であることを表示して非ゼロ終了した。
この記録を5 VMでのconformance合格や開発サイクル全体の短縮実績として扱わない。

## 実施済み

- `make provisioning-static`: 63 tests PASS、JSONおよびCompose設定検証PASS。
- `bash -n provisioning/scripts/build-artifacts.sh`: PASS。
- `ansible-playbook --syntax-check`による`deploy.yml`の構文検証: PASS。
- backend/frontendの実ビルドと同じ入力でのキャッシュ再利用: PASS。
- 一時inventoryを使い、実際のAnsibleで4つのローカル接続先にマイグレーション不一致を与えた。
  全4ホストで`changed=0, failed=1`となり、配布前に停止。callbackによる結果記録も確認した。
- `mise exec -- python provisioning/tests/deploy-local.py`: PASS。
  一時ディレクトリへの実Ansible copyとローカルHTTPサーバーを使用し、systemd・node readinessは
  コマンドで代用した。初回更新、変更なしの`changed=0`、backend変更時だけのAPI/worker再起動、
  frontendのみの更新で再起動なし、古いfrontendファイルの削除、backend部分配布失敗時の再起動試行、
  HTTP疎通失敗時の非ゼロ終了を検証した。実systemdやVM間ネットワークの検証ではない。
- 単体テストでビルド失敗・事前検証失敗後に配布へ進まないこと、破損キャッシュの再ビルド、
  配布失敗のレポート、選択コンポーネントだけのビルドを確認した。

## ビルド工程のみの参考計測

Linux x86_64上で`deploy.build`をコンポーネントごとに2回呼び出した経過時間。
Go/npm依存キャッシュは既存で、1回目は新しい成果物キャッシュ、2回目は同じ入力での再利用。
VMへの転送・再起動・疎通は含まない。各1回の参考値で、性能保証や従来経路との比較ではない。

| コンポーネント | ビルド実行 | 成果物キャッシュ再利用 |
|---|---:|---:|
| backend | 2.34秒 | 0.12秒 |
| frontend | 2.01秒 | 0.29秒 |

frontend再ビルド時に`npm ci`が繰り返されないことも確認した。

## VMが利用できる環境での残りの検証

1. `make vm-up`で現行5 VMを用意し、`make vm-validate`の成功とcompactの割当を確認する。
2. backend変更、frontend変更、変更なしの3ケースについて、同じ変更前のアプリ・DB状態から
   従来の`make provisioning-artifacts`＋`make vm-provision`と`make vm-deploy`を別々に計測する。
   最初の更新と変更なしの再実行を混同せず、それぞれの所要時間と成功結果を記録する。
3. `vm-deploy`の前後で、DBの起動時刻と検証用データが維持されること、Router/Envoyの起動時刻、
   全VMのCPU・メモリ割当が変わらないことを確認する。frontendのみの場合はAPI/workerの
   起動時刻も維持され、変更なしの場合は全appで`changed=0`になることを確認する。
4. 各appのreadinessとRouter経由の各appのHTML応答を確認し、実行レポートの成果物ハッシュ・
   ノード結果・build/deploy/restart/verify秒数を保存する。
5. 明示的に`make vm-benchmark BENCH_ARGS='-users 2 -cycles 1'`を実行する。
   この段階ではinitializeによるデータ初期化を伴う。

結果はこの記録に追記し、実測値から次の改善対象を決める。
