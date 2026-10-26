# syntax=docker/dockerfile:1.10
FROM rust:1.95.0-slim-trixie AS rust-build
WORKDIR /src
COPY webapp/rust/Cargo.toml webapp/rust/Cargo.lock webapp/rust/rust-toolchain.toml ./
COPY webapp/rust/src ./src
RUN --mount=type=cache,target=/usr/local/cargo/registry --mount=type=cache,target=/src/target cargo build --release --locked && install -D -m 0755 target/release/isureal /out/isureal

FROM node:24.14.1-alpine AS frontend-build
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
ENV VITE_API_PREFIX=/api
RUN npm run build

FROM debian:trixie-slim
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl iproute2 nginx postgresql-client tzdata \
  && rm -rf /var/lib/apt/lists/* /etc/nginx/sites-enabled/default
WORKDIR /app/rust
COPY --from=rust-build /out/isureal /usr/local/bin/isureal
COPY --from=frontend-build /src/dist /usr/share/nginx/html
COPY webapp/sql/init.sh webapp/sql/reset.sql /app/sql/
COPY deploy/dev/nginx.conf /etc/nginx/conf.d/default.conf
COPY deploy/dev/routes.sh deploy/dev/app-entrypoint.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/routes.sh /usr/local/bin/app-entrypoint.sh /app/sql/init.sh
EXPOSE 80
ENTRYPOINT ["app-entrypoint.sh"]
