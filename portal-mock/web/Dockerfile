# syntax=docker/dockerfile:1.10

FROM node:24.14.1-alpine3.23 AS build

WORKDIR /src/portal-mock/web

COPY gen/ts/ /src/gen/ts/
COPY portal-mock/web/package.json portal-mock/web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci \
    && ln -s /src/portal-mock/web/node_modules /src/node_modules

# Web は共有の gen/ts を import するため、build context はリポジトリルート。
COPY portal-mock/web/ ./
RUN npm run build

FROM nginx:1.29.1-alpine3.22

COPY portal-mock/web/nginx.conf /etc/nginx/nginx.conf
COPY --from=build /src/portal-mock/web/dist/ /usr/share/nginx/html/

EXPOSE 5174

HEALTHCHECK --interval=5s --timeout=2s --retries=12 \
  CMD wget -q -O /dev/null http://127.0.0.1:5174/ || exit 1
