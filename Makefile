.PHONY: tools fmt vet build test frontend-install frontend-lint frontend-typecheck frontend-test frontend-e2e contract generate-api proto-lint proto-generate proto-build portal-fmt portal-vet portal-test portal-build portal-web-install portal-web-lint portal-web-typecheck portal-web-test portal-web-build portal-web-e2e portal-check check

tools:
	mise install

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

proto-lint:
	mise exec -- buf lint

proto-generate:
	mise exec -- buf generate
	go -C gen/go mod tidy
	npm --prefix gen/ts install --package-lock-only --ignore-scripts

proto-build:
	mise exec -- buf build -o /tmp/isucon2026-proto.binpb

portal-fmt:
	gofmt -w $$(find portal-mock/server -name '*.go' -type f)

portal-vet:
	go -C portal-mock/server vet ./...

portal-test:
	go -C portal-mock/server test ./...

portal-build:
	go -C portal-mock/server build ./cmd/portal

portal-web-install:
	npm --prefix portal-mock/web ci

portal-web-lint:
	npm --prefix portal-mock/web run lint

portal-web-typecheck:
	npm --prefix portal-mock/web run typecheck

portal-web-test:
	npm --prefix portal-mock/web test

portal-web-build:
	npm --prefix portal-mock/web run build

portal-web-e2e:
	npm --prefix portal-mock/web run test:e2e

portal-check: proto-lint proto-generate portal-vet portal-test portal-build
	npm --prefix gen/ts ci --ignore-scripts
	npm --prefix portal-mock/web ci
	npm --prefix portal-mock/web run lint
	npm --prefix portal-mock/web run typecheck
	npm --prefix portal-mock/web test
	npm --prefix portal-mock/web run build

check: proto-lint vet frontend-lint frontend-typecheck contract test build
