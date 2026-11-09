.PHONY: tools fmt vet build test frontend-install frontend-lint frontend-typecheck frontend-test frontend-e2e proto-lint proto-generate proto-build portal-fmt portal-vet portal-test portal-build portal-check router-fmt router-vet router-test router-build router-haproxy-check router-check ami aws-infra-install aws-infra-check aws-infra-preview aws-infra-up aws-infra-destroy aws-benchmark datagen datagen-test check

tools:
	mise install

fmt:
	gofmt -w $$(find webapp/go -name '*.go' -type f)

vet:
	go -C webapp/go vet ./...

build:
	go -C webapp/go build ./...
	npm --prefix frontend run build

test:
	go -C webapp/go test ./...
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

datagen:
	cd webapp/datagen && go run .

datagen-test:
	cd dataset && go test ./...
	cd webapp/datagen && go test ./...

proto-lint:
	mise exec -- buf lint

proto-generate:
	mise exec -- buf generate
	go -C gen/go mod tidy
	npm --prefix gen/ts install --package-lock-only --ignore-scripts

proto-build:
	mise exec -- buf build -o /tmp/isucon2026-proto.binpb

portal-fmt:
	go -C portal fmt ./...

portal-vet:
	go -C portal vet ./...

portal-test:
	$(MAKE) -C portal test

portal-build:
	$(MAKE) -C portal build

portal-check: portal-vet portal-test portal-build

router-fmt:
	gofmt -w $$(find router -name '*.go' -type f)

router-vet:
	go -C router vet ./...

router-test:
	go -C router test ./...

router-build:
	go -C router build -trimpath -o /tmp/isureal-router ./cmd/router
	go -C router build -trimpath -o /tmp/isureal-router-control ./cmd/router-control

# 生成した HAProxy の設定を、コンテナと同じ版の HAProxy で検証する。既定の環境変数（HTTP だけ）と、
# AWS と同じ HTTPS あり（使い捨ての自己署名の PEM、上流は 80）の 2 通り
router-haproxy-check:
	dir="$$(mktemp -d)"; trap 'rm -rf "$$dir"' EXIT; chmod 755 "$$dir"; \
	ROUTER_HAPROXY_DIR="$$dir" go -C router run ./cmd/router haproxy-config && \
	docker run --rm -v "$$dir:$$dir:ro" mirror.gcr.io/library/haproxy:2.8.16-alpine haproxy -c -f "$$dir/haproxy.cfg"
	dir="$$(mktemp -d)"; trap 'rm -rf "$$dir"' EXIT; chmod 755 "$$dir"; \
	openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 1 -subj /CN=router.test \
		-keyout "$$dir/key.pem" -out "$$dir/cert.pem" 2>/dev/null && \
	cat "$$dir/cert.pem" "$$dir/key.pem" >"$$dir/server.pem" && chmod 644 "$$dir/server.pem" && \
	ROUTER_HAPROXY_DIR="$$dir" ROUTER_HTTPS_LISTEN_ADDRESS=0.0.0.0:443 ROUTER_TLS_CERTIFICATE="$$dir/server.pem" \
		ROUTER_UPSTREAM_PORT=80 go -C router run ./cmd/router haproxy-config && \
	docker run --rm -v "$$dir:$$dir:ro" mirror.gcr.io/library/haproxy:2.8.16-alpine haproxy -c -f "$$dir/haproxy.cfg"

router-check: router-vet router-test router-build router-haproxy-check

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

# ベンチを Fargate（本番の Supervisor と同じ 4 vCPU / 8 GiB、同じ NAT）から実行する。
# supervisor/Dockerfile のビルド → ECR へ push → ecspresso run → 結果を var/aws-bench/ へ取得
.PHONY: aws-bench-runner-preview aws-bench-runner-up aws-bench-run aws-bench-image aws-bench-show
# make の中では mise の環境変数が入らないので、provisioning/aws/mise.toml と同じ backend を渡す
BENCH_RUNNER_PULUMI = cd provisioning/aws/bench-runner && $(AWS_SDK_ENV) PULUMI_BACKEND_URL="$${PULUMI_BACKEND_URL:-s3://isucon2026-pulumi-state}" pulumi

aws-bench-runner-preview:
	$(BENCH_RUNNER_PULUMI) preview --stack "$(or $(RUNNER_STACK),dev)"

aws-bench-runner-up:
	$(BENCH_RUNNER_PULUMI) up --stack "$(or $(RUNNER_STACK),dev)"

# TEAM は必須（8,9,10 のように並べると同時に流す）。IMAGE（repository@digest）を指定するとビルドと push を省く
aws-bench-run:
	$(AWS_SDK_ENV) TEAM="$(TEAM)" BENCH_ARGS="$(BENCH_ARGS)" IMAGE="$(IMAGE)" HOST_STATS="$(HOST_STATS)" provisioning/aws/bench-runner/run.sh run

aws-bench-image:
	$(AWS_SDK_ENV) provisioning/aws/bench-runner/run.sh image

aws-bench-show:
	provisioning/aws/bench-runner/run.sh show $(RUN)

# ポータルの Supervisor（provisioning/aws/supervisor）用のイメージ。supervisor/Dockerfile に投稿用の写真のアーカイブと
# 初期データの画像のダイジェストの表を同梱して linux/amd64 でビルドし、Supervisor stack の ECR へ push して、
# Pulumi の image に書く値を表示する。どちらも bench-runner の結果バケットの assets/ から var/photos/ に取る
# （PHOTOS_S3、DIGESTS_S3 で別の場所を指定できる）
.PHONY: supervisor-photos supervisor-image
SUPERVISOR_PHOTOS = var/photos/merged.zip
SUPERVISOR_DIGESTS = var/photos/image-digests-large.bin
SUPERVISOR_ASSETS = s3://$$($(BENCH_RUNNER_PULUMI) stack output infra --json --stack "$(or $(RUNNER_STACK),dev)" | jq -r .bucket)/assets
supervisor-photos: $(SUPERVISOR_PHOTOS) $(SUPERVISOR_DIGESTS)
$(SUPERVISOR_PHOTOS):
	mkdir -p $(dir $@)
	$(AWS_SDK_ENV) aws s3 cp "$(or $(PHOTOS_S3),$(SUPERVISOR_ASSETS)/merged.zip)" $@
$(SUPERVISOR_DIGESTS):
	mkdir -p $(dir $@)
	$(AWS_SDK_ENV) aws s3 cp "$(or $(DIGESTS_S3),$(SUPERVISOR_ASSETS)/image-digests-large.bin)" $@

supervisor-image: $(SUPERVISOR_PHOTOS) $(SUPERVISOR_DIGESTS)
	$(AWS_SDK_ENV) SUPERVISOR_STACK="$(or $(SUPERVISOR_STACK),portal-dev)" provisioning/aws/supervisor/image.sh

# 初期データ（Large）の画像のダイジェストの表（bench -image-digests）を作る。すべての画像を描くので CPU 時間で
# 50 分ほどかかる。できた表は bench-runner の結果バケットの assets/image-digests-large.bin に置く
# （bench-runner の run.sh と make supervisor-photos がそこから取る）
.PHONY: bench-image-digests
bench-image-digests:
	cd bench && go run ./cmd/imagedigests -scale large -out ../var/image-digests/large.bin

check: proto-lint vet frontend-lint frontend-typecheck test build
