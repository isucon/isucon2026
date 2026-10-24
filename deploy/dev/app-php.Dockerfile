# syntax=docker/dockerfile:1.10
FROM php:8.5.12-fpm-trixie@sha256:f49965c6e6a237eb3b27a6fc565e167839dc8abc65ee0652af0078f6ecdf4bbd AS php-base
# 拡張をビルドしたら、ビルドにだけ要るパッケージを消す
RUN apt-get update \
  && apt-get install -y --no-install-recommends libicu-dev libpq-dev libzip-dev \
  && docker-php-ext-install -j"$(nproc)" intl pdo_pgsql zip \
  && apt-mark manual libicu76 libpq5 libzip5 \
  && apt-get purge -y --auto-remove libicu-dev libpq-dev libzip-dev \
  && rm -rf /var/lib/apt/lists/*

FROM php-base AS php-build
COPY --from=composer:2.10.3@sha256:b6dcd72ca98fa19d0e5b8476de179859704891fad06dd9e0b96833d4c430a5fc /usr/bin/composer /usr/bin/composer
WORKDIR /src
COPY webapp/php/composer.json webapp/php/composer.lock ./
RUN --mount=type=cache,target=/root/.cache/composer composer install --no-dev --no-autoloader --no-interaction --no-progress
COPY webapp/php/ ./
RUN composer dump-autoload --no-dev --optimize --no-interaction

FROM node:24.14.1-alpine AS frontend-build
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
ENV VITE_API_PREFIX=/api
RUN npm run build

FROM php-base
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl iproute2 iputils-ping nginx postgresql-client tzdata \
  && rm -rf /var/lib/apt/lists/* /etc/nginx/sites-enabled/default
WORKDIR /app/php
COPY --from=php-build /src/app ./app
COPY --from=php-build /src/public ./public
COPY --from=php-build /src/src ./src
COPY --from=php-build /src/vendor ./vendor
COPY --from=php-build /src/php-fpm.conf ./php-fpm.conf
COPY --from=frontend-build /src/dist /usr/share/nginx/html
COPY webapp/sql/init.sh webapp/sql/reset.sql /app/sql/
COPY deploy/dev/nginx.conf /etc/nginx/conf.d/default.conf
COPY deploy/dev/nginx-php.conf /etc/nginx/conf.d/php.conf
COPY deploy/dev/routes.sh deploy/dev/app-entrypoint.sh /usr/local/bin/
COPY deploy/dev/isureal-php.sh /usr/local/bin/isureal
RUN chmod +x /usr/local/bin/routes.sh /usr/local/bin/app-entrypoint.sh /usr/local/bin/isureal /app/sql/init.sh
STOPSIGNAL SIGTERM
EXPOSE 80
ENTRYPOINT ["app-entrypoint.sh"]
