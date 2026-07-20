# ISUReal と実在 BeReal の相違点整理

**対象仕様**: `specs/001-isureal-social-app/spec.md`
**更新日**: 2026-07-19
**目的**: 現在のBeRealにない独自機能がISURealへ混入しないよう、意図した差分と簡略化を管理する。

## 今回のフィードバックで解消した相違

- ランキングと閲覧数集計をサービス機能および負荷モデルから削除した。
- 投稿がフィードから消える前の予告通知を削除した。
- 投稿の公開範囲を「友達」「友達の友達」「公開」の3択に修正した。
- 非公開アカウントへの片方向フォローを削除し、承認制の双方向の友達関係へ変更した。
- 公開アカウントへの一方向フォローと、双方向の友達関係を別の概念へ変更した。
- 投稿画像を前面・背面の撮影・合成ではなく、JPEG一枚だけとして扱うよう変更した。
- 負荷生成、採点、性能課題など別アプリケーションに属する仕様をISUReal仕様から削除した。
- 測定条件のない応答時間、処理割合、可用性などの数値目標を成功基準から削除し、機能が仕様どおり
  成立または拒否されることを成功基準とした。
- 投稿を削除できるのは投稿者本人だけであることと、他人による削除を拒否することを明記した。

## ISUCONの制約により残す差分

### 3地域への集約

ISURealではユーザーをアメリカ、ヨーロッパ、アジアの3地域へ分ける。これは競技環境で用意できる
サーバーが最大3台であることに対応するためであり、実在BeRealの地域区分とは一致させない。

### JPEG画像1枚

初期クライアントがデスクトップWebアプリであるため、背面カメラや前面・背面画像の合成を必須にしない。
ISURealでは、撮影または選択したJPEG画像一枚だけを投稿として扱う。

## 初回範囲を抑えるための簡略化

- Bonus BeReal
- スクリーンショット通知
- 外部音楽サービス連携
- 動画およびライブ配信
- 広告と課金

これらはISUReal独自機能で置き換えず、必要になった場合は別機能として仕様化する。

## 継続確認事項

- 認定公開アカウントと一般ユーザー間の交流範囲の詳細
- 現在のBeRealにおけるRealPeople／RealBrands関連機能の採用範囲
- 応答時間、スループット、同時利用者数、可用性を仕様化する場合の利用状況、データ規模、測定条件、合否判定

## 参照した公式資料

- [Invite & Add Friends](https://help.bereal.com/hc/en-us/articles/7537949847069-Invite-Add-Friends)
- [Friends of Friends](https://help.bereal.com/hc/en-us/articles/11773264475933-Friends-of-Friends)
- [Time to BeReal](https://help.bereal.com/hc/en-us/articles/7350386715165--Time-to-BeReal)
- [Memories](https://help.bereal.com/hc/en-us/articles/7531349180829-Memories)
- [RealPeople and RealBrand Accounts Policy](https://help.bereal.com/hc/en-us/articles/15629739988509-RealPeople-and-RealBrand-Accounts-Policy)
