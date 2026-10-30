# Build context: repository root. Includes the ISUReal benchmark executable.
# PHOTOS=archive で var/photos/merged.zip（投稿に使う写真のアーカイブ）と var/photos/image-digests-large.bin
# （初期データの画像のダイジェストの表）を /opt/isucon/photos/ に同梱する。
# 既定の none では同梱しないので、アーカイブがなくてもビルドできる（bench-runner はこちらで、走行ごとに URL を渡す）。
ARG PHOTOS=none
FROM mirror.gcr.io/library/rust:1.90-bookworm@sha256:3914072ca0c3b8aad871db9169a651ccfce30cf58303e5d6f2db16d1d8a7e58f AS source
WORKDIR /src/supervisor
COPY supervisor/Cargo.toml supervisor/Cargo.lock supervisor/rustfmt.toml ./
COPY supervisor/src ./src
COPY supervisor/gen ./gen
COPY supervisor/tests ./tests

FROM source AS test
RUN rustup component add rustfmt
RUN cargo fmt --check && cargo test --locked && cargo check --locked --all-targets

FROM source AS build
RUN cargo build --locked --release --bin isuxportal-supervisor

FROM mirror.gcr.io/library/golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS bench
WORKDIR /src/bench
COPY bench/ ./
COPY portal/ /src/portal/
COPY dataset/ /src/dataset/
RUN CGO_ENABLED=0 go build -trimpath -o /out/isureal-bench .

# ルート直下ではなく /photos/ を写す。scratch のルートを COPY --from すると BuildKit のキャッシュが
# 中身の違いを見分けず、none と archive のビルドが互いの結果を使い回す
FROM scratch AS photos-none
WORKDIR /photos
FROM scratch AS photos-archive
COPY var/photos/merged.zip var/photos/image-digests-large.bin /photos/
FROM photos-${PHOTOS} AS photos

FROM mirror.gcr.io/library/debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251 AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libssl3 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --create-home supervisor \
    && mkdir -p /var/log/isuxportal-supervisor \
    && chown supervisor:supervisor /var/log/isuxportal-supervisor
COPY --from=build /src/supervisor/target/release/isuxportal-supervisor /usr/local/bin/
COPY --from=bench /out/isureal-bench /usr/local/bin/
COPY --from=photos /photos/ /opt/isucon/photos/
USER supervisor
ENV ISUXPORTAL_SUPERVISOR_LOG_DIRECTORY=/var/log/isuxportal-supervisor
ENTRYPOINT ["/usr/local/bin/isuxportal-supervisor"]
CMD ["/usr/local/bin/isureal-bench", "--target-port", "8080"]
