# 公開成果物監査

監査日: 2026-07-19 (UTC)

## 列挙した入力

1. tracked/public source候補: `.github/`, `Makefile`, `backend/`, `frontend/`, `deploy/`, `specs/`、root設定ファイル（`.gitignore`, `.dockerignore`を含む）。
2. 契約・生成物: `contracts/openapi.yaml`, `frontend/src/api/schema.d.ts`, `frontend/dist/index.html`, hashed JS/CSS, `dist/service-worker.js`。
3. backend build: `api`, `worker`, `image-sanitizer`, `migrate`, `reset`, `seed`の`CGO_ENABLED=0 -trimpath` binary。
4. container: backend/frontend Docker build context、nginx、Compose、app env example、全`FROM`/`image`の解決済みdigest、local validation images。
5. 永続化資産: forward-only migration 000001〜000007、minimal seed、reset/migrate command。
6. 文書: spec/plan/tasks/research/data-model/quickstart/checklist/全validationと競技引継ぎgate。

`node_modules`, `.git`, test report、coverage、runtime upload、`.env`、TypeScript incremental cache、local `build/dist`は`.gitignore`/`.dockerignore`の公開対象外とした。ただしproduction bundleとbackend binaryは監査時に再生成して別途直接検査した。

## 再現コマンド

```text
find . ... -type f | sort
npm --prefix frontend run check:openapi
VITE_VAPID_PUBLIC_KEY=<public-fixture> npm --prefix frontend run build
CGO_ENABLED=0 go -C backend build -trimpath -o build/<command> ./cmd/<command>  # 全6 command
docker build -f deploy/backend.Dockerfile -t isureal-backend:validation .
docker build -f deploy/frontend.Dockerfile -t isureal-frontend:validation .
docker compose -f deploy/compose.yaml config --quiet
rg '^(FROM|\s*image:)' deploy .github/workflows/ci.yml
gitleaks 8.30.1 dir . --no-banner --redact --exit-code 1
rg -S '<private-key/API-token patterns>' <public tree>
rg -ni '<benchmark/bottleneck/calibration/trial-solving terms>' backend frontend/src deploy Makefile .github
rg '<secret sentinels|DB/VAPID private values|absolute workspace paths>' frontend/dist backend/build deploy
strings backend/build/* | rg '<workspace path|secret sentinels>'
sha256sum contracts/schema/dist/migrations/build/*
git diff --check
```

## 結果

| 観点 | 結果 |
|---|---|
| secret | checksum検証済みgitleaksで0件。private key/token pattern、bundle/binary文字列scanも0件。 |
| personal data | 実在email、電話、home path、個人credentialなし。 |
| benchmark/registry | runtime source、bundle、deploy、CIにbenchmark、score、bottleneck、calibration、trial-solving実装/registryなし。 |
| intended-solution spoiler | 競技feature・負荷モデル・ボトルネック・解法・採点値が存在しないためspoilerなし。アプリ基準実装のsource/architectureは意図した公開物。 |
| generated version | OpenAPI client drift 0、Redocly PASS、Node/npm/Go lock整合、Chrome/Playwright一致。 |
| images/containers | 全base/service/browser imageはsha256 digest固定。backend/frontend buildとCompose config PASS。 |
| binary/bundle | Go 6 binaryは`-trimpath`でworkspace pathなし。frontend bundle/SWにtest token、password、private VAPID、DB URLなし。 |
| migrations/seed | 7件forward-only、repeat reset/clean seed/failed migration rollback PASS。 |
| documentation | performance/競技作業は明示的な別feature gateだけで、実装値・解法なし。 |

## 明示的なfalse positive

- `local-development-only`と`admin-password`/`password`: localhost専用Compose/minimal fixtureの公開テストcredentialであり、本番secretではない。production configでは環境注入が必須。
- `mailto:local@example.invalid`: RFC予約`.invalid`のVAPID subject例で、個人情報ではない。
- `alice`, `bob`, `carol`, `dave`および固定UUID: 文書化された決定的minimal/test fixtureで、実利用者データではない。
- `LOG_*_SECRET`, `SECURITY_TOKEN_MUST_NOT_LEAK`, fake Push endpoint/key: secret非露出を検証するtest sentinel。production bundleとbinaryには含まれない。
- spec/tasks/validationにある「benchmark」「bottleneck」「較正」「試し解き」: それらを禁止し別featureへ送るrelease gateの記述だけで、registry entry、数値、実装、解法ではない。
- VAPID public key build fixture: 公開鍵でありsecretではない。private keyはbundle/contextへ渡していない。

合格条件は「未解決finding 0」。結果は未解決finding 0でPASS。
