# US2 検証結果

検証日: 2026-07-19 (UTC)

- `ISUREAL_TEST_DATABASE_URL=... go test ./... -count=1` (`backend/`): PASS
- `npm run typecheck && npm run lint && npm run build && npm run test:e2e` (`frontend/`): PASS（2 Playwright tests）
- OpenAPI の検索、公開プロフィール、申請遷移、関係一覧、friendship/follow/block、管理者ログイン・認定・解除拒否を確認。
- 申請履歴と再申請、相互申請の一意な friendship 化、accept/cancel と二重解除競合、block 優先クリーンアップ、認定重複競合を実 PostgreSQL で確認。
- NFC + Unicode case folding 検索、一般/認定公開 bio 開示、入力境界、51件検索・関係一覧 keyset pagination を確認。
- Playwright 1.61.1 / Chrome 149.0.7827.55 で検索・プロフィール・関係操作のキーボード導線を確認。
- 操作ログは actor/target ID、operation、result、correlation ID のみで、プロフィール本文や認証情報を含めない。
