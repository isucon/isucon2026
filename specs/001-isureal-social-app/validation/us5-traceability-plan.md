# US5 事前トレーサビリティ計画

| 受入キー | 検証 |
|---|---|
| US5-AS01..AS05, AS11..AS15, AS17..AS18 | notification domain/integration/concurrency tests |
| US5-AS06..AS10 | `push_test.go`、`notifications_test.go`、worker concurrency tests |
| US5-AS16, AS19..AS20 | notification pagination/open tests と Playwright |
| API形状 | `notifications_test.go` (contract) |
