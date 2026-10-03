# syntax=docker/dockerfile:1.10
FROM golang:1.26.5-alpine AS go-build
WORKDIR /src
COPY webapp-optimized/go/go.mod webapp-optimized/go/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY webapp-optimized/go/ ./
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -o /out/isureal ./cmd/isureal

FROM node:24.14.1-alpine AS frontend-build
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
ENV VITE_API_PREFIX=/api
RUN npm run build

FROM alpine:3.23
RUN apk add --no-cache bash ca-certificates curl iproute2 nginx postgresql-client tzdata
WORKDIR /app/go
COPY --from=go-build /out/isureal /usr/local/bin/isureal
COPY --from=frontend-build /src/dist /usr/share/nginx/html
COPY webapp-optimized/sql/001-schema.sql webapp-optimized/sql/init.sh /app/sql/
COPY webapp/sql/002-seed.sql /app/sql/002-seed.sql
COPY webapp-optimized/nginx.conf /etc/nginx/nginx.conf
COPY deploy/dev/routes.sh deploy/dev/app-entrypoint.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/routes.sh /usr/local/bin/app-entrypoint.sh /app/sql/init.sh
EXPOSE 80
ENTRYPOINT ["app-entrypoint.sh"]
