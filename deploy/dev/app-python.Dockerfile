# syntax=mirror.gcr.io/docker/dockerfile:1.10
FROM ghcr.io/astral-sh/uv:0.13.5@sha256:32a7bb39b496babbd997b0a39a4d66c126bdaf5661b302bf6f06abda8deca6ff AS uv

FROM mirror.gcr.io/library/python:3.15.1-slim-trixie@sha256:533b0b2f9b5d46fd097091cf826f27902f1d6c3bca714b6dc74551356aa97a51 AS python-build
COPY --from=uv /uv /usr/local/bin/uv
ENV UV_COMPILE_BYTECODE=1 UV_LINK_MODE=copy UV_PYTHON_DOWNLOADS=never
WORKDIR /app/python
COPY webapp/python/pyproject.toml webapp/python/uv.lock webapp/python/.python-version ./
RUN --mount=type=cache,target=/root/.cache/uv uv sync --frozen --no-dev

FROM mirror.gcr.io/library/node:24.14.1-alpine AS frontend-build
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
ENV VITE_API_PREFIX=/api
RUN npm run build

FROM mirror.gcr.io/library/python:3.15.1-slim-trixie@sha256:533b0b2f9b5d46fd097091cf826f27902f1d6c3bca714b6dc74551356aa97a51
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl iproute2 iputils-ping nginx postgresql-client tzdata \
  && rm -rf /var/lib/apt/lists/* /etc/nginx/sites-enabled/default
WORKDIR /app/python
COPY --from=python-build /app/python/.venv ./.venv
COPY webapp/python/gunicorn.conf.py ./
COPY webapp/python/isureal ./isureal
COPY --from=frontend-build /src/dist /usr/share/nginx/html
COPY webapp/sql/init.sh webapp/sql/reset.sql /app/sql/
COPY deploy/dev/nginx.conf /etc/nginx/conf.d/default.conf
COPY deploy/dev/routes.sh deploy/dev/app-entrypoint.sh /usr/local/bin/
COPY deploy/dev/isureal-python.sh /usr/local/bin/isureal
RUN chmod +x /usr/local/bin/routes.sh /usr/local/bin/app-entrypoint.sh /usr/local/bin/isureal /app/sql/init.sh
EXPOSE 80
ENTRYPOINT ["app-entrypoint.sh"]
