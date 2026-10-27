# syntax=docker/dockerfile:1.10
FROM node:26.12.1-trixie-slim AS nodejs-build
WORKDIR /app/nodejs
COPY webapp/nodejs/package.json webapp/nodejs/package-lock.json ./
RUN npm ci
COPY webapp/nodejs/tsconfig.json ./
COPY webapp/nodejs/src ./src
RUN npm run build && npm prune --omit=dev

FROM node:24.14.1-alpine AS frontend-build
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
ENV VITE_API_PREFIX=/api
RUN npm run build

FROM node:26.12.1-trixie-slim
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl iproute2 nginx postgresql-client tzdata \
  && rm -rf /var/lib/apt/lists/* /etc/nginx/sites-enabled/default
WORKDIR /app/nodejs
COPY --from=nodejs-build /app/nodejs/package.json ./
COPY --from=nodejs-build /app/nodejs/node_modules ./node_modules
COPY --from=nodejs-build /app/nodejs/dist ./dist
COPY --from=frontend-build /src/dist /usr/share/nginx/html
COPY webapp/sql/init.sh webapp/sql/reset.sql /app/sql/
COPY deploy/dev/nginx.conf /etc/nginx/conf.d/default.conf
COPY deploy/dev/routes.sh deploy/dev/app-entrypoint.sh /usr/local/bin/
COPY deploy/dev/isureal-nodejs.sh /usr/local/bin/isureal
RUN chmod +x /usr/local/bin/routes.sh /usr/local/bin/app-entrypoint.sh /usr/local/bin/isureal /app/sql/init.sh
EXPOSE 80
ENTRYPOINT ["app-entrypoint.sh"]
