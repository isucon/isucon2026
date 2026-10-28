module github.com/isucon/isucon2026/router

go 1.26.5

require (
	connectrpc.com/connect v1.20.0
	github.com/isucon/isucon2026/gen/go v0.0.0
	github.com/zeebo/xxh3 v1.0.2
)

require (
	github.com/klauspost/cpuid/v2 v2.0.9 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/isucon/isucon2026/gen/go => ../gen/go
