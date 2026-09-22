# Feature Specification: ISUCON2026 開発用モックポータル（1チーム前提）

**Feature Directory**: `specs/002-portal-mock/`

**Spec File**: `specs/002-portal-mock/spec.md`

**Created**: 2026-07-25

**Status**: Draft

**2026-09-22 追加変更**: メインサーバーの概念と選択機能を廃止する。`app` はクライアントの国に対応する
リージョンのサーバーへ初回設定から自動ルーティングし、ベンチマークも共通の `app` ホストを対象とする。
変更可能な設定はデフォルト国のみ。以下に残るメイン接続先の選択・表示要件はこの変更で置き換える。

**2026-09-22 仕様変更**: サーバー配置は server1=AP、server2=EU、server3=US に固定する。
リージョンは表示専用で、更新要求は同値でも `INVALID_ARGUMENT` とする。Router の snapshot/report も固定配置と
一致しなければならない。以下および本ディレクトリの設計・検証資料に残る「リージョン選択」「全台 AP」の記述は
変更前の記録であり、この変更と [Router 設計](../../docs/router.md) を優先する。

**Specification Stage**: 支援ツール仕様（開発補助モック / アプリ基準仕様・競技仕様のいずれにも属さない独立成果物）

**Input**: User description: "docs/geometrics-distribution.md の開発時のテストを行うために 1チームのみを前提とした mock となる portal画面を作成したい。画面デザインは feature spec 配下へ取り込んだ参照デザインを使用する"

## Summary

`docs/geometrics-distribution.md` の擬似地理分散アーキテクチャ（server1〜3 のリージョン配置、メイン接続先、
デフォルト国）を、開発・動作確認時に安全に切り替え・観察するための **1チーム専用モックポータル** を新設する。
ポータルは ISUReal アプリ本体とは厳密に分離した独立フロントエンド＋モックサーバーで構成し、UI は
`specs/002-portal-mock/design/ISUCON Portal Dashboard.dc.html` のデザインをピクセル基準で再現する。
モックサーバーは望ましい設定（desired）を保持し、その反映を router 役へ委ねる。反映済み設定（applied）の
真実は router 側にあり、Portal は router が報告し、構造・必須項目・値域の妥当性検証を通過した applied を
「確定（反映済み）設定」として保持・公開する。
desired と applied の差分は「反映待ち」として表示し、タイムアウトでも desired を revert しない。
モックUIは desired・applied・両者の収束状態を状態取得インターフェースから参照する。
本フェーズは router が未実装のため、applied 側（＝将来の router 役）を演じる差し替え可能な薄いスタンドインを
Portal に内蔵する。起動時に `stand-in` / `external-router` を排他選択し、次フェーズでは後者で実 Router を接続する。
クライアントの国は US/AP/EU のリージョン名ではなく、初期版で対応する10か国の国コードから選択し、国と
リージョンの対応表をポータル上で利用者へ提示する。
ベンチマーク実行は当面 UI 上の対話型シミュレーション（実行中ロック・進捗・履歴・スコア表示）に留め、
実ベンチマーカー連携は対象外とする。

## Clarifications

### Session 2026-07-25

- Q: router との設定反映モデルとタイムアウトの扱いをどう定義するか → A: 望ましい設定(desired)の Single
  Source of Truth は Portal、反映済み設定(applied)の Single Source of Truth は Router とする。Portal は
  タイムアウトで desired を revert せず、`desired ≠ applied` を「反映待ち／遅延」として表示するのみ。確定
  （反映済み）値は Router が最後に成功報告し、構造・値域の妥当性検証を通過した applied を指す。applied の SoT は Router なので、
  Portal は Router が報告した状態へ後退した場合もその報告に忠実に従い、Portal 側の判断で上書き・巻き戻しをしない。本フェーズは
  router 不在のため applied SoT（＝将来の router 役）を演じる差し替え可能な薄いスタンドインを Portal に内蔵し、
  次フェーズで実 router へ差し替える。
- Q: CI で決定的にテストするための状態初期化手段は何にするか → A: 明示的な初期化インターフェース（reset 操作）を
  常時提供し、全モック状態（desired/applied・反映待ち・実行状態・履歴・スコア）を既知の初期状態へ戻せるように
  する。プロセス再起動も引き続き初期化手段として有効。メモリ保持のみ・永続化なしのため reset のモード制限は
  設けない。
- Q: reset／再起動後の既知の初期状態（初期値）をどう固定するか → A: server1=AP・server2=AP・server3=AP、メイン
  接続先=server1、デフォルト国=JP（日本, AP リージョン）とする（全サーバー共通リージョンで従来 ISUCON に近い
  感覚、かつ日本人ユーザー前提）。実行ステータス=idle、進捗=0、履歴=空、実行回数=0、最高/直近スコア=未設定。
  `stand-in` では desired=applied、`external-router` では applied=不明／未同期とする。TeamKey と
  TeamHostnameConfig は起動時の値を維持し、reset では変更しない。
- Q: 将来の Router が desired の変更を知る方式は何か → A: Router 起点の Streaming Pull を前提とする。Router が
  Portal へストリーム購読を張り、snapshot ID 付き desired の現在値と以後の更新を受け取り、反映後に
  snapshot ID と Config を
  Portal へ報告する。Portal は Router の所在・稼働に依存せず desired の SoT として振る舞い、Router は再接続時に
  現在の desired スナップショットを再取得して収束できる。本フェーズはスタンドインがこの購読・報告契約を演じる。
- Q: ベンチマークシミュレーションの結果生成の決定性をどうするか → A: 固定シードによる決定的な結果列とする。
  reset／再起動で同じシードへ初期化し、以後の実行で毎回同じ PASS/FAIL・スコア・ログの順序を再現する。PASS と
  FAIL の双方を含む結果列とし、テスト専用の注入機構は設けない。
- Q: 参照デザインは US/AP/EU 選択で国一覧表が無いが、10か国コードのデフォルト国 UI と対応表をどう扱うか →
  A: 全体デザインは基準として維持しつつ、デフォルト国セレクトを10か国の国コードへ置換する。国コード↔リージョン
  対応表は常時表示せず、ツールチップまたは展開パネル等の補助 UI にオンデマンドで収める。
- Q: 本フェーズのスタンドインと次フェーズの実 Router の同時動作をどう扱うか → A: 起動時に `stand-in` /
  `external-router` のモードを明示選択し、両者を排他とする。`external-router` モードでは内蔵スタンドインを完全に
  停止して applied 報告元を実 Router 一つに限定する。実行中のモード切替は不可（切替は再起動を要する）。
- Q: applied 報告の検証ルールをどう定めるか → A: 対象チームが一致し、成功報告の Config が構造・必須項目・
  値域の妥当性検証を通過している場合、Portal が発行済みの desired スナップショットと snapshot ID・変更可能な
  設定値が一致するかどうかにかかわらず、Config を Router が最後に報告した実状態として AppliedConfig へ反映する。
  snapshot ID は Router が適用対象とした
  desired を示す相関情報として保持する。Router が以前のスナップショットへ戻った場合や、Portal が保持していない
  snapshot ID を報告した場合も、その状態を表示し、最新 desired と一致しなければ反映待ちを継続する。
  同じ報告が複数回届いた場合も順序付けや重複排除は行わず、最後の成功報告として同じ状態を再設定する。
- Q: reset／再起動時の applied を external-router モードでどう扱うか → A: モード別とする。`stand-in` は即座に
  desired=applied の初期状態へ戻す。`external-router` は desired のみ初期値へ戻し、applied は Router の新しい報告が
  届くまで「不明／未同期」とする。reset 時に初期設定へ過去と異なる snapshot ID を発行する。reset 前の
  snapshot ID を含む成功報告が後から届いた場合も、Config が構造・必須項目・値域の妥当性検証を通過すれば、
  Router が報告した現在の実状態として applied へ反映する。
- Q: Streaming Pull の更新配信保証をどうするか（全中間スナップショットか最新収束か） → A: desired は命令キューではなく目標
  状態なので、最新状態への収束を保証する。接続時・再接続時は必ず最新スナップショットを配信し、未処理の中間スナップショットは
  最新値へ集約（coalesce）してよい。Router がどの版を反映したかは、順序比較せず FR-017 の検証ルールで扱う。
- Q: 反映失敗を報告された後の再試行・収束をどうするか → A: 失敗理由を表示しつつ反映待ちを維持する（失敗は終端
  状態にしない）。Portal は新しい版を勝手に作らず、Router が同じ desired 版を再試行でき、後の成功報告で収束する。
  再試行方針は applied の SoT である Router 側に委ねる。
- Q: Portal–Router 契約のバージョニングをどう表すか → A: 契約版はエンドポイント／接続先の名前で表す（初期版は
  `v1`）。Streaming Pull・applied 報告・状態取得の各メッセージ本体には契約版フィールドを含めない。非互換な契約は
  別名（例: 別バージョンのエンドポイント）として提供し、未対応版への接続は成立させない。
- Q: 起動モードが未指定の場合の既定値をどうするか → A: 未指定時は `stand-in` を既定とし、単体で直ちに動作させる。
  実 Router 連携時のみ `external-router` を明示指定する。
- Q: スタンドインの成功／失敗／未報告（遅延）挙動をどう選ぶか → A: 起動時の決定的な挙動プロファイルで選択する。
  既定は `success`、ほかに同一版再試行を検証できる `fail-once-then-success`、遅延・未報告を再現する `no-report` を
  提供する。実行中の変更は行わず、UI へ開発者用コントロールは追加しない。
- Q: ベンチ実行中・反映処理中・ストリーム接続中に reset された場合の優先順位は → A: reset を最優先とする。進行中の
  ベンチマークとスタンドイン処理をキャンセルし、即時に初期化する。Streaming Pull 接続は維持し、初期化後の
  新しい desired スナップショットを配信する。reset 前のタイマー・ベンチ完了処理はキャンセルする。
  Router の成功報告は snapshot ID にかかわらず、構造・値域の妥当性検証を通過した Config を applied の実状態として反映する。
- Q: Router（特に external-router）が構造・値域として不正な Config（対象外の国コード、不明なサーバー、
  US/AP/EU 以外のリージョン等）を成功報告した場合、applied にどう扱うか → A: 構造・必須項目・値域を検証し、
  不正な成功報告は拒否して applied を変更しない。desired との相関（snapshot ID の既知性・desired との Config 一致・
  版の前後関係・現在の applied）を理由とした拒否は引き続き行わないが、値域外・不明サーバー・非対応リージョンなど
  ドメイン妥当性を欠く報告は公開契約とデータ不変条件を保護するため拒否し、失敗診断を表示して反映待ちを継続する。

### Session 2026-07-26

- Q: 構造・必須項目・値域が不正な Router 成功報告を拒否する際、ReportApplied RPC は何を返すか → A:
  Connect code `INVALID_ARGUMENT` を返す。AppliedConfig は変更せず、Portal 側の妥当性エラー診断を更新して
  反映待ちを継続する。空の正常レスポンスによる黙示的拒否や、受理結果フィールドの追加は行わない。
- Q: Router 向け RPC に起動時設定と異なる TeamKey、または team_number=0 が指定された場合は何を返すか → A:
  異なる正の TeamKey には Connect code `NOT_FOUND`、team_number=0 には `INVALID_ARGUMENT` を返す。
  いずれも状態を変更せず、`StreamDesired` は購読を開始しない。TeamKey を無視して唯一のチームへ
  読み替えることはしない。
- Q: UpdateDesired で現在の desired と同じ設定値を指定した場合、どのように扱うか → A: 冪等な no-op として
  正常終了し、`changed=false` と現在の desired／診断状態を返す。snapshot ID・desired・診断状態を変更せず、
  `StreamDesired`／`StreamState` への更新配信も行わない。
- Q: 構造・必須項目・値域が不正な Router 成功報告を拒否した後、診断状態をどう表すか → A:
  `INVALID_REPORT` 状態を使用し、`reason` に妥当性エラーを格納する。通常の反映失敗を表す
  `FAILED_RETRYABLE` とは区別し、AppliedConfig を変更せず反映待ちを継続する。後続の妥当な成功報告で
  desired と applied が一致すれば `CONVERGED` へ遷移する。
- Q: INVALID_REPORT 後に構造・値域が妥当だが最新 desired と一致しない成功報告が届いた場合、診断をどう
  更新するか → A: `INVALID_REPORT` と妥当性エラーの `reason` をクリアし、反映待ちの経過時間に応じて
  `PENDING` または `DELAYED` へ遷移する。妥当な成功報告で desired と applied が一致した場合は
  `CONVERGED` へ遷移する。解消済みの妥当性エラーを現在の診断として残さない。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 地理分散設定を切り替えて確定させる (Priority: P1)

開発者はポータルで server1／server2／server3 のリージョン（US/AP/EU）、メイン接続先サーバー、
デフォルト国（X-Country-Code 欠落時の向き先）を変更する。変更は即時に desired へ反映され、router 役が
applied を反映・報告するまで「反映待ち（desired ≠ applied）」となり、router 報告後に確定（applied 更新）する。
失敗・遅延時も Portal は desired を revert せず、applied を従来のまま維持して差分を表示する。

**Why this priority**: geometrics 構成の中核である「どこに何を置くか」の設定切り替えと確定が、開発時テストの
前提になるため。

**Independent Test**: ポータルを開いて設定を変更し、router 役が applied を報告するまでは「反映待ち」となること、
報告の受信後だけ確定（applied）値が更新され、desired は revert されないことを確認できる。

**Acceptance Scenarios**:

1. **Given** 実行中でもロックでもない, **When** server2 のリージョンを AP→EU に変更する,
   **Then** 新しい snapshot ID の desired が公開され、router 役が Streaming Pull で受信し同じ snapshot ID と Config を報告するまでは
   「反映待ち」となり、報告後に applied が EU になる。
2. **Given** desired/applied のメイン接続先が server1, **When** メイン接続先を server3 に変更して router 役が
   同じ snapshot ID と Config を報告する, **Then** `app.<team>...` のターゲット表示が server3 のホスト名解決に対応する
   applied 状態へ更新される。
3. **Given** デフォルト国が JP（日本）, **When** サポート対象国から US（アメリカ合衆国）へ変更して router 役が
   同じ snapshot ID と Config を報告する, **Then** applied が US になり、状態取得では US と対応リージョン US が反映される。
4. **Given** 設定変更が反映待ち（desired ≠ applied）, **When** router 役が反映失敗を報告する、または所定時間内に
   applied を報告しない, **Then** applied（確定値）は従来のまま維持され、desired は revert されず、失敗理由または
   「未反映／遅延」が表示される。反映が後から到着した場合は applied がその値へ追従する。
5. **Given** サポート対象国の一覧が定義されている, **When** 一覧に無い国コード（例: 除外対象の国）を
   デフォルト国に指定しようとする, **Then** 指定は受け付けられず、desired/applied は変化しない。
6. **Given** 現在の desired が server1/2/3=AP、メイン=server1、デフォルト国=JP である,
   **When** 同じ値を `UpdateDesired` で指定する,
   **Then** 正常終了して `changed=false` と現在の desired／診断状態を返し、snapshot ID・状態・ストリーム配信は
   変化しない。

---

### User Story 2 - router が desired を購読し applied を報告する (Priority: P1)

router は Portal へ Streaming Pull 接続を張り、snapshot ID 付き desired の現在値と更新を購読する。反映後は
snapshot ID と Config を
Portal へ報告する。

**Why this priority**: モックポータルの本質は「設定をRouterへ渡して反映結果を受け取る」ことであり、UI表示と同格の
中核価値であるため。

**Independent Test**: router 役が Streaming Pull の接続時に最新の desired スナップショットを受け取り、以後も最新 desired へ
収束でき、受信したsnapshot IDを関連情報として実際のConfigを報告できることを確認できる。

**Acceptance Scenarios**:

1. **Given** router 役が未接続または再接続した, **When** Streaming Pull 接続を確立する,
   **Then** 接続時点の最新の desired スナップショットが最初に配信され、その後も最新 desired への収束が保証される。
   Router が未処理の中間スナップショットは最新値へ集約されてもよい。
2. **Given** Routerが設定を反映した, **When** snapshot IDと構造・値域が妥当な実際のConfigを成功報告する,
   **Then** Portalは報告されたConfigをappliedへ反映する。
3. **Given** Routerが設定を反映できなかった, **When** snapshot IDと失敗理由を報告する,
   **Then** Portalはappliedを変更せず、現在desiredに対する失敗であれば理由を表示する。
4. **Given** router 役が構造・値域として不正な Config（対象外の国コード、不明なサーバー、US/AP/EU 以外の
   リージョン等）を成功として報告する, **When** Portal が報告を検証する,
   **Then** Portal は Connect code `INVALID_ARGUMENT` を返し、applied を変更せず、妥当性エラーを診断表示して
   診断状態を `INVALID_REPORT` として反映待ちを継続する。
5. **Given** Portal が Team 1 用に起動している, **When** Router が正の異なる TeamKey で `StreamDesired` または
   `ReportApplied` を呼ぶ, **Then** Portal は Connect code `NOT_FOUND` を返し、購読開始や状態変更を行わない。
6. **Given** Portal が起動している, **When** Router が team_number=0 で `StreamDesired` または
   `ReportApplied` を呼ぶ, **Then** Portal は Connect code `INVALID_ARGUMENT` を返し、購読開始や状態変更を行わない。

---

### User Story 3 - ベンチマーク実行を模擬し結果を観察する (Priority: P2)

開発者はポータルの「ベンチマークを実行」を押し、実行待ち→実行中（進捗）→完了（PASS/FAIL とスコア、ログ）の
一連の対話型挙動と、実行中の設定ロック、実行履歴・最高/直近スコア・実行回数の更新を確認する。

**Why this priority**: 「実行中は設定変更不可」という geometrics のルール（`docs/geometrics-distribution.md`
6章）を UI 上で検証できることが、テスト観点として重要なため。実ベンチ連携は今回対象外。

**Independent Test**: 反映待ちが無い状態で実行を開始し、実行中は設定操作が不可になり、完了後に履歴とスコア
カードが更新されることを確認できる。

**Acceptance Scenarios**:

1. **Given** 反映待ちの設定変更がある, **When** 実行を押す,
   **Then** 実行は開始されず、反映待ちを示す理由が表示される。
2. **Given** 反映待ちが無い, **When** 実行を押す,
   **Then** 実行待ち→実行中へ遷移し、進捗が更新される。
3. **Given** 実行中, **When** リージョン/メイン/デフォルト国のいずれかを変更しようとする,
   **Then** 変更は受け付けられず、実行中である旨が表示される。
4. **Given** 実行が完了する, **When** 履歴とスコアカードを見る,
   **Then** 新しい実行行（日時・対象サーバー・スコア・PASS/FAIL・展開可能なログ）が先頭に追加され、
   最高/直近スコアと実行回数が更新される。
5. **Given** モックサーバーのプロセスを再起動する, **When** ポータルを開き状態を取得する,
   **Then** 設定・履歴・スコアが初期状態に戻っている。

### Edge Cases

- 実行中に設定取得と設定変更が同時に来た場合、ロック優先で設定変更を拒否し、状態は一貫して返す。
- 反映待ち中に別の設定変更が行われた場合、desired を最新へ更新し、snapshot ID で desired スナップショットと
  applied 報告を対応付けて混同しない。未処理の中間スナップショットは最新 desired へ集約してよい。
- router 役から構造・値域が妥当な同じ成功報告が複数回届いた場合も、順序付けや重複排除を行わず
  最後の成功報告として適用する。
- 最新 desired より前に発行されたスナップショットについて構造・値域が妥当な成功報告が届いた場合も、
  Router の実状態として
  applied を更新し、最新 desired への反映待ちを継続する。
- Router が現在の applied より前の状態へ戻った場合も、成功報告された Config が構造・値域の妥当性検証を
  通過すれば applied を更新する。
- 成功報告の snapshot ID が未発行、または Config が desired と一致しない場合も、Config が構造・値域の
  妥当性検証を通過すれば Router の実状態として
  applied を更新し、最新 desired への反映待ちを継続する。
- 成功報告の Config が構造・値域として不正な場合（対象外の国コード、server1〜3 以外の不明なサーバー、
  US/AP/EU 以外のリージョン等）は、相関の一致有無にかかわらず Connect code `INVALID_ARGUMENT` を返し、
  applied を更新せず、診断状態を `INVALID_REPORT` として `reason` に妥当性エラーを格納し、反映待ちを継続する。
- `INVALID_REPORT` の後に構造・値域が妥当な成功報告が届いた場合は通常の成功報告として処理し、最新 desired と
  applied が一致すれば診断状態を `CONVERGED` へ遷移する。一致しない場合は `INVALID_REPORT` と妥当性エラーの
  `reason` をクリアし、反映待ちの経過時間に応じて `PENDING` または `DELAYED` へ遷移する。
- Router 向け RPC の TeamKey が起動時設定と異なる正の値の場合は `NOT_FOUND`、team_number=0 の場合は
  `INVALID_ARGUMENT` を返す。どちらも TeamKey を読み替えず、状態変更や購読開始を行わない。
- `UpdateDesired` の有効な patch を現在の desired に適用した結果が完全に同じ Config になる場合は、冪等な
  no-op として `changed=false` を返す。新しい snapshot ID を発行せず、desired・診断状態を変更せず、
  `StreamDesired`／`StreamState` に更新を配信しない。反映待ち中に同じ desired を再指定した場合も、
  既存の反映待ちと診断状態をそのまま維持する。
- 遅延と見なした後に router 役が実際に applied を報告した場合、Portal はその applied を確定へ追従させ、
  恒久的な desired/applied 不整合を残さない。
- router 役が反映失敗を報告した場合、反映待ちと同じ desired 版を維持し、その版の再試行後に成功報告が届けば
  新しい desired 版を発行せずに収束する。
- 状態取得を初期状態（未操作）で呼んだ場合でも、全サーバー AP、メイン server1、デフォルト国 JP、空の履歴を
  含む有効な状態を返す。`stand-in` では desired=applied、`external-router` では applied=不明／未同期とする。
- reset 後も既存の Streaming Pull 接続を維持し、新しい初期 desired スナップショットを配信する。
- ベンチマークまたはスタンドイン処理中に reset した場合、それらを即時キャンセルし、reset 前に予約された完了処理
  やタイマーが後から状態を更新しない。
- `v1` 以外の未対応な契約接続先へ接続しようとした場合、別版として解釈せず接続を成立させない。
- モックUI状態取得の applied（確定値）と UI 表示の applied は常に一致する。applied には router 役の報告値のうち
  構造・必須項目・値域の妥当性検証を通過したものだけを入れ、desired（目標）とは区別して露出する。
- サポート対象外または国コードとして解釈できない値、ならびに国コードではない AP/EU のリージョン名を
  指定した場合、デフォルト国として受け付けない。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: システムは ISUReal アプリ本体とは独立した起動・配布単位として、専用のモックポータル UI と
  モックサーバーを提供しなければならない（本体の画面・認証・ルーティングへ組み込まない）。
- **FR-002**: ポータル UI は `specs/002-portal-mock/design/ISUCON Portal Dashboard.dc.html` のレイアウト・
  配色・情報構成を基準に再現しなければならない。ただし参照デザインの US/AP/EU 形式のデフォルト国選択は、
  本仕様の10か国コード選択へ置換する（スコアカード、ベンチマーク実行パネル＋ステータスピル、
  実行履歴テーブル＋展開ログ、メイン接続先選択、サーバー個別リージョン設定、デフォルト国設定、
  ヘッダーの Team 表示、モック注記フッター）。
- **FR-003**: システムは 1チームのみを前提とし、チーム番号を設定の識別キーとして扱わなければならない。
  random-hash は公開ホスト名を推測しにくくするための値として起動時に設定し、
  `app.team<N>-<hash>.isucon2026.westens.tokyo` および `server1-3.team<N>-<hash>...` の
  ホスト名生成にだけ使用しなければならない。チームの検索、認証、認可には使用してはならない。TeamKey の
  team_number は正の整数でなければならず、Router 向け RPC で 0 が指定された場合は Connect code
  `INVALID_ARGUMENT` を返さなければならない。起動時設定と異なる正の TeamKey が指定された場合は
  `NOT_FOUND` を返さなければならない。いずれも TeamKey を無視または読み替えてはならない。
- **FR-004**: server1=AP、server2=EU、server3=US の配置を固定して表示し、ユーザーによるリージョン指定を
  同値でも `INVALID_ARGUMENT` で拒否しなければならない。
- **FR-005**: システムは `app` 宛ての通信を国に対応する固定リージョンのサーバーへ自動的に振り分ける。
  メイン接続先を選択する UI・設定項目は持たない。
- **FR-006**: ユーザーは本仕様で定義する10か国の中からデフォルト国を選択でき、システムは変更内容を snapshot ID 付き
  desired として公開しなければならない。
- **FR-007**: システムはユーザー操作で desired（望ましい設定, SoT: Portal）を即時に更新し、router 役が対応する
  applied を反映・報告するまでを「反映待ち（desired ≠ applied）」としなければならない。確定（反映済み）値は
  router 役が報告し、構造・必須項目・値域の妥当性検証を通過した applied のみで更新し、Portal は自発的に
  desired を巻き戻してはならない。
- **FR-008**: システムは全モック状態（desired/applied・反映待ち・実行状態・履歴・スコア）を稼働中だけ保持し、
  モックサーバーの再起動時に初期状態へ戻さなければならない。
- **FR-009**: システムはモックUI向けに、desired（望ましい設定）、applied（反映済み設定: 各サーバーの
  リージョン、メイン接続先、デフォルト国、ターゲット/個別サーバーのホスト名）、両者の収束状態
  （applied == desired か）、チーム識別、ロック状態を返す機械可読な取得インターフェースを提供
  しなければならない。
- **FR-010**: モックUI向け状態取得は applied（反映済み）フィールドに router 役が報告した値のみを入れ、
  未反映の目標値を applied として混入させてはならない。desired（目標）と applied（反映済み）は明確に区別して
  返し、消費側が収束状態を判別できるようにしなければならない。
- **FR-011**: システムはベンチマーク実行を対話型シミュレーションとして提供し、実行待ち→実行中（進捗）→完了
  （PASS/FAIL・スコア・ログ）を模擬しなければならない（実ベンチマーカーとの通信は行わない）。
- **FR-012**: システムは反映待ちの変更がある間、または実行中はベンチマーク実行の開始を禁止し、理由を
  表示しなければならない。
- **FR-013**: システムは実行中に全設定変更（リージョン・メイン・デフォルト国）を禁止し、ロック状態を UI と
  モックUI向け状態取得の双方で一貫して示さなければならない。
- **FR-014**: システムは実行完了時に実行履歴（日時・対象サーバー・スコア・結果・展開可能なログ）へ新規行を
  先頭追加し、最高スコア・直近スコア・実行回数を更新しなければならない。
- **FR-015**: UI は日本語表記とし、モック注記（実際の通信制御は行わない旨）を明示しなければならない。
- **FR-016**: router 役が反映失敗を報告した場合、システムは applied を従来値のまま維持し（desired は据え置き）、
  失敗理由を表示しながら反映待ちを継続しなければならない。失敗は終端状態ではなく、Router は同じ desired 版を
  再試行し、後の成功報告で収束できなければならない。Portal は失敗を理由に新しい desired 版を自動発行しては
  ならない。所定時間内に applied 報告が得られない場合でも、システムは desired を revert したり失敗として確定
  したりせず、desired ≠ applied を「未反映／遅延」として診断表示するにとどめなければならない。
- **FR-017**: システムは desired の各スナップショットへ過去に再利用しない、順序を持たない snapshot ID を
  付与しなければならない。Router の成功報告に含まれる snapshot ID は適用対象とした desired の相関情報とし、
  Config は Router が実際に適用している状態としなければならない。Portal は snapshot ID の既知性、desired との
  Config 一致、版の前後関係、現在の applied を理由に成功報告を拒否してはならず、最後の成功報告を
  AppliedConfig へ反映しなければならない。同一報告を含む複数の成功報告に対する順序付け・重複排除は行わない。
  ただし成功報告の Config は反映前に構造・必須項目・値域を検証しなければならない。対象外の国コード、server1〜3
  以外の不明なサーバー、US/AP/EU 以外のリージョンなどドメイン妥当性を欠く報告は、公開契約とデータ不変条件を
  保護するため Connect code `INVALID_ARGUMENT` で拒否し、AppliedConfig を変更せずに妥当性エラー診断を表示して
  診断状態を `INVALID_REPORT` として反映待ちを継続しなければならない。`reason` には妥当性エラーを格納し、
  通常の反映失敗を表す `FAILED_RETRYABLE` と区別しなければならない。`ReportAppliedResponse` は空のままとし、
  受理結果フィールドを追加してはならない。この妥当性検証は desired との相関（snapshot ID・Config 一致・版前後・
  現在 applied）に基づく拒否とは独立であり、相関上のズレを理由とした拒否は引き続き行ってはならない。
- **FR-018**: システムはサポート対象国を本仕様の10か国に限定し、各国を ISO 3166-1 alpha-2 の大文字2文字の
  国コードと US/AP/EU のいずれかのリージョンとの組み合わせで一意に定義しなければならない。
- **FR-019**: ポータルはサポート対象の10か国について、国コード・国名・対応リージョンをツールチップまたは
  展開パネル等のオンデマンドな補助 UI で一覧表示しなければならない。対応表は常時表示を要しない。
- **FR-020**: ポータルは、サポート対象外の国コードをデフォルト国として受け付けては
  ならず、既存の確定値を変更せずに入力エラーとして扱わなければならない。
- **FR-021**: モックUI向けインターフェースは、サポート対象国の一覧と各国の対応リージョンを
  機械的に参照できる形で提供しなければならない。
- **FR-022**: システムは全モック状態（desired/applied、反映待ち、実行状態、履歴、最高/直近スコア、実行回数）を
  既知の初期状態へ戻す明示的な reset 操作を常時提供しなければならない。reset 後の Portal 管理状態はプロセス
  再起動直後と同一でなければならない。reset は他の処理より優先され、進行中のベンチマークとスタンドイン処理を
  キャンセルして即時初期化しなければならない。初期設定へ新しい snapshot ID を発行し、既存の Streaming Pull
  接続へ配信しなければならない。reset 前に開始されたタイマーや
  ベンチ完了処理は reset 後の状態を更新してはならない。ただし Router の成功報告は snapshot ID にかかわらず、
  構造・値域の妥当性検証を通過した Config を applied の実状態として反映しなければならない。
- **FR-023**: reset またはプロセス再起動直後の既知の desired 初期値は、server1=AP、server2=EU、server3=US、
  デフォルト国を JP とする。実行ステータスは idle、進捗は 0、履歴は空、
  実行回数は 0、最高/直近スコアは未設定とし、起動時に設定された TeamKey と TeamHostnameConfig は
  reset で変更してはならない。
  `stand-in` モードでは desired=applied、反映待ちなしとして即時初期化する。`external-router` モードでは applied を
  「不明／未同期」とし、Router の新しい報告を受けるまで未収束として扱わなければならない。
- **FR-024**: システムは Router 起点の Streaming Pull により、接続時の現在値と以後の更新を含む snapshot ID 付き desired を
  Router へ配信しなければならない。Router は反映後の snapshot ID と Config を Portal へ報告し、Portal は Config が
  構造・必須項目・値域の妥当性検証を通過した場合にその報告で AppliedConfig を更新しなければならない。切断・
  再接続した Router は現在の desired を再取得して収束できなければ
  ならない。desired は目標状態であり全スナップショットのリプレイを保証する命令キューではないため、未処理の中間スナップショットは最新値へ
  集約してよいが、接続時・再接続時にはその時点の最新スナップショットを必ず配信しなければならない。
- **FR-025**: ベンチマークシミュレーションは固定シードから生成される決定的な結果列を使用し、PASS と FAIL の
  双方、スコア、対応ログを含まなければならない。reset またはプロセス再起動後は同じシードへ戻り、その後の
  実行結果の順序と内容を同一に再現しなければならない。結果を指定するテスト専用注入機構は設けない。
- **FR-026**: システムは起動時に `stand-in` または `external-router` の動作モードを一つだけ選択しなければ
  ならない。`stand-in` モードでは内蔵スタンドインだけが applied を報告し、`external-router` モードでは内蔵
  スタンドインを完全に停止して実 Router だけを報告元としなければならない。モードは実行中に切り替えられず、
  変更には再起動を要する。起動時にモードが指定されなかった場合は `stand-in` を既定とし、`external-router` は
  実 Router 連携時に明示指定しなければならない。
- **FR-027**: reset またはプロセス再起動時に、初期 desired へ過去に再利用していない snapshot ID を
  発行しなければならない。既存の Streaming Pull 接続は維持し、新しい初期 desired を配信しなければならない。
  reset 前の snapshot ID を含む成功報告が届いた場合も、Config が構造・値域の妥当性検証を通過すれば applied へ反映し、
  新しい desired への反映待ちとして扱わなければならない。
- **FR-028**: Portal–Router 間の Streaming Pull、applied 報告、状態取得の契約版はエンドポイント／接続先の名前で
  表し、初期版を `v1` としなければならない。メッセージ本体には契約版フィールドを含めない。非互換な契約変更は
  別バージョンの接続先として提供し、未対応版への接続を成立させてはならない。
- **FR-029**: `stand-in` モードは起動時に決定的な挙動プロファイルを一つ選択しなければならない。未指定時は
  `success` とし、各 desired スナップショットを成功として applied 報告する。`fail-once-then-success` は各対象スナップショットの最初の試行で
  失敗を報告し、同じスナップショットの次の試行で成功を報告しなければならない。`no-report` は applied 成否を報告せず反映待ちを
  維持しなければならない。プロファイルは実行中に変更できず、UI に制御を追加してはならない。
- **FR-030**: `UpdateDesired` の有効な patch を現在の desired へ適用した結果が現在の Config と完全一致する場合、
  システムは冪等な no-op として正常終了し、`changed=false` と現在の desired／診断状態を返さなければならない。
  新しい snapshot ID の発行、desired／診断状態の変更、`StreamDesired`／`StreamState` への更新配信を行っては
  ならない。
- **FR-031**: 診断状態が `INVALID_REPORT` のときに構造・必須項目・値域が妥当な成功報告を受理した場合、
  システムは `INVALID_REPORT` と妥当性エラーの `reason` をクリアしなければならない。受理後の applied が
  最新 desired と一致する場合は `CONVERGED`、一致しない場合は反映待ちの経過時間に応じて `PENDING` または
  `DELAYED` へ遷移しなければならない。過去の妥当性エラーを現在の診断として維持してはならない。

### Supported Countries (v1)

初期版でサポートする国は以下の**合計10か国**に固定する。US/AP/EU はサーバー配置および通信制御上の
リージョンであり、`X-Country-Code` に指定する国コードとは区別する。

| Region | Country Code | Country Name |
|--------|--------------|--------------|
| US | US | アメリカ合衆国 |
| US | CA | カナダ |
| US | MX | メキシコ |
| AP | JP | 日本 |
| AP | KR | 大韓民国 |
| AP | CN | 中華人民共和国 |
| AP | AU | オーストラリア |
| EU | GB | イギリス |
| EU | DE | ドイツ |
| EU | FR | フランス |

上表にない国は初期版の対象外とする。AF などの追加リージョンは設けず、複数リージョンへ分類し得る国も
初期版では追加しない。このためロシアを含む分類が曖昧な国も対象外とする。対象国・対応リージョンの変更は
将来の仕様改定として行う。

### Key Entities

- **TeamKey（チームキー）**: 正のチーム番号。チームごとの設定を特定するキー。0 は入力不正として
  `INVALID_ARGUMENT`、起動時設定と異なる正の値は対象不在として `NOT_FOUND` となる。
- **TeamHostnameConfig（チームホスト名設定）**: random-hash とドメインサフィックス。
  チームの公開ホスト名を生成するために Portal が保持し、TeamKey や変更可能な Config には含めない。
- **Config（変更可能な設定）**: 各サーバー(server1〜3)のリージョン、メイン接続先サーバー、デフォルト国。
- **CalculatedConfig（導出値）**: TeamKey、TeamHostnameConfig、Config から計算する app/server ホスト名、
  ターゲットホスト名、デフォルト国のリージョン。入力として受け付けない。
- **ConfigSnapshot（設定スナップショット）**: 過去に再利用しない snapshot ID、Config、CalculatedConfig、
  発行時刻。desired と applied が参照する。現在の desired と同一の Config を再指定する no-op では新規発行しない。
- **Desired（望ましい設定 / SoT: Portal）**: Portal が現在の目標として保持する ConfigSnapshot。
  タイムアウトで revert されない。
- **AppliedConfig（反映済み設定 / SoT: Router）**: router 役が実際に反映し報告した状態。モックUIが実状態
  として参照する確定値で、適用対象の snapshot ID、実際の Config、Portal が再計算した CalculatedConfig、
  報告時刻を持つ。snapshot ID に順序はなく、Router の成功報告に従って以前の状態へ戻ることもある。
  構造・値域が不正な成功報告（対象外の国コード・不明なサーバー・非対応リージョン等）は反映せず、従来値を維持する。
  `external-router` の reset／再起動直後は報告受信まで
  「不明／未同期」となる。
- **SupportedCountry（サポート対象国）**: ISO 3166-1 alpha-2 国コード、利用者向け国名、対応する
  US/AP/EU リージョンからなる固定の国定義。
- **PendingChange（反映待ち）**: applied が不明、または desired ≠ applied の状態を表す。desired スナップショットの
  snapshot ID・Config を持ち、対応する applied 報告で最新 desired と一致した時に解消される。
- **ApplyResult（反映報告）**: router 役が返す反映結果。TeamKey、対象 desired の snapshot ID、成功/失敗、
  失敗理由、報告時点の Config を表す。成功報告は AppliedConfig を更新し、失敗報告は AppliedConfig を
  維持して現在の失敗診断だけを更新する。ただし構造・値域が不正な成功報告は AppliedConfig を更新せず、
  診断状態を `INVALID_REPORT`、`reason` を妥当性エラーとして更新して Connect code `INVALID_ARGUMENT` を返す。
  本フェーズはスタンドインが生成する。
- **ApplyDiagnostic（反映診断）**: `CONVERGED`、`PENDING`、`DELAYED`、`FAILED_RETRYABLE`、
  `INVALID_REPORT`、`UNSYNCED` の状態、現在の desired/applied snapshot ID、理由、反映待ち開始時刻を持つ。
  `FAILED_RETRYABLE` は Router が報告した通常の反映失敗、`INVALID_REPORT` は成功報告の構造・必須項目・値域が
  不正で拒否された状態を表す。どちらも終端状態ではなく、後続の妥当な成功報告で収束できる。
  `INVALID_REPORT` 後に妥当な成功報告を受理した時点で妥当性エラーの `reason` をクリアし、未収束なら
  `PENDING`／`DELAYED`、収束済みなら `CONVERGED` とする。
- **DesiredSubscription（設定購読）**: Router 起点の Streaming Pull 接続。接続時・再接続時に最新の desired スナップショット
  を配信し、その後も最新状態への収束を保証する。未処理の中間スナップショットは最新値へ集約してよい。reset 後も接続を維持し、
  新しい初期 desired を配信する。
- **BenchmarkRun（実行履歴）**: 実行 ID、日時、対象メインサーバー、スコア、結果(PASS/FAIL)、ログ行。
- **PortalState（状態スナップショット）**: 上記 + 実行ステータス(idle/pending/running)・進捗・ロック理由・
  desired/applied の収束状態。モックUIが参照する。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 開発者は各設定（3リージョン・メイン・デフォルト国）を切り替えられ、router 役が報告し、
  構造・必須項目・値域の妥当性検証を通過した applied だけが確定（反映済み）となり、確定後の applied が UI と
  モックUI状態取得で 100% 一致する。desired はタイムアウトでも revert されない。
- **SC-002**: router 役は認証なしの Streaming Pull 接続で接続時・再接続時の最新の desired スナップショットを受信し、
  中間スナップショットの集約有無にかかわらず最新状態へ収束でき、成功または失敗をPortalへ報告できる。
- **SC-003**: ベンチマーク（シミュレーション）実行中は 100% のケースで設定変更が拒否され、実行完了後に
  履歴・スコアカードが更新される。
- **SC-004**: 明示的な reset 操作またはモックサーバー再起動後、Portal 管理状態は毎回同一の既知の初期状態から
  再現できる（決定的）。比較対象は設定値、実行状態、履歴、スコア、乱数列であり、snapshot ID・時刻等の
  実行ごとに発行される識別情報は含めない。`stand-in` モードでは desired/applied の設定値が一致し、
  `external-router` モードでは desired の設定値と UI/履歴/スコア状態が一致し、applied はいずれも
  「不明／未同期」から Router 報告を待つ。
- **SC-005**: 主要フロー（設定変更→確定→設定取得、実行→履歴更新、実行中ロック）が自動テストで検証され、
  reset 後の同一シードから PASS/FAIL・スコア・ログを含む同一の実行結果列を CI で再現できる。
- **SC-006**: UI とモックUI向け国一覧が同一の10か国とリージョン対応を提示し、10か国すべてを受理し、
  一覧外の国コードを100%拒否する。
- **SC-007**: router 役の成功報告のうち構造・値域が不正なもの（対象外の国コード・server1〜3 以外のサーバー・
  US/AP/EU 以外のリージョン）を 100% Connect code `INVALID_ARGUMENT` で拒否し、AppliedConfig を変更せずに
  診断状態を `INVALID_REPORT`、`reason` を妥当性エラーとして更新して反映待ちを継続する。相関上のズレ
  （未知/不一致の snapshot ID、以前の版、現在 applied との差）のみを理由とする拒否は 0 件である。
- **SC-008**: Router 向け RPC で team_number=0 を指定した全ケースを `INVALID_ARGUMENT`、起動時設定と異なる
  正の TeamKey を指定した全ケースを `NOT_FOUND` で拒否し、状態変更および `StreamDesired` の購読開始を 0 件とする。
- **SC-009**: 現在の desired と同じ結果になる有効な `UpdateDesired` を 100% `changed=false` の正常応答とし、
  snapshot ID・desired・診断状態の変更および `StreamDesired`／`StreamState` の更新イベント発行を 0 件とする。
- **SC-010**: `INVALID_REPORT` 後に構造・値域が妥当な成功報告を受理した全ケースで妥当性エラーの `reason` を
  クリアし、収束時は `CONVERGED`、未収束時は経過時間に応じた `PENDING`／`DELAYED` へ 100% 遷移する。

## Application Correctness & Safety Requirements *(support-tool boundary)*

- **Public Contract & Invariants**: 本モックは ISUReal の「アプリ基準仕様」「競技仕様」を上書き・緩和せず、
  公開機能・データ不変条件・認証認可を変更しない独立の開発補助ツールである。モックUI状態取得が
  返す desired/applied と国一覧は UI が表示する内容と常に一致する。router 役が報告していない desired を applied
  として露出せず、対象外の国コードを desired/applied のいずれにも受け入れない。desired 入力は FR-020 で、
  applied として受理する router 役の成功報告は FR-017 の妥当性検証で境界を守り、対象外の国コード・不明な
  サーバー・非対応リージョンなど構造・値域が不正な報告は applied へ反映しない。
- **Required Diagnostics**: 設定変更の確定、実行状態遷移、ロックによる拒否、および不正な Router 成功報告の
  妥当性エラーは UI 上で観測可能でなければならない。
- **Delegated Production Concerns**: router による実際の遅延・帯域制御、実ベンチマーカーのジョブ取得/結果ポスト、
  スコア算定ロジック、複数チーム対応、永続化、認証・レート制限は本仕様の対象外とし、別成果物へ委ねる。
- **Competition Handoff**: 実ベンチマーカー連携が必要になった段階で、本モックとRouter向け定義の
  版を固定して別仕様へ引き渡す。
- **Out of Scope**: 実負荷・採点式・ボトルネック台帳・想定解・複数チーム・永続化・router 内部の遅延/帯域制御。

## Assumptions

- ポータルは ISUReal 本体とは異なる独立成果物として作成し、本体のビルド/配布/ルーティングには載せない。
- router 役の applied 報告を待つ遅延判定のしきい値とベンチマーク進捗速度は開発用に調整可能とし、テスト時は
  決定的に短縮/固定できる手段を用意する。遅延判定は診断表示のためだけに用い、desired の revert や成功/失敗の
  確定には用いない。固定時間の経過だけで設定を成功扱いにはしない。
- 本フェーズは router が未実装のため、applied SoT（＝将来の router 役）を演じる差し替え可能な薄いスタンドインを
  Portal に内蔵する。起動時の `stand-in` / `external-router` モードは排他的で実行中に変更せず、次フェーズでは
  `external-router` を選択して実 Router を接続する。スタンドインは開発/テスト用に applied 報告の遅延・失敗を
  発生させられるが、これは Portal 本体へのテスト注入ではなく router 役の振る舞いである。
- 状態はモックサーバーの稼働中だけ保持し、永続化・複数チーム・複数同時利用は対象外（1チーム前提）。
  明示的な reset 操作を常時提供し、プロセス再起動と同一の既知の Portal 管理状態へ戻す。reset はメモリ上の
  モック状態を消去するだけであるため、テスト専用モード等による制限は設けない。`external-router` では reset／
  再起動後の applied を不明／未同期とし、新しい desired スナップショットへの Router 報告を待つ。
- モックUI向けRPCと router 役の Streaming Pull／applied 報告は認証なし。実ベンチマーカーからの
  ジョブ取得/結果報告などの外部連携は今回対象外
  （将来の別仕様で追加）。
- 初期版のサポート対象国は合計10か国とする。各リージョンの国数を均等にはせず、国の追加、削除、名称変更、
  リージョン再分類は将来の仕様議論と明示的な版更新で行う。
- ベンチマークのスコア/ログは表示検証用のダミーであり、実際の採点・整合性検査を意味しない。結果列は
  PASS/FAIL の双方を含む固定シードから決定的に生成し、reset／再起動で同じ先頭状態へ戻す。
