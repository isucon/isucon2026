module github.com/isucon/isucon2026/bench

go 1.26.5

require (
	github.com/isucon/isucon2026/dataset v0.0.0-00010101000000-000000000000
	github.com/isucon/isucon2026/portal v0.0.0
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/goccy/go-json v0.11.2
	github.com/zeebo/xxh3 v1.0.2
)

require github.com/klauspost/cpuid/v2 v2.0.9 // indirect

replace (
	github.com/isucon/isucon2026/dataset => ../dataset
	github.com/isucon/isucon2026/portal => ../portal
)
