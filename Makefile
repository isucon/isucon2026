.PHONY: tools fmt vet build test frontend-install frontend-lint frontend-typecheck frontend-test frontend-e2e contract generate-api proto-lint proto-generate proto-build portal-fmt portal-vet portal-test portal-build portal-web-install portal-web-lint portal-web-typecheck portal-web-test portal-web-build portal-web-e2e portal-check router-fmt router-vet router-test router-build router-compose-check router-envoy-check router-e2e router-check provisioning-static provisioning-artifacts vm-preflight vm-up vm-provision vm-validate vm-status vm-logs vm-tls-ca vm-reset-data vm-destroy image-validate image-build-base image-build-role image-promote aws-image-validate aws-image-build-role aws-infra-install aws-infra-check aws-infra-preview aws-infra-up aws-infra-validate aws-infra-destroy check

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

router-fmt:
	gofmt -w $$(find router -name '*.go' -type f)

router-vet:
	go -C router vet ./...

router-test:
	go -C router test ./...

router-build:
	go -C router build -trimpath -o /tmp/isureal-router ./cmd/router

router-compose-check:
	docker compose -f router/compose.e2e.yaml config --quiet

router-envoy-check:
	docker run --rm -v "$$PWD/router/envoy/bootstrap.yaml:/etc/envoy/envoy.yaml:ro" envoyproxy/envoy:v1.32.2 envoy --mode validate -c /etc/envoy/envoy.yaml

router-e2e:
	router/tests/e2e/run.sh

router-check: router-vet router-test router-build router-compose-check router-envoy-check

provisioning-static:
	provisioning/tests/static/run.sh

provisioning-artifacts:
	provisioning/scripts/build-artifacts.sh

vm-preflight:
	provisioning/scripts/vm.sh preflight

vm-up:
	provisioning/scripts/vm.sh up

vm-provision:
	provisioning/scripts/vm.sh provision

vm-validate:
	provisioning/scripts/vm.sh validate

vm-status:
	provisioning/scripts/vm.sh status

vm-logs:
	provisioning/scripts/vm.sh logs

vm-tls-ca:
	provisioning/scripts/tls.sh

vm-reset-data:
	provisioning/scripts/vm.sh reset-data

vm-destroy:
	provisioning/scripts/vm.sh destroy

image-validate:
	provisioning/scripts/image.sh validate

image-build-base:
	provisioning/scripts/image.sh build-base "$(or $(ARCH),amd64)"

image-build-role:
	provisioning/scripts/image.sh build-role "$(ROLE)" "$(or $(ARCH),amd64)"

image-promote:
	provisioning/scripts/image.sh promote "$(ROLE)" "$(or $(ARCH),amd64)" "$(ARTIFACT_ID)"

aws-image-validate:
	provisioning/scripts/aws-image.sh validate

aws-image-build-role:
	provisioning/scripts/aws-image.sh build-role "$(ROLE)"

aws-infra-install:
	npm --prefix provisioning/aws ci

aws-infra-check:
	npm --prefix provisioning/aws run typecheck
	npm --prefix provisioning/aws test

aws-infra-preview:
	cd provisioning/aws && pulumi preview --stack "$(or $(STACK),dev)"

aws-infra-up:
	cd provisioning/aws && pulumi up --stack "$(or $(STACK),dev)"

aws-infra-validate:
	STACK="$(or $(STACK),dev)" provisioning/scripts/aws-validate.sh

aws-infra-destroy:
	cd provisioning/aws && pulumi destroy --stack "$(or $(STACK),dev)"

check: proto-lint vet frontend-lint frontend-typecheck contract test build
