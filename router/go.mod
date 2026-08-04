module github.com/isucon/isucon2026/router

go 1.26.5

require (
	connectrpc.com/connect v1.20.0
	github.com/envoyproxy/go-control-plane v0.13.3
	github.com/envoyproxy/go-control-plane/envoy v1.32.2
	github.com/isucon/isucon2026/gen/go v0.0.0
	google.golang.org/genproto/googleapis/rpc v0.0.0-20241015192408-796eee8c2d53
	google.golang.org/grpc v1.69.4
	google.golang.org/protobuf v1.36.11
)

require (
	cel.dev/expr v0.16.2 // indirect
	github.com/cncf/xds/go v0.0.0-20240905190251-b4127c9b8d78 // indirect
	github.com/envoyproxy/go-control-plane/ratelimit v0.1.0 // indirect
	github.com/envoyproxy/protoc-gen-validate v1.1.0 // indirect
	github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10 // indirect
	golang.org/x/net v0.30.0 // indirect
	golang.org/x/sys v0.26.0 // indirect
	golang.org/x/text v0.19.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20241015192408-796eee8c2d53 // indirect
)

replace github.com/isucon/isucon2026/gen/go => ../gen/go
