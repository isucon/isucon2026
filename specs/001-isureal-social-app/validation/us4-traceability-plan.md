# US4 事前トレーサビリティ計画

| 受入キー | 検証 |
|---|---|
| US4-AS01..AS04, AS19, AS21, AS23..AS24, AS26..AS30, AS32..AS35 | 認証domain/contractおよび `account_deletion_test.go` |
| US4-AS11, AS15..AS16, AS25, AS31 | `profile_test.go` |
| US4-AS05..AS10, AS17, AS20 | `memories_test.go` |
| US4-AS12..AS14, AS18, AS35 | `account_deletion_test.go` と concurrency test |
| Web操作 | `account-and-memories.spec.ts` |
