# Portal Mock validation artifacts

このディレクトリは、仕様・テスト・実行結果・配布境界を追跡するための記録です。

## 記録規約

- `*-traceability-plan.md`: 実装前に期待する自動テストパスを固定する計画。
- `us1.md` / `us2.md` / `us3.md`: ストーリー別実行記録。実装担当がコマンド、commit、結果を追記できるひな形。
- `traceability.md`: 実装後に FR/SC/acceptance/edge case と最終テストパスを照合する台帳。
- `quickstart-results.md` / `final-validation.md`: 実際の clean run 後だけ PASS に変更する。未実行を成功扱いしない。
- `version-manifest.md`: 契約・生成物・ツール・テスト版を固定する。再生成時は hash を更新する。

結果欄の状態は `PASS` / `FAIL` / `BLOCKED` / `NOT RUN` のいずれかを使用します。
