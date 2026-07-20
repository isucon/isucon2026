.PHONY: fmt vet build test frontend-install frontend-lint frontend-typecheck frontend-test frontend-e2e contract generate-api check

fmt:
	gofmt -w $$(find backend -name '*.go' -type f)

vet:
	go -C backend vet ./...

build:
	go -C backend build ./cmd/api ./cmd/worker ./cmd/image-sanitizer
	npm --prefix frontend run build

test:
	go -C backend test ./...
	npm --prefix frontend test

frontend-install:
	npm --prefix frontend ci

frontend-lint:
	npm --prefix frontend run lint

frontend-typecheck:
	npm --prefix frontend run typecheck

frontend-test:
	npm --prefix frontend test

frontend-e2e:
	npm --prefix frontend run test:e2e

contract:
	npm --prefix frontend run test:contract

generate-api:
	npm --prefix frontend run generate:api

check: vet frontend-lint frontend-typecheck contract test build
