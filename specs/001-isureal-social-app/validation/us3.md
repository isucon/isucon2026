# US3 検証結果

検証日: 2026-07-19 (UTC)

- `ISUREAL_TEST_DATABASE_URL=... go test ./... -count=1 && go vet ./...` (`backend/`): PASS
- `npm run typecheck && npm run lint && npm run build && npm run test:e2e` (`frontend/`): PASS（4 Playwright tests）
- 固定6種、自己RealMoji拒否、自己コメント許可、距離2コメント拒否、公開投稿コメントを検証。
- RealMoji置換で旧IDを非表示化し、同時置換後もactiveは1件。コメント昇順、RealMoji降順、51件keyset境界を検証。
- 送信者/投稿者削除、第三者削除拒否、終了・削除・blockとの競合、冪等コメント作成を検証。
- RealMoji画像のJPEG、2 MiB、再エンコード、寸法非依存を検証。
- Webは `getUserMedia` とcanvas撮影だけを使用し、file inputを持たない。カメラ不在/拒否を明示し、コメントと読み取り専用Memories表示を分離した。
- 構造化ログはoperation/result/IDのみで、画像、コメント、認証情報を含めない。
