#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"
for name in astra-engine-postgres astra-engine-mysql; do
    if docker inspect "$name" >/dev/null 2>&1; then
        echo "Dedicated experiment container already exists: $name" >&2
        exit 1
    fi
done
created=()
cleanup() {
    for name in "${created[@]}"; do docker rm -fv "$name" >/dev/null; done
}
trap cleanup EXIT
pg=postgres@sha256:9e2cf6287f25c3151f4c9421ccce2b17f3e60061c111a29165e0d2cdd9ff0a97
my=mysql@sha256:a0225d1ea6631c8bc2385dc4c2faba17bbf1834ecf369e1659a9a382bd8d5dd7
docker run -d --name astra-engine-postgres --cpus 2 --memory 1g -p 127.0.0.1:26216:5432 \
    -e POSTGRES_HOST_AUTH_METHOD=trust -e POSTGRES_DB=astra_engine "$pg" \
    -c shared_buffers=256MB -c max_connections=40 -c wal_level=minimal -c max_wal_senders=0 \
    -c full_page_writes=on -c fsync=on -c synchronous_commit=on -c wal_compression=lz4 \
    -c checkpoint_timeout=15min -c max_wal_size=2GB >/dev/null
created+=(astra-engine-postgres)
docker run -d --name astra-engine-mysql --cpus 2 --memory 1g -p 127.0.0.1:26215:3306 \
    -e MYSQL_ALLOW_EMPTY_PASSWORD=yes -e MYSQL_ROOT_HOST=% -e MYSQL_DATABASE=astra_engine "$my" \
    --innodb-buffer-pool-size=256M --innodb-redo-log-capacity=256M \
    --innodb-flush-log-at-trx-commit=1 --innodb-doublewrite=ON --skip-log-bin \
    --max-connections=40 --max-allowed-packet=64M >/dev/null
created+=(astra-engine-mysql)
ready() {
    for attempt in $(seq 1 60); do
        if "$@" >/dev/null 2>&1; then return; fi
        sleep 1
    done
    echo 'Experiment database did not become ready' >&2
    return 1
}
ready docker exec astra-engine-postgres pg_isready -h 127.0.0.1 -U postgres
ready docker exec astra-engine-mysql mysqladmin --protocol=tcp -h127.0.0.1 ping
export CARGO_TARGET_DIR="${CARGO_TARGET_DIR:-$PWD/target}"
cargo +1.99.1 build --release --locked
output=$(mktemp -d "${TMPDIR:-/tmp}/astra-engine-results.XXXXXX")
for iteration in 1 2 3; do
    engines='mysql postgres'
    if [ "$iteration" = 2 ]; then engines='postgres mysql'; fi
    for engine in $engines; do
        for phase in write read; do
            "$CARGO_TARGET_DIR/release/astra-image-engine-compare" "$engine" "$phase" 512 262144 8 >"$output/$engine-repeat$iteration-$phase.json"
        done
    done
done
for engine in postgres mysql; do
    docker restart "astra-engine-$engine" >/dev/null
    if [ "$engine" = postgres ]; then
        ready docker exec astra-engine-postgres pg_isready -h 127.0.0.1 -U postgres
    else
        ready docker exec astra-engine-mysql mysqladmin --protocol=tcp -h127.0.0.1 ping
    fi
    "$CARGO_TARGET_DIR/release/astra-image-engine-compare" "$engine" read 512 262144 8 >"$output/$engine-after-restart-read.json"
done
printf 'Results: %s\n' "$output"
