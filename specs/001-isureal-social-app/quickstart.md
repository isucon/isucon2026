# Quickstart Validation Guide: ISUReal

このガイドは実装完了後に、[API契約](contracts/openapi.yaml)と[データモデル](data-model.md)の主要不変条件を
ローカル環境で再現検証するための手順である。完全なテスト実装やサービス内部コードは含めない。

## Prerequisites

- Linux
- Go 1.26.5
- Node.js 24 / npm 11
- Docker Compose（PostgreSQL 16以降）
- 最新安定版デスクトップChrome
- `curl`, `jq`, JPEGテストフィクスチャ

## 1. Start and initialize

```bash
docker compose up
```

PostgreSQLのhealth check後に、`init`サービスがmigrationと初回だけのminimal fixture投入を完了し、API、worker、Webクライアントが依存順に起動する。バックグラウンド起動は`docker compose up -d`、停止は`docker compose down`を使う。

期待結果:

- `GET /health/ready`が成功し、DBマイグレーション版を返す。
- WebクライアントがAPIとService Workerを読み込む。
- 同じ起動コマンドを再実行しても既存データは保持される。完全初期化が必要な場合だけ`docker compose down --volumes`後に再起動する。

## 2. Run automated validation

```bash
go -C backend test ./...
npm --prefix frontend test
npm --prefix frontend run test:e2e
npm --prefix frontend run build
```

契約検証コマンド（実装時に`package.json`へ固定する）も実行する。

```bash
npm --prefix frontend run test:contract
```

期待結果: 単体、OpenAPI契約、実DB統合、競合、Playwright E2E、ビルドがすべて成功する。
性能スコアはこのフィーチャーの合否に含めない。

## 3. Core end-to-end scenario

以下のAPI呼び出しはすべて`/api/v1`を基点にし、読み取り専用操作を除くすべての
`POST`/`PUT`/`PATCH`/`DELETE`には、自己登録と一般・管理者ログインを含めて毎回UUIDの`Idempotency-Key`を付ける。
具体的な要求/応答形式は[OpenAPI契約](contracts/openapi.yaml)を参照する。

1. 管理者資格情報でログインし、Asiaの将来時刻を設定する。
2. `alice`, `bob`, `carol`をAsia、`dave`をEuropeで登録する。Alice→Bob申請をBobが承認する。
3. Alice→Carol申請、Carol→Bob申請を成立させ、Bobから見てCarolが距離2になる関係も用意する。
4. ワーカーの注入Clockを予定時刻へ進め、AsiaのPostingWindowを一件開始する。
5. Alice/Bob/Carolにはアプリ内通知が各一件、Daveには0件であることを確認する。
6. BobがJPEG、既定の`friends`、位置情報なしで投稿する。同じIdempotency-Key/同内容を再送する。
7. Aliceも投稿した後、friendsフィードでBobの投稿が一件だけ表示されることを確認する。
8. 2分境界後にCarolが投稿し、`onTime=false`と正の`lateSeconds`を確認する。

期待結果:

- 地域/利用者/Windowごとの通知と投稿は重複しない。
- 閲覧者は自地域の現在Windowへ投稿するまで4フィードを取得できない。
- フィードは`postedAt DESC, id DESC`で、本人投稿を含まない。
- 同一キー異内容の再送は409、24時間後は新規操作として処理される。

## 4. Visibility and interaction matrix

データモデルの[Authorization Matrix](data-model.md#authorization-matrix)に沿って次を検証する。

1. Bobの公開範囲を`friends_of_friends`へ変え、直接友達と距離2の利用者で取得する。
2. 距離2だけの利用者はRealMojiを追加できるが、コメントは403になることを確認する。
3. Bobの公開範囲を`friends`へ狭め、範囲外利用者の既存交流が保持される一方、本人以外には見えないことを確認する。
4. Bobの公開範囲を`public`へ変え、非友達が取得・コメントできることを確認する。
5. 同じ利用者がRealMojiを再送し、新IDだけが一覧へ残り旧IDが404になることを確認する。
6. 投稿者本人のRealMojiは403、本人コメントは201になることを確認する。

## 5. Relationship concurrency

競合テストランナーで同じバリアから次の操作を並行開始する。

- Alice→BobとBob→Aliceの友達申請: friendshipは一件、pendingは0件。
- pending申請の承認と取消: 確定状態は一つだけ。
- Bobへのblockと、同時のfollow/申請/交流: blockが優先され、新関係・交流は残らない。
- 同時ログイン2件: 有効セッションとPush購読は最後に確定した端末の一件だけ。
- 同一投稿へのRealMoji 2件: 有効行は一件だけ。

各ケース後にDB不変条件テストを実行し、APIからも不可視データを取得できないことを確認する。

## 6. Post deletion lifecycle

1. 同じWindowで投稿→削除→再投稿→削除を行う。
2. 2回目削除と3回目投稿を並行させる。
3. 投稿、画像、RealMoji、コメントが全取得経路で不可視で、3回目投稿が409になることを確認する。
4. 次の同地域Windowを開始し、新Windowでは再び一件投稿できることを確認する。

## 7. JPEG safety

投稿用10 MiB境界、プロフィール/RealMoji用2 MiB境界について、JPEG、非JPEG、上限+1 byteを試す。
EXIF、ICC、コメントセグメントを含むJPEGも送信する。

期待結果:

- 有効容量のJPEGだけが成立し、保存/配信画像から入力メタデータを取得できない。
- 非JPEGは415、容量超過は413で、関連DB行と公開ファイルを残さない。
- 仕様が許す大きな縦横寸法のJPEGを、寸法だけを理由に拒否しない。
- 隔離サニタイザの実行枠不足、資源上限到達、異常終了、タイムアウトを注入すると503となり、APIは継続し、
  DB行、一時画像、公開画像を残さない。同じIdempotency-Keyで資源回復後に再試行すると一件だけ成立する。
- 構造化ログに画像内容、EXIF、正確な位置情報を出力しない。

## 8. Notifications and Push failure

Push adapterをテスト用fakeへ切り替え、成功、一時失敗、恒久無効を順に返す。

- 一時失敗と結果不明: 同じ論理通知IDで初回+最大3再試行まで行い、Service Workerが複数回受信しても
  ブラウザ表示とアプリ内通知は各一件。
- 恒久無効: 購読を無効化し、以後のWindowで送信対象外。アプリ内通知は維持。
- 新規ログイン後: 旧購読へ送らず、新セッションの購読だけを使用。
- 無効セッション端末からPushを開く: 401、遷移なし、アプリ内通知は未読。
- 有効端末から開く: 対象通知だけ既読化し、未投稿/投稿済み/期間終了に応じた遷移先を返す。

## 9. Region and Memories boundaries

注入ClockをUTC境界、各固定offsetの日付境界、通知後2分境界へ動かす。

1. America=-05:00、Europe=+01:00、Asia=+09:00で`localDate`が一意に決まることを確認する。
2. 地域日が変わっても次Window開始までは旧Windowがactiveであることを確認する。
3. 進行中に地域変更を予約/上書き/取消し、旧Window終了まで現在地域が変わらないことを確認する。
4. 新地域ですでにWindowが進行中なら投稿できるが、通知が遡及作成されないことを確認する。
5. 旧Window投稿が本人の月別・日別Memoriesへ移り、交流を含み、編集/新規交流を拒否することを確認する。

## 10. Account deletion

1. 削除要求直後に本人セッション/購読と他者からのプロフィール・投稿取得が無効になることを確認する。
2. 30日未満に正しい資格情報でログインし、元の関係を含めactiveへ復旧することを確認する。
3. 再度削除し、期限直前のログインと完全削除ワーカーを競合させる。
4. 期限以後は完全削除が優先され、ログイン不能となり、稼働中は60秒以内に関連DBデータと所有画像が物理削除されることを確認する。
5. 期限後の同名登録は物理削除を同期完了し、ユーザー名を再利用して旧データを引き継がないことを確認する。
6. 既存の構造化ログは変更されないことを確認する。

## 11. Pagination and keyboard checks

各一覧へ51件以上をseedし、全ページで仕様順、最大50件、重複なしを検証する。ページ間で新規作成・更新を挟む。
不透明カーソルの改ざん、別一覧への転用は400にする。

Chrome/Playwrightでは登録、ログイン、投稿、4フィード、関係操作、ブロック、RealMoji、コメント、通知、
Memories、プロフィール、地域変更、アカウント削除をキーボードだけで完了し、フォーカスが常に視認できることを確認する。

## 12. Structured log audit

成功・認証失敗・認可失敗・入力不正・競合・Push失敗・予期しないエラーを発生させ、各JSONログが
`timestamp`, `operation`, `result`, `correlationId`を持つことを検査する。次の値が一件も現れないことを確認する。

- 平文パスワード/セッショントークン/Push秘密鍵
- 画像バイトまたは画像メタデータ
- コメント、キャプション、自己紹介本文
- 正確な緯度・経度

## 13. Browser and HTTP security boundary

1. ユーザーBearerトークンがIndexedDBにだけ永続化され、URL、DOM、Cookie、ログ、エラー、解析イベントへ現れないことを確認する。
2. Cookie、query、form bodyだけにトークンを置いた要求が401となることを確認する。
3. 状態変更要求について、設定済み公開Originと一致する`Origin`および`Sec-Fetch-Site: same-origin`は受理され、
   異なるOrigin、`cross-site`、cross-origin preflightは拒否されてCORS許可ヘッダーが返らないことを確認する。
4. `Origin`と`Sec-Fetch-Site`を送らない非ブラウザクライアントは、正しいBearerと権限があれば公開契約どおり動作することを確認する。
5. `/api/v1`の成功・エラー応答と認可画像応答が`Cache-Control: no-store`、`Pragma: no-cache`、
   `Referrer-Policy: no-referrer`、`X-Content-Type-Options: nosniff`を返すことを確認する。
6. SPA応答がFR-044記載のself限定CSP、`frame-ancestors 'none'`、`Permissions-Policy: camera=(self)`を返し、
   同一OriginのAPI、画像、Service Worker、カメラを利用できることをPlaywrightで確認する。
7. ログアウト、別端末ログインによるセッション置換、およびアカウント削除後に、以前の端末のIndexedDBから
   トークンが削除され、保護対象操作へ利用できないことを確認する。

## Completion criteria

- `spec.md`の全受け入れシナリオとSC-001〜SC-033が自動テストへ追跡される。
- OpenAPIレスポンス検証、DB不変条件、競合、E2E、ログ監査が成功する。
- 競技仕様の負荷・採点・ボトルネック情報が公開アプリ成果物へ含まれていない。
- 競技配布へ進む場合は、別feature specificationで較正と再現性ゲートを完了する。
