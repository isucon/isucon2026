# Tasks: ISUReal Social Application

**Input**: Design documents from `specs/001-isureal-social-app/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/openapi.yaml](contracts/openapi.yaml), [quickstart.md](quickstart.md)

**Validation**: Constitution 2.0.0と仕様が正常系、境界値、異常系、再試行、並行操作の自動テストを必須とするため、
各ユーザーストーリーでテストを先に作成し、実装前に期待どおり失敗することを確認する。

**Specification stage**: 2026-07-19承認済みの`spec.md` v1.0.5を固定した、Constitution 2.0.0のアプリ基準仕様。このフィーチャーは正しい基準アプリだけを実装する。
ベンチマーカー、意図的ボトルネック、較正、試し解き、競技配布は版固定後の競技仕様で扱う。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 直前までの依存を満たした後、異なるファイルを変更する他の`[P]`タスクと並行実行可能
- **[Story]**: `US1`〜`US5`は`spec.md`のユーザーストーリーに対応
- すべてのタスクに実際の変更先ファイルを明記する
- **Pre-implementation traceability gate**: 各ストーリーの実装タスク開始前に、全`USn-ASnn`を少なくとも一つの
  自動テストファイルへ割り当てた`validation/usn-traceability-plan.md`を作成し、対応するValidationタスクで
  未実装動作に対する期待失敗を確認する。T132はこの事前計画と完成テストの一致を最終検証するだけで、
  初めて不足テストを発見・追加する工程として使用しない。

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Go API/ワーカー、React SPA、PostgreSQL、共通開発コマンドの骨格を作る。

- [ ] T001 Create the backend API/worker/image-sanitizer/internal/test directory structure and placeholder package documentation in `backend/cmd/api/doc.go`, `backend/cmd/worker/doc.go`, `backend/cmd/image-sanitizer/doc.go`, and `backend/internal/doc.go`
- [ ] T002 Initialize the Go 1.26.5 module/toolchain with Echo v5, pgx, bcrypt, UUID, Unicode normalization, and Web Push dependencies in `backend/go.mod`
- [ ] T003 [P] Initialize the React/TypeScript/Vite application with Vitest, Playwright, OpenAPI type-generation scripts, and a committed exact dependency lock in `frontend/package.json` and `frontend/package-lock.json`
- [ ] T004 [P] Configure TypeScript, Vite, Vitest, and Playwright to accept and enforce an exact Chrome version and immutable test-image digest, record an implementation-test bootstrap version/digest without calling it the release target, and document the T133 release-resolution/update procedure in `frontend/tsconfig.json`, `frontend/vite.config.ts`, `frontend/playwright.config.ts`, and `frontend/README.md`
- [ ] T005 [P] Define PostgreSQL, API, worker, frontend, persistent image-volume services, and an API image that contains the separately executable image-sanitizer binary, with immutable image digests recorded at implementation time in `deploy/compose.yaml`
- [ ] T006 [P] Define environment variables and safe local defaults without secrets, including image-sanitizer executable path, concurrency, memory, CPU, and timeout budgets, in `deploy/config/app.env.example`
- [ ] T007 [P] Configure Go formatting/vetting, API/worker/image-sanitizer builds, frontend lint/typecheck, contract validation, and test commands in `Makefile`
- [ ] T008 [P] Add repository ignore rules for build output, local secrets, test artifacts, and uploaded images in `.gitignore`

**Checkpoint**: API、ワーカー、SPA、PostgreSQLを空の状態で起動・ビルドできる。

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: 全ストーリーをブロックする認証、トランザクション、画像、冪等性、カーソル、ログ、テスト基盤を実装する。

**⚠️ CRITICAL**: このフェーズ完了前にユーザーストーリー実装へ進まない。

- [ ] T009 Define typed configuration loading and startup validation for database, image root, cursor key, Push credentials, worker settings, and image-sanitizer executable/concurrency/memory/CPU/timeout budgets in `backend/internal/config/config.go`
- [ ] T010 [P] Implement injectable system/fake clocks and fixed region offsets in `backend/internal/clock/clock.go` and `backend/internal/clock/fake.go`
- [ ] T011 [P] Implement UUID generation and deterministic test IDs behind an injectable interface in `backend/internal/domain/id.go`
- [ ] T012 Create the transactional forward-only migration runner, migration metadata table, and deterministic full-reset command in `backend/cmd/migrate/main.go`, `backend/migrations/000001_migrations.up.sql`, and `backend/cmd/reset/main.go`
- [ ] T013 Create shared user, administrator, image object, login session, Push subscription, idempotency, friendship, follow, and block tables needed by cross-story visibility in `backend/migrations/000002_foundation.up.sql`
- [ ] T014 Implement pgx pool setup, transaction helpers, UUID-ordered row locking, and rollback-safe error mapping in `backend/internal/repository/postgres/db.go`
- [ ] T015 [P] Implement Echo v5 middleware for the FR-027 HTTP status/type/stable-code taxonomy, RFC 9457 problem responses, correlation IDs, request size limits, panic recovery, and JSON decoding rules in `backend/internal/httpapi/middleware.go`
- [ ] T016 [P] Implement structured JSON logging with mandatory fields and redaction for passwords, tokens, images, text bodies, bios, and exact coordinates in `backend/internal/observability/logger.go`
- [ ] T017 Write failing unit tests for complete registration/login rules, username/password/Unicode code-point validation, bcrypt cost 12, generic login failures, and unauthenticated registration/user-login/admin-login idempotency caller scopes in `backend/internal/domain/validation_test.go` and `backend/internal/service/auth_test.go`
- [ ] T018 Implement username normalization, NFC plus Unicode Default Case Folding, code-point limits, password validation, and bcrypt cost 12 in `backend/internal/domain/validation.go` and `backend/internal/service/auth.go`
- [ ] T019 Write failing integration and concurrency tests for registration, login, one active session, equal login-failure responses, and old-session/Push invalidation in `backend/tests/integration/auth_test.go` and `backend/tests/concurrency/login_test.go`
- [ ] T020 Implement opaque hashed user sessions plus shared administrator-session storage and separate authentication middleware, user registration/login/logout handlers, and redacted structured logs for user authentication success/failure paths in `backend/internal/repository/postgres/session.go`, `backend/internal/service/session.go`, and `backend/internal/httpapi/auth.go`; do not implement the administrator login HTTP handler here because T063 solely owns that endpoint and its authentication-outcome logging; deletion-pending restoration is deferred to US4, where T085, T088, and T090 first define failing lifecycle/restoration tests, T091-T092 add the account-lifecycle schema and domain rules, and T095 implements restoration
- [ ] T021 Write failing tests proving every OpenAPI POST/PUT/PATCH/DELETE—including registration, user/admin login, logout, profile/region/image/Push subscription, notification open, relationships, posts/interactions/deletes, schedules, and certification—requires `Idempotency-Key`, plus authenticated and unauthenticated caller scoping, missing-key 400, non-finalizing/no-mutation 503 retry with the same key, 24-hour same-request replay from the first non-503 result, different-request 409, in-flight duplicates, and expired-key generations in `backend/tests/integration/idempotency_test.go`
- [ ] T022 Implement transactional idempotency acquisition/finalization and middleware response replay for every state-changing HTTP operation, using principal scope when authenticated and auth-kind plus normalized username for unauthenticated registration/user-login/admin-login, rolling back and marking 503 attempts retryable without a finalized response, in `backend/internal/repository/postgres/idempotency.go` and `backend/internal/httpapi/idempotency.go`
- [ ] T023 [P] Write failing tests for signed cursor tampering, list binding, ordering directions, and 50-item bounds, and record the expected pre-implementation failures in `backend/internal/httpapi/cursor_test.go`
- [ ] T024 After T023's expected failures are verified, implement versioned HMAC-signed keyset cursors in `backend/internal/httpapi/cursor.go`
- [ ] T025 Write failing use-case-parameterized JPEG store tests for MIME/encoded-byte boundaries, metadata stripping, atomic publication, orphan cleanup, dimension-independent acceptance under an adequate test resource profile, and isolated sanitizer slot exhaustion, memory/CPU limit, crash, timeout, 503/no-partial-state, API-survival, and retry behavior in `backend/internal/image/store_test.go` and `backend/tests/integration/image_sanitizer_test.go`
- [ ] T026 Implement a one-shot JPEG sanitizer subprocess with configurable concurrency, OS memory/CPU/time budgets, cleanup and 503 mapping for slot exhaustion/resource limit/crash/timeout, while never using decoded pixel count or dimensions as a fixed rejection rule, plus per-owner immutable object keys with SHA-256 integrity metadata, atomic rename, authorized serving, and unreferenced-file GC in `backend/cmd/image-sanitizer/main.go`, `backend/internal/image/store.go`, `backend/internal/image/sanitizer.go`, and `backend/internal/httpapi/images.go`
- [ ] T027 [P] Create reusable PostgreSQL fixtures, fake Clock, fake Push adapter, controllable image-sanitizer process adapter, HTTP server harness, and invariant assertions in `backend/tests/testkit/testkit.go`
- [ ] T028 [P] Create Echo v5 OpenAPI request/response validation middleware and a contract-test harness that verifies every FR-027 status/type/code mapping, every state-changing operation's required `Idempotency-Key` and 409 response, and the absence of that header on reads from `specs/001-isureal-social-app/contracts/openapi.yaml` in `backend/internal/httpapi/openapi.go` and `backend/tests/contract/harness_test.go`
- [ ] T029 Assemble the Echo v5 router, health/readiness endpoints, middleware ordering, and graceful shutdown in `backend/internal/httpapi/router.go` and `backend/cmd/api/main.go`
- [ ] T030 Assemble retryable DB-job polling, `FOR UPDATE SKIP LOCKED`, graceful shutdown, and fake-adapter injection in `backend/internal/worker/runner.go` and `backend/cmd/worker/main.go`
- [ ] T031 [P] Build the SPA shell, protected routing, session replacement behavior, persistent keyboard-accessible logout control, logout API/session-state cleanup with post-logout focus transfer, accessible focus styles, and generated API client in `frontend/src/app/App.tsx`, `frontend/src/app/auth.ts`, `frontend/src/app/LogoutButton.tsx`, `frontend/src/app/styles.css`, and `frontend/src/api/client.ts`
- [ ] T032 Add deterministic minimal seed data and a repeatability test for schema, constraints, users, administrators, and settings in `backend/cmd/seed/main.go` and `backend/tests/integration/reset_test.go`

**Checkpoint**: Foundation ready。各ストーリーは専用フィクスチャを使い、他ストーリーのUI/API実装なしで単独検証できる。

---

## Phase 3: User Story 1 - 日次の写真を投稿して友達の今を見る (Priority: P1) 🎯 MVP

**Goal**: 現在の投稿期間へJPEG画像一枚を投稿し、時間内外・公開範囲・位置情報を保持して、投稿後に4フィードと自分の現在投稿を閲覧・管理できる。

**Independent Test**: PostingWindowと友達/距離2/follow関係をDBフィクスチャで用意し、通知後2分の境界前後で投稿して、投稿前ゲート、公開範囲、フィード順、削除・再投稿上限をAPIとChromeから検証する。

### Validation for User Story 1 (write first and verify failure)

- [ ] T033 [P] [US1] Before any US1 implementation, map US1-AS01..US1-AS17 to the US1 validation test files in `specs/001-isureal-social-app/validation/us1-traceability-plan.md`, then write the sole failing OpenAPI contract tests for the user-facing current-window read plus create/get/update/delete current or ended owner post, four feeds, and current-self post in `backend/tests/contract/posts_test.go`; T103 must reuse this current-window contract instead of duplicating it
- [ ] T034 [P] [US1] Write failing domain tests for two-minute classification, fixed-region current-window selection, visibility, exact-distance-two exclusion, comment-independent feed access, and location unions in `backend/internal/domain/post_policy_test.go`
- [ ] T035 [P] [US1] Write failing integration tests for post-before-feed gate, cross-region viewer/poster windows, all three visibility values, self exclusion, caption/visibility updates, ended-window denial, and the post image path's JPEG MIME/10 MiB boundaries, metadata stripping, and dimension-independent acceptance in `backend/tests/integration/posts_test.go`
- [ ] T036 [P] [US1] Write failing concurrency tests for one active post, first repost, second-delete-versus-repost, and idempotent post/delete retries in `backend/tests/concurrency/posts_test.go`
- [ ] T037 [P] [US1] Write failing pagination tests for 51+ items, tie-breaking IDs, concurrent inserts, exact distance-two feed membership, and cursor non-duplication in `backend/tests/integration/feeds_test.go`
- [ ] T038 [P] [US1] Write failing Playwright journey for opening the posting screen with friends default, uploading one JPEG, viewing own post, and browsing four feeds in `frontend/tests/e2e/post-and-feed.spec.ts`

### Implementation for User Story 1

- [ ] T039 [P] [US1] Create posting window, user posting-window state, post, and location constraints plus feed indexes in `backend/migrations/000003_posts.up.sql`
- [ ] T040 [P] [US1] Define PostingWindow, Post, Visibility, Location, on-time calculation, and post-slot transition domain types in `backend/internal/domain/post.go`
- [ ] T041 [US1] Implement posting-window, post-slot, post CRUD, and keyset feed repositories with row-lock order from the data model in `backend/internal/repository/postgres/posts.go`
- [ ] T042 [US1] Implement the centralized VisibilityPolicy with distinct concepts for the inclusive friends-plus-distance-two `friends-of-friends` audience and the exact-distance-two-only `friends-of-friends` feed, plus blocks, active status, viewer-posted gate, public scope, follow scope, and self exclusion in `backend/internal/service/visibility.go`
- [ ] T043 [US1] Implement post creation, immutable image/location enforcement, current-period caption/visibility updates, owner-only deletion for both current and ended posts, deletion counter, associated image/interaction hiding, and repost rules in `backend/internal/service/posts.go`
- [ ] T044 [US1] Implement current-window, post CRUD including owner deletion of ended posts, self-current-post, and four keyset-feed HTTP handlers in `backend/internal/httpapi/posts.go`
- [ ] T045 [P] [US1] Implement the keyboard-accessible post form with JPEG preview, friends default, caption, visibility, and exclusive location controls in `frontend/src/features/posts/PostComposer.tsx`
- [ ] T046 [P] [US1] Implement self-current-post management including caption/visibility updates and delete/repost state in `frontend/src/features/posts/MyCurrentPostPage.tsx`
- [ ] T047 [P] [US1] Implement four independently paged feed views with an exact-distance-two-only `friends-of-friends` feed, direct friends confined to the friends feed between those two views, and on-time/late and location presentation in `frontend/src/features/feeds/FeedPage.tsx`
- [ ] T048 [US1] Wire post/current/feed routes and generated API calls into the SPA navigation in `frontend/src/app/routes.tsx` and `frontend/src/api/posts.ts`
- [ ] T049 [US1] Add structured operation/result logging without image, caption, or exact-location content for all US1 handlers in `backend/internal/httpapi/posts.go`
- [ ] T050 [US1] Run US1 contract, unit, integration, concurrency, pagination, and Playwright suites and record the verified commands/results in `specs/001-isureal-social-app/validation/us1.md`

**Checkpoint**: P1を単独デモ可能。これが推奨MVPである。

---

## Phase 4: User Story 2 - 友達とフォローを管理する (Priority: P2)

**Goal**: ユーザー検索、友達申請/承認/拒否/取消/解除、認定公開アカウントのfollow、block/unblock、関係一覧を一貫した競合規則で提供する。

**Independent Test**: ユーザーだけを登録し、投稿APIを使わずに、通常/認定アカウントの公開プロフィール開示、全関係遷移、相互申請、ブロック優先、検索・一覧ページングを検証する。

### Validation for User Story 2 (write first and verify failure)

- [ ] T051 [P] [US2] Before any US2 implementation, map US2-AS01..US2-AS25 to the US2 validation test files in `specs/001-isureal-social-app/validation/us2-traceability-plan.md`, then write failing OpenAPI contract tests for search and another user's disclosure-aware public profile, friend requests, relationship lists, friendship/follow/block operations, administrator certification, and certification-removal rejection in `backend/tests/contract/relationships_test.go`
- [ ] T052 [P] [US2] Write failing domain tests for request actor permissions, self-relation rejection, certified-only follow, irreversible certification, and general/certified-public bio visibility in `backend/internal/domain/relationship_policy_test.go`
- [ ] T053 [P] [US2] Write failing integration tests for request lifecycle/history, immediate re-request, independent follow plus friendship, removal, block cleanup, unblock without restoration, and pending-only lists in `backend/tests/integration/relationships_test.go`
- [ ] T054 [P] [US2] Write failing concurrency tests for mutual requests, accept-versus-cancel, double friendship removal, block-versus-request/follow, and duplicate certification in `backend/tests/concurrency/relationships_test.go`
- [ ] T055 [P] [US2] Write failing search/public-profile/list integration tests for NFC case folding, inactive/blocked exclusion, general-versus-certified-public bio disclosure, 1..100 query limits, 51+ results, relationship ordering, and concurrent updates in `backend/tests/integration/user_search_test.go`
- [ ] T056 [P] [US2] Write failing Playwright journey for search, friend request decisions, follow/unfollow, block/unblock, and keyboard operation in `frontend/tests/e2e/relationships.spec.ts`

### Implementation for User Story 2

- [ ] T057 [P] [US2] Extend the foundational schema with friend-request history, normalized-pair lifecycle constraints, normalized display-name search support, public-profile disclosure fields/indexes, and relationship-list indexes in `backend/migrations/000004_relationships.up.sql`
- [ ] T058 [P] [US2] Define relationship entities, request states, normalized pairs, transition errors, and profile disclosure policy in `backend/internal/domain/relationship.go`
- [ ] T059 [US2] Implement searchable users, disclosure-aware reads of another user's public profile, and keyset relationship-list repositories with normalized username/display-name fields in `backend/internal/repository/postgres/users.go` and `backend/internal/repository/postgres/relationships.go`
- [ ] T060 [US2] Implement transactional friend request transitions, mutualization, friendship removal, follow lifecycle, and certification guards in `backend/internal/service/relationships.go`
- [ ] T061 [US2] Implement block-priority locking, bidirectional relationship/pending cleanup, actor-only unblock, and relationship creation denial in `backend/internal/service/blocks.go`
- [ ] T062 [US2] Implement user search, another user's disclosure-aware public-profile read, request transition, relationship list, friendship, follow, and block handlers in `backend/internal/httpapi/relationships.go`; authenticated self-profile reads and mutations remain owned by US4
- [ ] T063 [US2] Using the administrator-session storage and middleware primitives from T020, implement the sole administrator login HTTP handler, irreversible certification and state-preserving certification-removal rejection handlers, and redacted structured logs for every administrator authentication/certification outcome in `backend/internal/httpapi/admin_users.go`; no other task owns the administrator login endpoint
- [ ] T064 [P] [US2] Implement search results and disclosure-aware profile views in `frontend/src/features/users/UserSearchPage.tsx` and `frontend/src/features/users/ProfilePage.tsx`
- [ ] T065 [P] [US2] Implement friends/follows/followers/incoming/outgoing lists and request action controls in `frontend/src/features/relationships/RelationshipPage.tsx`
- [ ] T066 [P] [US2] Implement follow, friendship removal, block, and unblock confirmations with post-action state refresh in `frontend/src/features/relationships/RelationshipActions.tsx`
- [ ] T067 [US2] Wire US2 routes/API clients and redact relationship-operation logs in `frontend/src/app/routes.tsx`, `frontend/src/api/relationships.ts`, and `backend/internal/httpapi/relationships.go`
- [ ] T068 [US2] Run US2 contract, unit, integration, concurrency, pagination, and Playwright suites and record the verified commands/results in `specs/001-isureal-social-app/validation/us2.md`

**Checkpoint**: P2は投稿作成なしで関係管理機能として単独検証でき、P1は関係APIで作った可視性を利用できる。

---

## Phase 5: User Story 3 - 投稿へRealMojiとコメントを送る (Priority: P3)

**Goal**: 現行で閲覧可能な投稿へ固定6種のRealMojiとコメントを追加・一覧・削除し、置換、権限、期間終了、公開範囲変更を正しく扱う。

**Independent Test**: 投稿・関係をフィクスチャ投入し、直接友達、距離2のみ、公開閲覧者、投稿者本人でRealMoji/コメントの権限差と削除者権限を検証する。

### Validation for User Story 3 (write first and verify failure)

- [ ] T069 [P] [US3] Before any US3 implementation, map US3-AS01..US3-AS15 to the US3 validation test files in `specs/001-isureal-social-app/validation/us3-traceability-plan.md`, then write failing OpenAPI contract tests for RealMoji list/replace/delete and comment list/create/delete in `backend/tests/contract/interactions_test.go`
- [ ] T070 [P] [US3] Write failing domain tests for six reaction types, self-RealMoji denial, self-comment allowance, distance-two comment denial, current-post checks, and Unicode limits in `backend/internal/domain/interaction_policy_test.go`
- [ ] T071 [P] [US3] Write failing integration tests for RealMoji replacement/old-ID hiding, ascending comments, descending RealMojis, sender/post-author deletion, narrowed visibility, ended-window denial, Memories retention, and the RealMoji image path's JPEG MIME/2 MiB boundaries, metadata stripping, and dimension-independent acceptance in `backend/tests/integration/interactions_test.go`
- [ ] T072 [P] [US3] Write failing concurrency tests for simultaneous replacement, post-delete-versus-create, duplicate comment retries, and block-versus-interaction in `backend/tests/concurrency/interactions_test.go`
- [ ] T073 [P] [US3] Write failing 51+ item pagination tests with concurrent insertion/replacement and no duplicates in `backend/tests/integration/interaction_pagination_test.go`
- [ ] T074 [P] [US3] Write failing Playwright journeys for live-camera RealMoji replacement, camera absence/permission denial, absence of any file-picker/upload fallback, comment creation, lists, and authorized deletion in `frontend/tests/e2e/interactions.spec.ts`

### Implementation for User Story 3

- [ ] T075 [P] [US3] Create RealMoji and comment tables with one-active-reaction and list-order indexes in `backend/migrations/000005_interactions.up.sql`
- [ ] T076 [P] [US3] Define RealMoji/comment entities, reaction enum, creation/deletion policies, and ordering keys in `backend/internal/domain/interaction.go`
- [ ] T077 [US3] Implement active RealMoji replacement/history hiding, comment persistence, keyset lists, and authorized soft deletion in `backend/internal/repository/postgres/interactions.go`
- [ ] T078 [US3] Implement interaction authorization through current VisibilityPolicy, transactional post/block rechecks, JPEG sanitation, and idempotency in `backend/internal/service/interactions.go`
- [ ] T079 [US3] Implement interaction HTTP handlers and safe structured logs in `backend/internal/httpapi/interactions.go`
- [ ] T080 [P] [US3] Implement camera-only RealMoji capture/type controls with no file-picker/upload path, explicit unavailable/permission-denied states, replacement state, and paged list in `frontend/src/features/interactions/RealMojiPanel.tsx`
- [ ] T081 [P] [US3] Implement comment creation, paged ascending list, and conditional delete controls in `frontend/src/features/interactions/CommentPanel.tsx`
- [ ] T082 [US3] Create the routed current-post detail container and integrate RealMoji/comment panels into its interactive mode and the Memories read-only mode in `frontend/src/features/posts/PostDetail.tsx` and `frontend/src/app/routes.tsx`; T046 owns self-post management while this component owns shared post-detail data loading and interaction-panel composition
- [ ] T083 [US3] Run US3 contract, unit, integration, concurrency, pagination, and Playwright suites and record the verified commands/results in `specs/001-isureal-social-app/validation/us3.md`

**Checkpoint**: P3はUS1の投稿基盤完了後、関係をfixture投入して独立検証できる。

---

## Phase 6: User Story 4 - アカウント、プロフィール、Memoriesを管理する (Priority: P4)

**Goal**: 厳密な自己登録/ログイン、プロフィール/画像更新、本人限定Memories、30日削除猶予・復旧・完全削除を提供する。

**Independent Test**: Clockとフィクスチャで、資格情報境界、本人プロフィール更新、画像境界、月別Memories、削除直後の不可視化、期限前復旧、期限到来完全削除と名前再利用を検証する。

### Validation for User Story 4 (write first and verify failure)

- [ ] T084 [P] [US4] Before any US4 implementation, map US4-AS01..US4-AS35 to the US4 validation test files in `specs/001-isureal-social-app/validation/us4-traceability-plan.md`, then write failing OpenAPI contract tests for authenticated get/update me, self profile-image put/delete, account deletion, monthly/daily Memories, and the deliberate absence of password-change, password-reset, and username-change operations while proving rejected attempts leave credentials and usernames unchanged in `backend/tests/contract/account_test.go`; another user's public-profile contract remains owned by T051
- [ ] T085 [P] [US4] Write failing account-lifecycle domain tests for pending deletion, deadline transition, purge priority, username release, and preserved structured logs in `backend/internal/domain/account_lifecycle_test.go`
- [ ] T086 [P] [US4] Write failing authenticated self-profile/image tests for code-point boundaries, optional image lifecycle, EXIF stripping, exact byte limits, and invalid-update atomicity in `backend/tests/integration/profile_test.go`; public-profile disclosure integration remains owned by T055
- [ ] T087 [P] [US4] Write failing Memories tests for fixed-offset local dates, month/day filtering, owner-only interactions, 51+ keyset order, concurrent inserts, edit/interaction denial after window end, owner deletion of an ended post, non-owner denial, and removal of the deleted post, image, RealMoji, and comments from every read path in `backend/tests/integration/memories_test.go`
- [ ] T088 [P] [US4] Write failing deletion lifecycle tests for immediate hiding, frozen relationships, pre-deadline restore, due-time purge, username reuse, preserved logs, and all related-data removal in `backend/tests/integration/account_deletion_test.go`
- [ ] T089 [P] [US4] Write failing concurrency/failure tests for login-versus-purge at the deadline, repeated purge, image-finalize DB failure, and unreferenced-image GC in `backend/tests/concurrency/account_deletion_test.go`
- [ ] T090 [P] [US4] Write failing Playwright journeys for registration/login, closing and reopening desktop Chrome through a fresh browser context while restoring the same valid IndexedDB-backed session, profile and image management, Memories navigation, owner deletion of an ended post with disappearance from month/day views, account deletion request, restoration login, and the absence of password-change, password-reset, and username-change UI while credentials and usernames remain unchanged in `frontend/tests/e2e/account-and-memories.spec.ts`

### Implementation for User Story 4

- [ ] T091 [P] [US4] Add deletion due-time, authenticated self-profile mutation support, Memories and image-reference indexes, plus explicit purge support constraints in `backend/migrations/000006_accounts_memories.up.sql`; public-profile search/disclosure schema remains owned by T057
- [ ] T092 [P] [US4] Define profile mutation, account lifecycle, Memories calendar, and purge domain rules in `backend/internal/domain/account.go` and `backend/internal/domain/memory.go`
- [ ] T093 [US4] Implement authenticated self-profile read/update and image-reference changes in `backend/internal/repository/postgres/profiles.go`; another user's disclosure-aware public-profile lookup remains owned by T059
- [ ] T094 [US4] Implement owner-only monthly and daily Memories queries with fixed posting-window local dates and interaction snapshots in `backend/internal/repository/postgres/memories.go`
- [ ] T095 [US4] Implement deletion request, immediate session/Push invalidation, frozen pending state, login restoration, deadline purge priority, synchronous related-row/owned-image deletion, and username release in `backend/internal/service/accounts.go`
- [ ] T096 [US4] Implement retry-safe due-account selection and physical purge completion within 60 seconds while the service is running in `backend/internal/worker/account_purge.go`
- [ ] T097 [US4] Implement authenticated self-profile, self-image, account-deletion, and Memories HTTP handlers with secret/content redaction in `backend/internal/httpapi/accounts.go` and `backend/internal/httpapi/memories.go`; another user's public-profile handler remains owned by T062
- [ ] T098 [P] [US4] Implement registration/login pages with exact input preservation and generic authentication errors in `frontend/src/features/auth/RegisterPage.tsx` and `frontend/src/features/auth/LoginPage.tsx`
- [ ] T099 [P] [US4] Implement profile edit/image lifecycle and account-deletion confirmation pages in `frontend/src/features/profile/ProfileSettingsPage.tsx`
- [ ] T100 [P] [US4] Implement month/day Memories navigation with owner-only interaction display, ended posts that remain read-only for editing and new interactions, and an owner-only delete control that refreshes month/day views after successful deletion in `frontend/src/features/memories/MemoriesPage.tsx`
- [ ] T101 [US4] Wire US4 routes/API clients and purge worker registration in `frontend/src/app/routes.tsx`, `frontend/src/api/account.ts`, and `backend/cmd/worker/main.go`
- [ ] T102 [US4] Run US4 contract, unit, integration, concurrency, pagination, and Playwright suites and record the verified commands/results in `specs/001-isureal-social-app/validation/us4.md`

**Checkpoint**: P4は関係/投稿APIを使わずseed済み履歴で単独検証でき、全アカウントライフサイクルが成立する。

---

## Phase 7: User Story 5 - 地域ごとの日次通知を受ける (Priority: P5)

**Goal**: 管理者が地域ごとの次回通知を設定・変更・取消し、地域Windowとアプリ内通知を一意に開始し、Web Pushの購読・再試行・開封と保留地域変更を扱える。

**Independent Test**: 3地域ユーザー、fake Clock、fake Pushを使い、地域日一回、次Windowまでの継続、対象者だけの通知、部分障害、単一購読、通知開封遷移、地域変更適用を検証する。

### Validation for User Story 5 (write first and verify failure)

- [ ] T103 [P] [US5] Before any US5 implementation, map US5-AS01..US5-AS20 to the US5 validation test files in `specs/001-isureal-social-app/validation/us5-traceability-plan.md`, then write failing OpenAPI contract tests for admin schedule set/change/cancel, notification list/open, and Push subscription replace/delete in `backend/tests/contract/notifications_test.go`; reuse T033's user-facing current-window contract and cover scheduler-driven current-window turnover only in T104-T107 rather than duplicating the endpoint contract here
- [ ] T104 [P] [US5] Write failing domain tests for fixed offsets, one local-day window, two-minute deadline, next-window-only ending, schedule lifecycle, notification destination, and pending-region overwrite/cancel in `backend/internal/domain/notification_policy_test.go`
- [ ] T105 [P] [US5] Write failing integration tests for three-region fanout, registration or region-change into an already-active window allowing one post without retroactive notification, schedule reset, start-before-change conflict, list/read semantics, and regional-day boundaries in `backend/tests/integration/notifications_test.go`
- [ ] T106 [P] [US5] Write failing Push tests for active-subscription replacement/removal, one logical notification ID across successful delivery and initial plus three transient/unknown-result retries, Service Worker duplicate-display suppression, permanent invalidation, app-notification preservation, and stale-subscription suppression in `backend/tests/integration/push_test.go` and `frontend/tests/unit/push-dedup.test.ts`
- [ ] T107 [P] [US5] Write failing concurrency/restart tests for duplicate scheduler claims, one notification per recipient/window, concurrent subscription updates, worker crash recovery, schedule cancel-versus-start, and cross-region isolation proving that a region with backlogged, failing, or retrying notification/Push work cannot share locks, job state, or failure results that prevent another due region from independently committing exactly one window and its notifications in `backend/tests/concurrency/notifications_test.go`
- [ ] T108 [P] [US5] Write failing 51+ notification pagination/open tests for descending order, concurrent inserts, one-time read marking, Push-open linkage, and invalid-session denial in `backend/tests/integration/notification_pagination_test.go`
- [ ] T109 [P] [US5] Write failing Playwright/Service Worker journey for Push opt-in/out, notification center, focus-safe open, and create-post/current-post/Memory destinations in `frontend/tests/e2e/notifications.spec.ts`

### Implementation for User Story 5

- [ ] T110 [P] [US5] Create notification schedule, notification, and Push attempt tables with regional-day, recipient/window, claim, and retry indexes in `backend/migrations/000007_notifications.up.sql`
- [ ] T111 [P] [US5] Define schedule, Notification, PushAttempt, delivery state, open destination, and pending-region domain transitions in `backend/internal/domain/notification.go`
- [ ] T112 [US5] Implement schedule locking, current-window turnover, pending-region application, eligible-user snapshot, notification uniqueness, keyset lists, and account-purge cleanup for notification rows in `backend/internal/repository/postgres/notifications.go`
- [ ] T113 [US5] Implement admin schedule set/change/cancel validation, separately authenticated handlers, and redacted structured logs for every administrator authentication/schedule outcome in `backend/internal/service/schedules.go` and `backend/internal/httpapi/admin_schedules.go`
- [ ] T114 [US5] Implement due-schedule worker transaction that ends the previous window, starts one regional-day window, resets schedule, applies pending regions, and creates non-retroactive notifications in `backend/internal/worker/window_scheduler.go`
- [ ] T115 [US5] Implement Push subscription lifecycle, secret-safe storage and log exclusion, stale-session checks, and user handlers in `backend/internal/service/push_subscriptions.go` and `backend/internal/httpapi/push_subscriptions.go`
- [ ] T116 [US5] Implement the external Push adapter and retry worker with the app notification ID as an invariant logical delivery token across the initial attempt plus three retries, including unknown-result recovery and permanent-failure invalidation, in `backend/internal/push/webpush.go` and `backend/internal/worker/push_delivery.go`
- [ ] T117 [US5] Implement notification keyset list/open/read/destination handlers and pending-region profile update integration in `backend/internal/httpapi/notifications.go` and `backend/internal/service/notifications.go`
- [ ] T118 [P] [US5] Implement Service Worker Push receipt/click handling that requires the current valid session, links the app notification ID, and uses that logical notification ID as a stable notification tag so repeated delivery replaces rather than duplicates the browser display in `frontend/src/service-worker/push.ts`
- [ ] T119 [P] [US5] Implement Push subscription settings, notification center pagination/read state, and destination navigation in `frontend/src/features/notifications/NotificationPage.tsx`
- [ ] T120 [P] [US5] Implement current/pending region controls with overwrite/cancel feedback in `frontend/src/features/profile/RegionSettings.tsx`
- [ ] T121 [US5] Wire Service Worker registration, US5 API clients/routes, and both notification workers in `frontend/src/app/serviceWorker.ts`, `frontend/src/api/notifications.ts`, `frontend/src/app/routes.tsx`, and `backend/cmd/worker/main.go`
- [ ] T122 [US5] Add structured schedule/Push/notification logs while excluding Push subscription endpoints, auth secrets, p256dh keys, VAPID private keys, session or idempotency keys, notification/user text, and exact coordinates in `backend/internal/worker/window_scheduler.go` and `backend/internal/worker/push_delivery.go`
- [ ] T123 [US5] Run US5 contract, unit, integration, concurrency, pagination, Push-failure, and Playwright suites and record the verified commands/results in `specs/001-isureal-social-app/validation/us5.md`

**Checkpoint**: 全5ストーリーを個別および統合状態で検証できる。

---

## Phase 8: Polish & Cross-Cutting Concerns

**Purpose**: ストーリー横断の正しさ、移行、観測可能性、アクセシビリティ、再現性を仕上げる。

- [ ] T124 Perform the initial cross-cutting review of authorization, transaction lock ordering, repository boundaries, idempotency coverage, image-sanitizer isolation, worker retry paths, and shared frontend state in `backend/internal/service/`, `backend/internal/repository/postgres/`, `backend/internal/image/`, and `frontend/src/`; record the initial checklist and findings without claiming final closure in `specs/001-isureal-social-app/validation/architecture-review.md`
- [ ] T125 [P] Add forward-only migration transaction, repeated full reset, clean seed, constraint introspection, and failed-upgrade rollback-safety tests in `backend/tests/integration/migrations_test.go`
- [ ] T126 [P] Add cross-story failure-injection tests for image-sanitizer slot exhaustion/resource limit/crash/timeout, DB rollback after image publication, worker restart, partial or unknown-result Push failure, stale cursor, and expired idempotency records in `backend/tests/integration/failure_recovery_test.go`
- [ ] T127 [P] Add a structured-log audit that exercises user/admin authentication, certification and scheduling, success/input/conflict/unexpected-error paths and rejects passwords, session/idempotency keys, Push endpoints/auth/p256dh/VAPID secrets, content text, images, bios, and exact coordinates in `backend/tests/integration/log_audit_test.go`
- [ ] T128 Add a complete keyboard-only Playwright suite including logout and every other FR-038 primary operation with visible-focus assertions in `frontend/tests/e2e/keyboard-accessibility.spec.ts`
- [ ] T129 [P] Add generated-client drift detection and full OpenAPI request/response conformance to CI in `frontend/scripts/check-openapi.mjs` and `.github/workflows/ci.yml`
- [ ] T130 Implement and test the FR-044/SC-033 browser/HTTP security contract: accept user/admin tokens only from `Authorization: Bearer`, reject cookie/query/form credentials, enforce exact configured `Origin` and `Sec-Fetch-Site: same-origin` when browser headers are present while allowing headerless non-browser clients, return no CORS permission, apply the specified no-store/referrer/nosniff headers to API and authorized-image success/error responses plus the specified SPA CSP and camera Permissions-Policy, keep the user token only in IndexedDB and remove it on logout/session replacement/account deletion without URL/DOM/Cookie/log/error/analytics exposure, preserve multipart/image sanitizer boundaries, and verify that rapid repeated requests receive only normal contract outcomes without 429 or rate-limit headers in `backend/internal/httpapi/security.go`, `backend/internal/image/sanitizer.go`, `frontend/src/app/auth.ts`, `backend/tests/integration/security_test.go`, and `frontend/tests/e2e/security.spec.ts`
- [ ] T131 After T125-T130 are complete, perform the final cross-cutting review against the security-hardened code, re-run every affected check from T124, resolve every remaining correctness or security finding, and update `specs/001-isureal-social-app/validation/architecture-review.md` with final resolutions and test commands/results; do not complete T131 while any finding remains unresolved
- [ ] T132 After T131 is complete, reconcile the five pre-implementation `validation/usn-traceability-plan.md` files with the completed automated tests and build the final matrix covering FR-001..FR-044, SC-001..SC-033, every acceptance scenario as `US1-AS01` through each story's final scenario, and the cross-region functional-isolation edge case in `specs/001-isureal-social-app/validation/traceability.md`; map each requirement, acceptance-scenario key, and required edge case to at least one existing automated test, identify Web-client scenarios that run through Playwright for SC-012, map FR-044/SC-033 to the security integration and Playwright tests from T130, and map non-browser scenarios to tests under `backend/internal/`, `backend/tests/contract/`, `backend/tests/integration/`, or `backend/tests/concurrency/`; if any row is uncovered or differs from its pre-implementation plan, fail this gate and return the gap to task planning and the owning story instead of adding an unplanned test inside T132, and do not complete T132 until the corrected story has restored full coverage
- [ ] T133 After T132 is complete, add or finalize backend unit, contract, integration, concurrency, frontend unit/E2E, migration, image-sanitizer resource-isolation, build, generated-client drift, OpenAPI conformance, and secret-scan jobs with pinned tool versions plus the release-resolved Chrome exact version and immutable test-image digest in `.github/workflows/ci.yml`; require every automated test referenced by the completed T132 traceability matrix to run in the pinned CI suite, execute it against the final security-hardened code, and record commands, versions, and results in `specs/001-isureal-social-app/validation/ci.md`
- [ ] T134 Execute every scenario in `specs/001-isureal-social-app/quickstart.md` from a clean environment and record commands, tool versions, the resolved Chrome version, immutable browser-test image digest, resolution date, and results in `specs/001-isureal-social-app/validation/quickstart-results.md`
- [ ] T135 Audit the complete public artifact inventory—tracked source, generated OpenAPI/client output, frontend production bundle, backend API/worker/image-sanitizer/migrate/reset/seed binaries, container build contexts and resolved images, deployment/configuration examples, migration/seed assets, and documentation—for secrets, personal data, benchmark code, bottleneck registry entries, intended-solution spoilers, and generated version mismatch; record the enumerated inputs, reproducible inspection commands, findings, explicitly justified false positives, and a zero-unresolved-finding pass criterion in `specs/001-isureal-social-app/validation/public-artifact-audit.md`
- [ ] T136 Record the app specification, contract, migration, test-suite, API/worker/image-sanitizer build artifacts, resolved Chrome exact version, immutable browser-test image digest, and browser resolution date for the future competition feature handoff in `specs/001-isureal-social-app/validation/version-manifest.md`
- [ ] T137 Add an explicit release gate stating that benchmark creation, bottleneck introduction, isolated/cumulative calibration, trial solving, and competition distribution require a separate approved feature specification in `specs/001-isureal-social-app/validation/competition-handoff.md`

**Final Checkpoint**: 正しい基準アプリと公開成果物が完成する。競技要素は別featureの「基準実装とテスト → ベンチマーカー → ボトルネック導入 → 単独・累積較正 → 試し解き → 配布監査」の順でのみ開始する。

---

## Dependencies & Execution Order

### Phase dependencies

```text
Phase 1 Setup
  └─> Phase 2 Foundation (全ストーリーをブロック)
        ├─> Phase 3 US1 (P1 / MVP) ──────┬─> Phase 5 US3 (P3) ─> Phase 6 US4 (P4)
        └─> Phase 4 US2 (P2) ────────────┘                         └─> Phase 7 US5 (P5)

選択した全ストーリー ─> Phase 8 Polish ─> 別の競技feature specification
```

### User story dependencies

- **US1 (P1)**: Foundation後に開始可能。Foundationの最小friendship/follow/blockテーブルへfixtureを投入するため、US2の関係操作APIには依存しない。
- **US2 (P2)**: Foundation後にUS1と並行開始可能。関係遷移自体は投稿機能に依存しない。
- **US3 (P3)**: US1のPost、PostingWindow、VisibilityPolicyに依存する。US2の操作APIは不要だが、関係fixtureはFoundationのテーブルを利用する。
- **US4 (P4)**: 完全なMemories検証のためUS1とUS3に依存する。認証基盤はPhase 2でテスト先行実装済みとする。
- **US5 (P5)**: US1のPostingWindowとUS4の地域/アカウントライフサイクルに依存する。
- **Phase 8**: リリース対象の全ストーリー完了後。T124で初回レビューを完了し、T125〜T130の
  横断テスト・契約検証・セキュリティ強化後にT131で最終レビューを完了してから、T132のトレーサビリティを確定する。T133ではその最終状態へ
  完全CIを実行・記録し、T135〜T137を競技featureへ進む前の監査・版固定・情報分離ゲートとする。

### Within each user story

1. Validation tasksを作成し、実装前に失敗することを確認する。
2. Migration/domain typesを作成する。
3. Repository、service、HTTP handlerの順で実装する。
4. Frontendを実装し、契約生成型だけを利用する。
5. 全テストを通し、ストーリー別validation記録を残す。

## Parallel Opportunities

- Phase 1のT003〜T008はT001/T002と競合しないファイルを中心に並行可能。
- Phase 2ではClock/ID、HTTP/logging、cursor、テスト基盤、SPA shellを依存成立後に並行可能。
- Foundation完了後、US1とUS2は並行可能。US3はUS1後、US4はUS1/US3後、US5はUS1/US4後に開始する。
- 同じストーリー内でも契約、domain、integration、concurrency、pagination、E2Eのテストファイルは並行作成可能。
- migration番号、共有router、`frontend/src/app/routes.tsx`、`backend/cmd/worker/main.go`の変更はストーリー統合時に直列マージする。

## Parallel Example: User Story 1

```text
Task T033: backend/tests/contract/posts_test.go
Task T034: backend/internal/domain/post_policy_test.go
Task T035: backend/tests/integration/posts_test.go
Task T036: backend/tests/concurrency/posts_test.go
Task T037: backend/tests/integration/feeds_test.go
Task T038: frontend/tests/e2e/post-and-feed.spec.ts
```

## Parallel Example: User Story 2

```text
Task T051: backend/tests/contract/relationships_test.go
Task T052: backend/internal/domain/relationship_policy_test.go
Task T053: backend/tests/integration/relationships_test.go
Task T054: backend/tests/concurrency/relationships_test.go
Task T055: backend/tests/integration/user_search_test.go
Task T056: frontend/tests/e2e/relationships.spec.ts
```

## Parallel Example: User Story 3

```text
Task T069: backend/tests/contract/interactions_test.go
Task T070: backend/internal/domain/interaction_policy_test.go
Task T071: backend/tests/integration/interactions_test.go
Task T072: backend/tests/concurrency/interactions_test.go
Task T073: backend/tests/integration/interaction_pagination_test.go
Task T074: frontend/tests/e2e/interactions.spec.ts
```

## Parallel Example: User Story 4

```text
Task T084: backend/tests/contract/account_test.go
Task T085: backend/internal/domain/account_lifecycle_test.go
Task T086: backend/tests/integration/profile_test.go
Task T087: backend/tests/integration/memories_test.go
Task T088: backend/tests/integration/account_deletion_test.go
Task T089: backend/tests/concurrency/account_deletion_test.go
Task T090: frontend/tests/e2e/account-and-memories.spec.ts
```

## Parallel Example: User Story 5

```text
Task T103: backend/tests/contract/notifications_test.go
Task T104: backend/internal/domain/notification_policy_test.go
Task T105: backend/tests/integration/notifications_test.go
Task T106: backend/tests/integration/push_test.go
Task T107: backend/tests/concurrency/notifications_test.go
Task T108: backend/tests/integration/notification_pagination_test.go
Task T109: frontend/tests/e2e/notifications.spec.ts
```

## Implementation Strategy

### MVP first

1. Phase 1 Setupを完了する。
2. Phase 2 Foundationを完了し、全共有テストを通す。
3. Phase 3 US1を完了する。
4. US1のvalidation記録とQuickstart 1〜7を確認して停止・レビューする。
5. 投稿、自己投稿、4フィード、時間内外、公開範囲、削除/再投稿をMVPとしてデモする。

### Incremental delivery

1. Setup + Foundation → 認証・DB・画像・冪等性・契約検証が利用可能。
2. US1 → 日次投稿とフィードのMVP（US2は並行可能）。
3. US2 → 実際の友達/follow/blockグラフ。
4. US3 → US1上の投稿交流。
5. US4 → US1/US3上の完全なアカウント/プロフィール/Memoriesライフサイクル。
6. US5 → US1/US4上の地域スケジュールと通知/Push。
7. Polish → 横断的な正しさ、移行、アクセシビリティ、公開成果物監査。

### Competition handoff

本タスク群のT124〜T137まで完了した基準実装の版を固定した後、別featureで以下を順序どおり計画する。

1. ベンチマーカーと再現可能な負荷/採点/初期化
2. 非公開課題台帳と独立したボトルネック挿入点
3. 初期、単独解消、累積解消、想定上位解の反復較正
4. 競技者同等条件の試し解き
5. 長時間負荷、秘密情報、スポイラー、再現ビルドを含む配布物監査

## Notes

- `[P]`は依存がないという意味ではなく、直前フェーズと明示された前提完了後に同時作業できることを示す。
- 各テストタスクは実装前に失敗することを確認し、対応実装後も初期実装と将来の最適化へ同じ判定を適用する。
- OpenAPI、データ不変条件、固定時刻、認可、エラー形式を性能上の都合で変更しない。
- タスク完了時はチェックボックスを`[x]`へ更新し、ストーリー別validation記録へ実行コマンドと結果を残す。
