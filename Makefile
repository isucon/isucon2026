.PHONY: tools fmt vet build test frontend-install frontend-lint frontend-typecheck frontend-test frontend-e2e contract generate-api proto-lint proto-generate proto-build portal-fmt portal-vet portal-test portal-build portal-web-install portal-web-lint portal-web-typecheck portal-web-test portal-web-build portal-web-e2e portal-check router-fmt router-vet router-test router-build router-compose-check router-envoy-check router-e2e router-check ami aws-infra-install aws-infra-check aws-infra-preview aws-infra-up aws-infra-destroy aws-benchmark datagen datagen-test datagen-load check

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

datagen:
	cd webapp/datagen && go run .

datagen-test:
	cd dataset && go test ./...
	cd webapp/datagen && go test ./...

# Docker Compose の DB に、ファイルを経ずに初期データを入れ直す（SCALE=large で本番の規模）。
# bench は同じ -scale で実行する。
SCALE ?= small
DB_PSQL = docker compose exec -T db-asia psql -U isureal -d isureal -v ON_ERROR_STOP=1 -q
datagen-load:
	$(DB_PSQL) < webapp/sql/001-schema.sql
	cd webapp/datagen && go run . -scale $(SCALE) -out - | $(DB_PSQL)
	$(DB_PSQL) < webapp/sql/003-initial-snapshot.sql

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

AWS_SDK_ENV = AWS_CONFIG_FILE=$(CURDIR)/provisioning/aws-sdk.config AWS_PROFILE=default AWS_SDK_LOAD_CONFIG=1

ami:
	cd provisioning/ansible && ansible-playbook prepare.yml
	packer init provisioning/packer
	$(AWS_SDK_ENV) packer build $(if $(ROLE),-only=amazon-ebs.$(ROLE)) provisioning/packer

aws-infra-install:
	npm --prefix provisioning/aws ci

aws-infra-check:
	npm --prefix provisioning/aws run format:check
	npm --prefix provisioning/aws run lint
	npm --prefix provisioning/aws run typecheck

aws-infra-preview:
	cd provisioning/aws && $(AWS_SDK_ENV) pulumi preview --stack "$(or $(STACK),dev)"

aws-infra-up:
	cd provisioning/aws && $(AWS_SDK_ENV) pulumi up --stack "$(or $(STACK),dev)"

aws-infra-destroy:
	cd provisioning/aws && $(AWS_SDK_ENV) pulumi destroy --stack "$(or $(STACK),dev)"

# TEAM でチームを選ぶ（1 始まり）
aws-benchmark:
	go -C bench run . \
		-target "http://$$(cd provisioning/aws && pulumi stack output teamOutputs --stack "$(or $(STACK),dev)" --json | jq -r '.[$(or $(TEAM),1) - 1].routerPublicAddress'):8080" \
		-host "$$(cd provisioning/aws && pulumi stack output teamOutputs --stack "$(or $(STACK),dev)" --json | jq -r '.[$(or $(TEAM),1) - 1].appHostname')" \
		-scale large \
		$(BENCH_ARGS)

check: proto-lint vet frontend-lint frontend-typecheck contract test build

.PHONY: dev-up dev-down dev-logs dev-check

dev-up:
	docker compose up -d --build --wait --wait-timeout 180

dev-down:
	docker compose down

dev-logs:
	docker compose logs -f

dev-check:
	python3 deploy/dev/smoke.py

# Isolated portal -> supervisor -> real webapp development environment.
DEBUG_COMPOSE := docker compose -f deploy/local-debug/compose.yaml
.PHONY: debug-up debug-down debug-register debug-bench debug-result debug-logs debug-loadfixture

debug-up:
	python3 deploy/local-debug/scripts/timing.py
	$(DEBUG_COMPOSE) --env-file deploy/local-debug/.env.timing up -d --build --wait
	$(DEBUG_COMPOSE) exec -T portal-db psql -U isuxportal -d isuxportal -v ON_ERROR_STOP=1 -c "INSERT INTO jobs (name, args) VALUES ('update_contestant_dashboard', '{}');"
	@echo "Portal: http://localhost:$${DEBUG_PORTAL_PORT:-18081}; run make debug-register next"

debug-down:
	$(DEBUG_COMPOSE) down

debug-register:
	$(DEBUG_COMPOSE) exec -T portal /opt/portal/bin/local-debug register

debug-bench: debug-register
	$(DEBUG_COMPOSE) exec -T portal /opt/portal/bin/local-debug enqueue

debug-result:
	$(DEBUG_COMPOSE) exec -T portal /opt/portal/bin/local-debug status

debug-logs:
	$(DEBUG_COMPOSE) logs -f --tail=100 portal portal-worker supervisor

# Append synthetic history to the running debug portal, preserving existing teams.
debug-loadfixture:
	bash deploy/local-debug/scripts/loadfixture.sh

.PHONY: debug-refresh-scores
debug-refresh-scores:
	bash deploy/local-debug/scripts/loadfixture.sh --scores-only
