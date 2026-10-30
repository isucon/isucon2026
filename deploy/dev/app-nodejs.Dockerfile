# syntax=mirror.gcr.io/docker/dockerfile:1.10
FROM mirror.gcr.io/library/node:26.12.1-alpine AS nodejs-build
WORKDIR /app/nodejs
COPY webapp/nodejs/package.json webapp/nodejs/package-lock.json ./
RUN npm ci --omit=dev

FROM mirror.gcr.io/library/node:24.14.1-alpine AS frontend-build
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
ENV VITE_API_PREFIX=/api
RUN npm run build

FROM mirror.gcr.io/library/node:26.12.1-alpine
RUN apk add --no-cache bash ca-certificates curl iproute2 nginx postgresql-client tzdata
WORKDIR /app/nodejs
COPY --from=nodejs-build /app/nodejs/node_modules ./node_modules
COPY webapp/nodejs/package.json webapp/nodejs/package-lock.json ./
COPY webapp/nodejs/src ./src
COPY --from=frontend-build /src/dist /usr/share/nginx/html
COPY webapp/sql/init.sh webapp/sql/reset.sql /app/sql/
COPY deploy/dev/nginx.conf /etc/nginx/http.d/default.conf
COPY deploy/dev/routes.sh deploy/dev/app-entrypoint.sh /usr/local/bin/
COPY deploy/dev/isureal-nodejs.sh /usr/local/bin/isureal
RUN chmod +x /usr/local/bin/routes.sh /usr/local/bin/app-entrypoint.sh /usr/local/bin/isureal /app/sql/init.sh
EXPOSE 80
ENTRYPOINT ["app-entrypoint.sh"]
