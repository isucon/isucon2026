# ベンチから Router への経路を VPC peering にする計画

いまのベンチ（supervisor と dev の bench-runner）は、自分の VPC の NAT gateway を通って Router の公開 IP へ
送っている。この経路は MTU 1500 で、Router の ens5 は MSS 1448 で受ける（2026-10-27 に確認、上位帯で 3,000 本ほどの
接続がすべて NAT の 1 つの IP から来る）。同じリージョンの VPC peering で Router のプライベート IP へ送れば、
ens5 もジャンボフレーム（9001）になり、NAT gateway の処理料金とスループットの上限もなくなる。未着手で、
採否はこれから決める。

## 効果の見込み

- Router: 最上位帯では 30 秒に ens5 が約 3 GB を送受信する（data0 は約 10 GB）。data0 を 1500 から 9001 にしたときは
  CPU が約 2 割減った。ens5 は量が data0 の 3 割ほどなので、1 割前後減る見込み（未計測）。
- 料金: NAT gateway の処理料金（東京で 1 GB あたり 0.062 USD）は、最上位帯の走行 1 回で数 GB になる。同じ AZ の
  peering は無料、AZ をまたぐと 1 GB あたり 0.01 USD ずつ。
- 上限: NAT gateway は 1 つの宛先へ同時に 55,000 接続まで、帯域は最大 100 Gbps。多くのチームを同時に走らせる
  ときの共有の上限がなくなる。

## 変えるもの

| 対象 | いま | 変えたあと |
|---|---|---|
| peering | なし | supervisor の VPC（10.80.0.0/24）とチームの VPC（192.168.0.0/16）を `aws.ec2.VpcPeeringConnection` でつなぐ。dev の bench-runner の VPC（10.26.0.0/16）も同様 |
| ルート | supervisor の private のルートテーブルは 0.0.0.0/0 → NAT | 192.168.0.0/16 → peering を足す。チームの `router-public-routes` に supervisor の CIDR → peering を足す |
| Router のセキュリティグループ | 8080 を NAT の EIP から許可（`supervisor-to-router-*`） | 8080 を supervisor の CIDR から許可する |
| ベンチの宛先 | Router の EIP（`routerPublicIps`、dev は `routerPublicAddress`） | Router の公開側 ENI のプライベート IP（`t.routerPublicIp`） |
| X-Client-IP | ベンチが付ける（HAProxy は無ければ接続元を足す） | 変えない |

CIDR は重ならない（10.80.0.0/24、10.26.0.0/16、192.168.0.0/16）。

## 確かめること

- supervisor のタスクから Router のプライベート IP の 8080 に届き、`ss -tni` の MSS が 8949 になること。
- 最上位帯の Router の CPU 時間 / GB（docs/router.md の「性能」と同じ測り方）。
- チームのほかのサーバー（app と db）へは supervisor から届かないこと（セキュリティグループとネットワーク ACL）。

## 決めること

- 採るか。おすすめ: 採る。Router の負荷、料金、共有の上限のどれにも効き、競技の意味は変わらない。
