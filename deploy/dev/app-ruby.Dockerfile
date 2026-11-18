# syntax=docker/dockerfile:1.10
FROM ruby:4.0.9-slim-trixie@sha256:88f0d8f1f2f7829c8c85c5cfee774f7ac936a7ba460e904e8c873b6a7f96c311 AS ruby-build
RUN apt-get update && apt-get install -y --no-install-recommends build-essential && rm -rf /var/lib/apt/lists/*
ENV BUNDLE_WITHOUT=test
WORKDIR /app/ruby
COPY webapp/ruby/Gemfile webapp/ruby/Gemfile.lock ./
RUN bundle install

FROM node:24.14.1-alpine AS frontend-build
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
ENV VITE_API_PREFIX=/api
RUN npm run build

FROM ruby:4.0.9-slim-trixie@sha256:88f0d8f1f2f7829c8c85c5cfee774f7ac936a7ba460e904e8c873b6a7f96c311
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl iproute2 iputils-ping libturbojpeg0 nginx postgresql-client tzdata \
  && rm -rf /var/lib/apt/lists/* /etc/nginx/sites-enabled/default
ENV BUNDLE_WITHOUT=test
WORKDIR /app/ruby
COPY --from=ruby-build /usr/local/bundle /usr/local/bundle
COPY webapp/ruby/Gemfile webapp/ruby/Gemfile.lock webapp/ruby/config.ru ./
COPY webapp/ruby/config ./config
COPY webapp/ruby/lib ./lib
COPY --from=frontend-build /src/dist /usr/share/nginx/html
COPY webapp/sql/init.sh webapp/sql/reset.sql /app/sql/
COPY deploy/dev/nginx.conf /etc/nginx/conf.d/default.conf
COPY deploy/dev/routes.sh deploy/dev/app-entrypoint.sh /usr/local/bin/
COPY deploy/dev/isureal-ruby.sh /usr/local/bin/isureal
RUN chmod +x /usr/local/bin/routes.sh /usr/local/bin/app-entrypoint.sh /usr/local/bin/isureal /app/sql/init.sh
EXPOSE 80
ENTRYPOINT ["app-entrypoint.sh"]
