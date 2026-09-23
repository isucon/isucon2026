.PHONY: build test fmt check docker-build docker-test

build:
	cargo build --locked --release --bin isuxportal-supervisor

test:
	cargo test --locked

fmt:
	cargo fmt

check:
	cargo fmt --check
	cargo check --locked --all-targets

docker-build:
	docker build -f Dockerfile -t isucon2026-supervisor:local ..

docker-test:
	docker build -f Dockerfile --target test ..
