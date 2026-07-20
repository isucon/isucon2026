# US4 検証結果

検証日: 2026-07-19 (UTC)

- `ISUREAL_TEST_DATABASE_URL=... go test ./... -count=1 && go vet ./...` (`backend/`): PASS
- `npm run typecheck && npm run lint && npm run build && npm run test:e2e` (`frontend/`): PASS（5 Playwright tests）
- 自己登録/ログイン境界、IndexedDBセッション継続と置換、パスワード変更・再設定・username変更API/UIの不在を確認。
- 自己プロフィールのUnicodeコードポイント境界と原子的更新、JPEGプロフィール画像の置換・削除・2 MiB境界・補償削除・GCを確認。
- posting-windowのlocal dateに基づく月/日Memories、本人限定、交流snapshot、終了後read-only、51件keyset、同時挿入、本人削除を確認。
- 削除要求直後の不可視化・セッション/Push停止、30日前のログイン復旧、期限同時刻のpurge優先、再実行安全な物理削除、username再利用を確認。
- purgeは本人所有物に加え、本人投稿に紐づく他者RealMoji画像と全関連行を削除し、外部構造化ログは変更しない。
- workerは最大30秒周期で期限到来アカウントを処理し、稼働中60秒以内要件を満たす。
- T132照合で事前計画から漏れていたUS4-AS22を所有ストーリーへ戻し、`backend/internal/domain/post_policy_test.go`で範囲外・NaN・±Inf座標、空および101コードポイント地域名を検証して補正した。
