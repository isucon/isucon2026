FROM mirror.gcr.io/library/golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS build
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN for command in api worker image-sanitizer reset seed; do \
      CGO_ENABLED=0 go build -trimpath -o "/out/$command" "./cmd/$command"; \
    done

FROM mirror.gcr.io/library/alpine:3.23@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40
RUN addgroup -S isureal && adduser -S -G isureal isureal
COPY --from=build /out/* /usr/local/bin/
COPY backend/schema/schema.sql /usr/local/share/isureal/schema.sql
COPY specs/001-isureal-social-app/contracts/openapi.yaml /usr/local/share/isureal/openapi.yaml
RUN mkdir -p /var/lib/isureal/images && chown -R isureal:isureal /var/lib/isureal
USER isureal
ENTRYPOINT ["/usr/local/bin/api"]
