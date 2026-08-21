# 過去 ISUCON ベンチマーカーの終了条件調査

調査日: 2026-08-14。isucon org の公式リポジトリと当日マニュアル（gist 含む）から、
ベンチマーカーの終了条件を抽出した。原文は `isucon-manuals/` に保存してある。

## 出典

| 年 | 原文 | ソース |
|---|---|---|
| ISUCON10 予選 | `isucon-manuals/isucon10q-manual.md` | isucon/isucon10-qualify README → gist |
| ISUCON11 予選 | `isucon-manuals/isucon11q-manual.md` | isucon/isucon11-qualify docs/manual.md |
| ISUCON11 本選 | `isucon-manuals/isucon11f-manual.md` | isucon/isucon11-final docs/manual.md |
| ISUCON12 予選 | `isucon-manuals/isucon12q-manual.md` | isucon/isucon12-qualify README → gist |
| ISUCON12 本選 | `isucon-manuals/isucon12f-manual.md` | isucon/isucon12-final README → gist |
| ISUCON13 | `isucon-manuals/isucon13-cautionary_note.md` ほか | isucon/isucon13 docs/ |
| ISUCON14 | `isucon-manuals/isucon14-manual.md` | isucon/isucon14 docs/manual.md |

## 年度横断比較

| 年 | 負荷時間 | initialize タイムアウト | 即 fail 条件 | 途中打ち切り（abort） |
|---|---|---|---|---|
| ISUCON10 予選 | 60 秒 | 30 秒 | init/互換性チェック失敗、致命的エラー 1 回 | レスポンス誤り 10 回以上で失格、減点でスコア 0 未満も失格 |
| ISUCON11 予選 | 60 秒 | 20 秒 | init/互換性チェック失敗 | レスポンス誤り 100 回で fail（タイムアウトは減点のみ） |
| ISUCON11 本選 | 60 秒 + リトライ 10 秒 | 20 秒 ×2 回 | init/互換性/整合性チェック失敗 | レスポンス誤り 100 回超で fail |
| ISUCON12 予選 | 60 秒 | 30 秒 | init/互換性チェック失敗 | 減点後スコア 0 点確定で打ち切り（Critical −10%/件、一般 −1%/件） |
| ISUCON12 本選 | 60 秒 | 60 秒 | init/整合性チェック失敗 | エラー + タイムアウト 50 回超で負荷計測終了（−15 点/回） |
| ISUCON13 | 60 秒 | 42 秒 | init/整合性チェック失敗 | 打ち切りなし（減点制度自体なし） |
| ISUCON14 | 60 秒 | 30 秒 | init/整合性チェック失敗、クリティカルエラー 1 回、決済が負荷終了後 5 秒以内に未完了 | ワーニング 200 件以上で FAIL |

## 共通する構造

- 負荷走行時間は全年度 60 秒固定（ISUCON11 本選のみ +10 秒のリトライフェーズ付き）
- フェーズは「initialize → 事前チェック（互換性/整合性）→ 負荷走行 → 事後チェック」でほぼ共通
- initialize / 事前チェックの失敗は全年度で即 fail
- 負荷走行終了時点で処理中だったリクエストは強制切断され、減点対象外（複数年で明文化）
- abort の設計は 2 系統: 固定回数閾値（10/50/100/200 件）と、スコア 0 確定時打ち切り（ISUCON12 予選のみ）。ISUCON13 は意図的に打ち切りを廃止
- 「タイムアウトは fail 事由にせず減点のみ」は ISUCON11 予選・本選で明文化

## ISUReal ベンチマーカーへの反映

- 負荷走行時間は 60 秒固定とする（D-008、OQ-9 解決）
- 途中打ち切りは固定回数閾値方式を採用し、エラー合計が閾値に達した時点で負荷走行を
  打ち切り fail とする（D-009）。閾値は較正段階で見直す
