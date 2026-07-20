# 最終トレーサビリティマトリクス

検証日: 2026-07-19 (UTC)

5件の事前計画を完成済みテストと照合した。US4-AS22の事前割当漏れだけを所有ストーリーUS4へ戻して補正し、`validation/us4.md`へ記録した。以下は補正後の全coverageであり、未被覆・未解決差分はない。

## 機能要件

| 要件 | 自動テスト |
|---|---|
| FR-001 | `backend/tests/integration/auth_test.go`, `backend/tests/integration/security_test.go` |
| FR-002 | `backend/internal/domain/notification_policy_test.go`, `backend/tests/integration/profile_test.go` |
| FR-003 | `backend/tests/integration/relationships_test.go`, `backend/tests/contract/notifications_test.go` |
| FR-004 | `backend/internal/domain/post_policy_test.go`, `backend/tests/integration/posts_test.go` |
| FR-005 | `backend/tests/concurrency/posts_test.go` |
| FR-006 | `backend/tests/integration/posts_test.go`, `backend/internal/image/store_test.go` |
| FR-007 | `backend/tests/contract/posts_test.go`, `backend/tests/integration/posts_test.go` |
| FR-008 | `backend/tests/integration/relationships_test.go` |
| FR-009 | `backend/tests/integration/relationships_test.go` |
| FR-010 | `backend/tests/integration/relationships_test.go` |
| FR-011 | `backend/tests/concurrency/relationships_test.go` |
| FR-012 | `backend/tests/integration/feeds_test.go` |
| FR-013 | `backend/internal/domain/post_policy_test.go`, `frontend/tests/e2e/post-and-feed.spec.ts` |
| FR-014 | `backend/tests/integration/feeds_test.go` |
| FR-015 | `backend/tests/integration/feeds_test.go` |
| FR-016 | `backend/tests/integration/notifications_test.go`, `backend/tests/integration/posts_test.go` |
| FR-017 | `backend/tests/integration/interactions_test.go`, `frontend/tests/e2e/interactions.spec.ts` |
| FR-018 | `backend/tests/integration/interactions_test.go` |
| FR-019 | `backend/tests/integration/interactions_test.go` |
| FR-020 | `backend/internal/domain/interaction_policy_test.go`, `backend/tests/integration/interactions_test.go` |
| FR-021 | `backend/tests/integration/memories_test.go` |
| FR-022 | `backend/tests/integration/posts_test.go`, `backend/tests/integration/memories_test.go` |
| FR-023 | `backend/tests/integration/profile_test.go` |
| FR-024 | `backend/internal/domain/post_policy_test.go`, `backend/tests/integration/posts_test.go` |
| FR-025 | `backend/tests/integration/user_search_test.go`, `backend/tests/integration/relationships_test.go` |
| FR-026 | `backend/tests/integration/idempotency_test.go` |
| FR-027 | `backend/tests/contract/harness_test.go`, `backend/tests/integration/failure_recovery_test.go` |
| FR-028 | `backend/tests/integration/interactions_test.go` |
| FR-029 | `backend/tests/concurrency/relationships_test.go` |
| FR-030 | `backend/tests/integration/auth_test.go`, `backend/internal/domain/validation_test.go` |
| FR-031 | `backend/tests/integration/account_deletion_test.go`, `backend/tests/concurrency/account_deletion_test.go` |
| FR-032 | `backend/tests/integration/auth_test.go`, `backend/tests/integration/account_deletion_test.go` |
| FR-033 | `frontend/scripts/verify-browser.mjs`, `frontend/tests/e2e/account-and-memories.spec.ts` |
| FR-034 | `backend/tests/integration/feeds_test.go` |
| FR-035 | `backend/tests/integration/notification_pagination_test.go` |
| FR-036 | `backend/internal/image/store_test.go`, `backend/tests/integration/image_sanitizer_test.go` |
| FR-037 | `backend/internal/domain/validation_test.go`, `backend/tests/integration/profile_test.go` |
| FR-038 | `frontend/tests/e2e/keyboard-accessibility.spec.ts` |
| FR-039 | `backend/internal/image/store_test.go`, `backend/tests/integration/image_sanitizer_test.go` |
| FR-040 | `backend/internal/domain/relationship_policy_test.go`, `backend/tests/concurrency/relationships_test.go` |
| FR-041 | `backend/tests/integration/notifications_test.go`, `backend/tests/integration/posts_test.go` |
| FR-042 | `backend/tests/integration/notifications_test.go` |
| FR-043 | `backend/tests/integration/push_test.go`, `backend/tests/concurrency/notifications_test.go` |
| FR-044 | `backend/tests/integration/security_test.go`, `frontend/tests/e2e/security.spec.ts` |

## 成功基準

| 基準 | 自動テスト |
|---|---|
| SC-001 | `backend/tests/contract/`, `backend/tests/integration/`, `backend/tests/concurrency/` |
| SC-002 | `backend/tests/integration/posts_test.go` |
| SC-003 | `backend/tests/integration/feeds_test.go` |
| SC-004 | `backend/tests/integration/notifications_test.go`, `backend/tests/concurrency/notifications_test.go` |
| SC-005 | `backend/tests/integration/relationships_test.go` |
| SC-006 | `backend/tests/concurrency/posts_test.go` |
| SC-007 | `backend/tests/integration/idempotency_test.go` |
| SC-008 | `backend/tests/integration/auth_test.go` |
| SC-009 | `backend/tests/integration/account_deletion_test.go`, `backend/tests/concurrency/account_deletion_test.go` |
| SC-010 | `backend/tests/integration/profile_test.go`, `backend/tests/integration/user_search_test.go` |
| SC-011 | `backend/tests/integration/log_audit_test.go` |
| SC-012 | `frontend/tests/e2e/post-and-feed.spec.ts`, `frontend/tests/e2e/relationships.spec.ts`, `frontend/tests/e2e/interactions.spec.ts`, `frontend/tests/e2e/account-and-memories.spec.ts`, `frontend/tests/e2e/notifications.spec.ts`, `frontend/tests/e2e/keyboard-accessibility.spec.ts`, `frontend/tests/e2e/security.spec.ts` |
| SC-013 | `backend/tests/integration/memories_test.go`, `backend/internal/domain/notification_policy_test.go` |
| SC-014 | `backend/tests/integration/feeds_test.go` |
| SC-015 | `backend/tests/concurrency/login_test.go` |
| SC-016 | `backend/tests/integration/push_test.go` |
| SC-017 | `backend/tests/integration/user_search_test.go` |
| SC-018 | `backend/tests/integration/interaction_pagination_test.go` |
| SC-019 | `backend/tests/concurrency/relationships_test.go` |
| SC-020 | `backend/tests/integration/notification_pagination_test.go` |
| SC-021 | `backend/tests/integration/interaction_pagination_test.go` |
| SC-022 | `backend/internal/domain/post_policy_test.go` |
| SC-023 | `backend/internal/image/store_test.go`, `backend/tests/integration/profile_test.go`, `backend/tests/integration/interactions_test.go` |
| SC-024 | `backend/internal/domain/validation_test.go`, `backend/internal/domain/post_policy_test.go` |
| SC-025 | `backend/tests/integration/posts_test.go` |
| SC-026 | `backend/tests/integration/notifications_test.go`, `backend/tests/integration/profile_test.go` |
| SC-027 | `frontend/tests/e2e/keyboard-accessibility.spec.ts` |
| SC-028 | `backend/tests/integration/auth_test.go`, `backend/internal/domain/validation_test.go` |
| SC-029 | `backend/internal/image/store_test.go`, `backend/tests/integration/image_sanitizer_test.go` |
| SC-030 | `backend/tests/integration/notifications_test.go` |
| SC-031 | `backend/tests/integration/notifications_test.go` |
| SC-032 | `backend/tests/integration/push_test.go`, `backend/tests/concurrency/notifications_test.go` |
| SC-033 | `backend/tests/integration/security_test.go`, `frontend/tests/e2e/security.spec.ts` |

## 受入シナリオ

| シナリオ | 自動テスト |
|---|---|
| US1-AS01 | `backend/internal/domain/post_policy_test.go`, `backend/tests/integration/posts_test.go`, `frontend/tests/e2e/post-and-feed.spec.ts` |
| US1-AS02 | `backend/internal/domain/post_policy_test.go`, `backend/tests/integration/posts_test.go` |
| US1-AS03 | `backend/tests/integration/posts_test.go`, `frontend/tests/e2e/post-and-feed.spec.ts` |
| US1-AS04 | `backend/tests/integration/posts_test.go` |
| US1-AS05 | `backend/internal/domain/post_policy_test.go`, `backend/tests/integration/feeds_test.go`, `frontend/tests/e2e/post-and-feed.spec.ts` |
| US1-AS06 | `backend/internal/domain/post_policy_test.go`, `backend/tests/integration/posts_test.go` |
| US1-AS07 | `backend/tests/integration/posts_test.go`, `backend/internal/image/store_test.go` |
| US1-AS08 | `backend/tests/integration/posts_test.go` |
| US1-AS09 | `backend/internal/domain/post_policy_test.go`, `backend/tests/integration/posts_test.go` |
| US1-AS10 | `backend/tests/integration/feeds_test.go` |
| US1-AS11 | `backend/tests/integration/posts_test.go` |
| US1-AS12 | `backend/tests/integration/posts_test.go` |
| US1-AS13 | `frontend/tests/e2e/post-and-feed.spec.ts` |
| US1-AS14 | `backend/tests/integration/posts_test.go` |
| US1-AS15 | `backend/tests/integration/posts_test.go`, `backend/internal/image/store_test.go` |
| US1-AS16 | `backend/internal/domain/post_policy_test.go`, `backend/tests/integration/feeds_test.go` |
| US1-AS17 | `backend/tests/integration/posts_test.go`, `frontend/tests/e2e/post-and-feed.spec.ts` |
| US2-AS01 | `backend/tests/integration/relationships_test.go` |
| US2-AS02 | `backend/tests/integration/relationships_test.go` |
| US2-AS03 | `backend/tests/integration/relationships_test.go` |
| US2-AS04 | `backend/tests/integration/relationships_test.go` |
| US2-AS05 | `backend/tests/integration/relationships_test.go` |
| US2-AS06 | `backend/tests/integration/relationships_test.go` |
| US2-AS07 | `backend/tests/integration/relationships_test.go` |
| US2-AS08 | `backend/tests/integration/relationships_test.go` |
| US2-AS09 | `backend/tests/integration/user_search_test.go` |
| US2-AS10 | `backend/tests/concurrency/relationships_test.go`, `backend/internal/domain/relationship_policy_test.go` |
| US2-AS11 | `backend/tests/integration/user_search_test.go` |
| US2-AS12 | `backend/tests/concurrency/relationships_test.go`, `backend/internal/domain/relationship_policy_test.go` |
| US2-AS13 | `backend/tests/integration/user_search_test.go` |
| US2-AS14 | `backend/tests/concurrency/relationships_test.go`, `backend/internal/domain/relationship_policy_test.go` |
| US2-AS15 | `backend/tests/concurrency/relationships_test.go`, `backend/internal/domain/relationship_policy_test.go` |
| US2-AS16 | `backend/tests/concurrency/relationships_test.go`, `backend/internal/domain/relationship_policy_test.go` |
| US2-AS17 | `backend/tests/concurrency/relationships_test.go`, `backend/internal/domain/relationship_policy_test.go` |
| US2-AS18 | `backend/tests/integration/user_search_test.go` |
| US2-AS19 | `backend/tests/concurrency/relationships_test.go`, `backend/internal/domain/relationship_policy_test.go` |
| US2-AS20 | `backend/tests/integration/user_search_test.go` |
| US2-AS21 | `backend/tests/concurrency/relationships_test.go`, `backend/internal/domain/relationship_policy_test.go` |
| US2-AS22 | `backend/tests/integration/user_search_test.go` |
| US2-AS23 | `backend/tests/concurrency/relationships_test.go`, `backend/internal/domain/relationship_policy_test.go` |
| US2-AS24 | `backend/tests/concurrency/relationships_test.go`, `backend/internal/domain/relationship_policy_test.go` |
| US2-AS25 | `backend/tests/integration/user_search_test.go` |
| US3-AS01 | `backend/tests/integration/interactions_test.go` |
| US3-AS02 | `backend/tests/integration/interactions_test.go` |
| US3-AS03 | `backend/tests/integration/interactions_test.go` |
| US3-AS04 | `backend/tests/integration/interactions_test.go` |
| US3-AS05 | `backend/tests/integration/interactions_test.go` |
| US3-AS06 | `backend/tests/integration/interactions_test.go` |
| US3-AS07 | `backend/tests/integration/interactions_test.go` |
| US3-AS08 | `backend/tests/integration/interactions_test.go` |
| US3-AS09 | `backend/tests/integration/interactions_test.go` |
| US3-AS10 | `backend/tests/integration/interactions_test.go` |
| US3-AS11 | `backend/tests/integration/interactions_test.go` |
| US3-AS12 | `backend/tests/integration/interaction_pagination_test.go` |
| US3-AS13 | `backend/tests/integration/interaction_pagination_test.go` |
| US3-AS14 | `backend/tests/integration/interactions_test.go` |
| US3-AS15 | `frontend/tests/e2e/interactions.spec.ts` |
| US4-AS01 | `backend/tests/integration/auth_test.go`, `backend/tests/contract/account_test.go`, `backend/tests/integration/account_deletion_test.go` |
| US4-AS02 | `backend/tests/integration/auth_test.go`, `backend/tests/contract/account_test.go`, `backend/tests/integration/account_deletion_test.go` |
| US4-AS03 | `backend/tests/integration/auth_test.go`, `backend/tests/contract/account_test.go`, `backend/tests/integration/account_deletion_test.go` |
| US4-AS04 | `backend/tests/integration/auth_test.go`, `backend/tests/contract/account_test.go`, `backend/tests/integration/account_deletion_test.go` |
| US4-AS05 | `backend/tests/integration/memories_test.go` |
| US4-AS06 | `backend/tests/integration/memories_test.go` |
| US4-AS07 | `backend/tests/integration/memories_test.go` |
| US4-AS08 | `backend/tests/integration/memories_test.go` |
| US4-AS09 | `backend/tests/integration/memories_test.go` |
| US4-AS10 | `backend/tests/integration/memories_test.go` |
| US4-AS11 | `backend/tests/integration/profile_test.go` |
| US4-AS12 | `backend/tests/integration/account_deletion_test.go`, `backend/tests/concurrency/account_deletion_test.go` |
| US4-AS13 | `backend/tests/integration/account_deletion_test.go`, `backend/tests/concurrency/account_deletion_test.go` |
| US4-AS14 | `backend/tests/integration/account_deletion_test.go`, `backend/tests/concurrency/account_deletion_test.go` |
| US4-AS15 | `backend/tests/integration/profile_test.go` |
| US4-AS16 | `backend/tests/integration/profile_test.go` |
| US4-AS17 | `backend/tests/integration/memories_test.go` |
| US4-AS18 | `backend/tests/integration/account_deletion_test.go`, `backend/tests/concurrency/account_deletion_test.go` |
| US4-AS19 | `backend/tests/integration/auth_test.go`, `backend/tests/contract/account_test.go`, `backend/tests/integration/account_deletion_test.go` |
| US4-AS20 | `backend/tests/integration/memories_test.go` |
| US4-AS21 | `backend/tests/integration/auth_test.go`, `backend/tests/contract/account_test.go`, `backend/tests/integration/account_deletion_test.go` |
| US4-AS22 | `backend/internal/domain/post_policy_test.go`（US4補正） |
| US4-AS23 | `backend/tests/integration/auth_test.go`, `backend/tests/contract/account_test.go`, `backend/tests/integration/account_deletion_test.go` |
| US4-AS24 | `backend/tests/integration/auth_test.go`, `backend/tests/contract/account_test.go`, `backend/tests/integration/account_deletion_test.go` |
| US4-AS25 | `backend/tests/integration/profile_test.go` |
| US4-AS26 | `backend/tests/integration/auth_test.go`, `backend/tests/contract/account_test.go`, `backend/tests/integration/account_deletion_test.go` |
| US4-AS27 | `backend/tests/integration/auth_test.go`, `backend/tests/contract/account_test.go`, `backend/tests/integration/account_deletion_test.go` |
| US4-AS28 | `backend/tests/integration/auth_test.go`, `backend/tests/contract/account_test.go`, `backend/tests/integration/account_deletion_test.go` |
| US4-AS29 | `backend/tests/integration/auth_test.go`, `backend/tests/contract/account_test.go`, `backend/tests/integration/account_deletion_test.go` |
| US4-AS30 | `backend/tests/integration/auth_test.go`, `backend/tests/contract/account_test.go`, `backend/tests/integration/account_deletion_test.go` |
| US4-AS31 | `backend/tests/integration/profile_test.go` |
| US4-AS32 | `backend/tests/integration/auth_test.go`, `backend/tests/contract/account_test.go`, `backend/tests/integration/account_deletion_test.go` |
| US4-AS33 | `backend/tests/integration/auth_test.go`, `backend/tests/contract/account_test.go`, `backend/tests/integration/account_deletion_test.go` |
| US4-AS34 | `backend/tests/integration/auth_test.go`, `backend/tests/contract/account_test.go`, `backend/tests/integration/account_deletion_test.go` |
| US4-AS35 | `backend/tests/integration/auth_test.go`, `backend/tests/contract/account_test.go`, `backend/tests/integration/account_deletion_test.go` |
| US5-AS01 | `backend/internal/domain/notification_policy_test.go`, `backend/tests/integration/notifications_test.go`, `backend/tests/concurrency/notifications_test.go` |
| US5-AS02 | `backend/internal/domain/notification_policy_test.go`, `backend/tests/integration/notifications_test.go`, `backend/tests/concurrency/notifications_test.go` |
| US5-AS03 | `backend/internal/domain/notification_policy_test.go`, `backend/tests/integration/notifications_test.go`, `backend/tests/concurrency/notifications_test.go` |
| US5-AS04 | `backend/internal/domain/notification_policy_test.go`, `backend/tests/integration/notifications_test.go`, `backend/tests/concurrency/notifications_test.go` |
| US5-AS05 | `backend/internal/domain/notification_policy_test.go`, `backend/tests/integration/notifications_test.go`, `backend/tests/concurrency/notifications_test.go` |
| US5-AS06 | `backend/tests/integration/push_test.go`, `backend/tests/concurrency/notifications_test.go` |
| US5-AS07 | `backend/tests/integration/push_test.go`, `backend/tests/concurrency/notifications_test.go` |
| US5-AS08 | `backend/tests/integration/push_test.go`, `backend/tests/concurrency/notifications_test.go` |
| US5-AS09 | `backend/tests/integration/push_test.go`, `backend/tests/concurrency/notifications_test.go` |
| US5-AS10 | `backend/tests/integration/push_test.go`, `backend/tests/concurrency/notifications_test.go` |
| US5-AS11 | `backend/internal/domain/notification_policy_test.go`, `backend/tests/integration/notifications_test.go`, `backend/tests/concurrency/notifications_test.go` |
| US5-AS12 | `backend/internal/domain/notification_policy_test.go`, `backend/tests/integration/notifications_test.go`, `backend/tests/concurrency/notifications_test.go` |
| US5-AS13 | `backend/internal/domain/notification_policy_test.go`, `backend/tests/integration/notifications_test.go`, `backend/tests/concurrency/notifications_test.go` |
| US5-AS14 | `backend/internal/domain/notification_policy_test.go`, `backend/tests/integration/notifications_test.go`, `backend/tests/concurrency/notifications_test.go` |
| US5-AS15 | `backend/internal/domain/notification_policy_test.go`, `backend/tests/integration/notifications_test.go`, `backend/tests/concurrency/notifications_test.go` |
| US5-AS16 | `backend/tests/integration/notification_pagination_test.go`, `frontend/tests/e2e/notifications.spec.ts`, `frontend/tests/unit/push-dedup.test.ts` |
| US5-AS17 | `backend/internal/domain/notification_policy_test.go`, `backend/tests/integration/notifications_test.go`, `backend/tests/concurrency/notifications_test.go` |
| US5-AS18 | `backend/internal/domain/notification_policy_test.go`, `backend/tests/integration/notifications_test.go`, `backend/tests/concurrency/notifications_test.go` |
| US5-AS19 | `backend/tests/integration/notification_pagination_test.go`, `frontend/tests/e2e/notifications.spec.ts`, `frontend/tests/unit/push-dedup.test.ts` |
| US5-AS20 | `backend/tests/integration/notification_pagination_test.go`, `frontend/tests/e2e/notifications.spec.ts`, `frontend/tests/unit/push-dedup.test.ts` |

## 必須edge case

| edge | 自動テスト |
|---|---|
| 地域間機能分離（backlog/failure/retry中の地域が別のdue地域の一意commitを妨げない） | `backend/tests/concurrency/notifications_test.go::TestUS5LockedRegionDoesNotBlockAnotherRegion` |

SC-012のWebクライアント経路は上記7本のPlaywright suite、FR-044/SC-033はbackend security integrationとPlaywright security suiteで検証する。その他の行は`backend/internal/`、`backend/tests/contract/`、`backend/tests/integration/`、`backend/tests/concurrency/`の非browserテストへ割り当てた。
