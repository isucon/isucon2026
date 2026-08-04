# Real Router / competition handoff boundary

**対象タスク**: T079  
**記録日**: 2026-08-04

## 引き渡す固定成果物

1. `portal.v1.PortalRouterService`
   - `StreamDesired`: 接続/再接続初回の最新snapshot、以後のlatest-value配信。
   - `ReportApplied`: 妥当成功を相関差にかかわらずappliedへ反映、失敗はapplied維持。
2. TeamKey
   - `team_number > 0`、起動対象以外は`NOT_FOUND`、0は`INVALID_ARGUMENT`。
   - hostname hashはTeamKey/認証ではない。
3. Config domain
   - server1–3、Region US/AP/EU、main server1–3、supported country v1 10件。
4. desired/applied/reset semantics
   - desired SoT=Portal、applied SoT=Router。
   - external-router reset直後はUNSYNCED。
   - reset前snapshotの妥当成功もRouter実状態として受理する。
5. Version evidence
   - `version-manifest.md` のcontract/generated hashesとtest revision。

## Real Router 側の責任

- Streaming Pullの接続、再接続、最新desiredへの収束。
- 実際に反映したConfigと相関snapshot IDの成功報告。
- 失敗理由の報告と同一desired版への再試行方針。
- 実際の遅延、帯域制御、リージョンrouting、運用監視。
- Portalから受信した値の適用前安全性とRouter内部の並行制御。

## 後続の競技仕様で定義する責任

- 実ベンチマーカー契約、ジョブ管理、負荷モデル、採点式、timeout。
- 初期データ、実行環境、再現条件、スコア較正。
- 意図的ボトルネック、非公開課題台帳、想定解、試し解き。
- 複数チーム、本番認証・認可、可用性・監査・秘密管理。

## 明示的に引き渡さないもの

- `portal_mock.v1` のbenchmark/runtime/history型を実ベンチ契約として使用しない。
- stand-in profileを実Routerの仕様として要求しない。
- モックの固定seed score/logを競技スコアまたは基準値として使用しない。
- nginx/Composeのlocal topologyを競技本番配置とみなさない。

## Handoff acceptance checklist

- [ ] `version-manifest.md` hashがfinal commitと一致
- [ ] proto breaking/generate-driftが成功
- [ ] external-router quickstartとgenerated clientが成功
- [ ] invalid/unknown/backward/duplicate report semanticsをRouter担当と共同確認
- [ ] supported-country tableを双方で固定
- [ ] 実bench/競技事項を別feature specificationで承認
