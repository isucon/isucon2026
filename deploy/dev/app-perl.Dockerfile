# syntax=docker/dockerfile:1.10
FROM perl:5.44.0-trixie@sha256:8573a88cae3ae8ff95cff0a40ab72a0cadd2cb59f0e77ecd380e586a0607a572 AS perl-build
RUN apt-get update && apt-get install -y --no-install-recommends libjpeg-dev libpq-dev && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://raw.githubusercontent.com/skaji/cpm/v1.1.6/cpm | perl - install -g App::cpm@1.1.6 Carton@v1.0.35
WORKDIR /src
COPY webapp/perl/cpanfile webapp/perl/cpanfile.snapshot ./
RUN --mount=type=cache,target=/root/.perl-cpm cpm install
RUN perl -Ilocal/lib/perl5 -MImager -e 'exit !$Imager::formats{jpeg}'

FROM node:24.14.1-alpine AS frontend-build
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
ENV VITE_API_PREFIX=/api
RUN npm run build

FROM perl:5.44.0-slim-trixie@sha256:a0c68e9afd12dc9a10c0693f1dacac08e93d41a9e2f43aefc3a209c53e5147d2
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl iproute2 iputils-ping libjpeg62-turbo libpq5 nginx postgresql-client tzdata \
  && rm -rf /var/lib/apt/lists/* /etc/nginx/sites-enabled/default
ENV MOJO_MODE=production
WORKDIR /app/perl
COPY --from=perl-build /src/local ./local
COPY webapp/perl/lib ./lib
COPY webapp/perl/script ./script
COPY --from=frontend-build /src/dist /usr/share/nginx/html
COPY webapp/sql/init.sh webapp/sql/reset.sql /app/sql/
COPY deploy/dev/nginx.conf /etc/nginx/conf.d/default.conf
COPY deploy/dev/routes.sh deploy/dev/app-entrypoint.sh /usr/local/bin/
COPY deploy/dev/isureal-perl.sh /usr/local/bin/isureal
RUN chmod +x /usr/local/bin/routes.sh /usr/local/bin/app-entrypoint.sh /usr/local/bin/isureal /app/sql/init.sh
EXPOSE 80
ENTRYPOINT ["app-entrypoint.sh"]
