.PHONY: all generate dump test vet fmt

all:
	go run . all

generate:
	go run . generate

dump:
	go run . dump

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w $$(find . -name '*.go' -type f)
