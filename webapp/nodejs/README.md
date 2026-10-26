# ISUReal Node.js reference implementation

This is a direct port of `../go`, using TypeScript, Hono, and PostgreSQL.
The structure follows the [ISUCON14 Node.js implementation](https://github.com/isucon/isucon14/tree/53f8b627e040c30ebec600457c6c97da008b84b0/webapp/nodejs): route handlers execute SQL directly, with explicit transaction boundaries.

All 42 Go routes are implemented. SQL, query order, row/table locks, retries,
visibility checks, and pagination directions follow the Go implementation.
Feed and user-search filtering still happens after loading the SQL results.
Image signing still performs 1,000 HMAC rounds for each URL. Images remain in
PostgreSQL. Notifications poll every 500 ms and close after one event.
There are no added caches, indexes, background workers, or parallel queries.

## Run

Requirements: Node.js 26 or later (for `Temporal`), PostgreSQL, Bash, and `psql` on `PATH`.
Use the same schema, generated seed, and initial snapshot as the Go app
(`../sql/001-schema.sql`, `002-seed.sql`, and `003-initial-snapshot.sql`).
See [data generation](../datagen/README.md) for preparing those files.

Run from this directory so `/api/initialize` can execute `../sql/init.sh`:

```sh
npm ci
npm start
```

The server listens on port 8080. `npm run dev` starts the TypeScript watcher.
Configuration matches Go:

| Variable | Default |
| --- | --- |
| `ISUREAL_REGION` | `asia` |
| `ISUREAL_DB_HOST` | `127.0.0.1` |
| `ISUREAL_DB_PORT` | `5432` |
| `ISUREAL_DB_USER` | `isureal` |
| `ISUREAL_DB_PASSWORD` | `isureal` |
| `ISUREAL_DB_NAME` | `isureal` |
| `ISUREAL_LOG_LEVEL` | `debug` |

The connection pool has a maximum of 30 connections.
Initialization uses the shared SQL reset script and returns `{"language":"nodejs"}`.
As in Go, every request is written to stdout as one JSON action log line
(`ISUREAL_LOG_LEVEL` is `debug`, `info`, or `error`), requests from IPs in
`ip_bans` get `403 ip_banned`, and uploaded images must decode as JPEG within
the Go size limits. On the problem servers the implementation runs as
`isureal-nodejs.service`; in the dev environment, start it with
`ISUREAL_WEBAPP_LANGUAGE=nodejs make dev-up`.

## Verify

```sh
npm run build
npm run typecheck
npm run format:check
npm test
```

Without a database URL, the database integration tests are skipped.
To run all tests, provide a **disposable database**: the tests recreate its
application tables using `../sql/001-schema.sql`.

```sh
ISUREAL_NODE_TEST_DSN=postgres://isureal:isureal@127.0.0.1:5432/isureal_node_test npm test
```

The same integration scenarios can target an already running Go or Node.js
server. Its database must be the disposable database named by the test URL:

```sh
ISUREAL_NODE_TEST_DSN=postgres://isureal:isureal@127.0.0.1:5432/isureal_node_test \
ISUREAL_TEST_BASE_URL=http://127.0.0.1:8080 npm test
```

Coverage includes authentication and region boundaries, concurrent registration,
transaction rollback, post visibility, cursor pagination, comments, RealMoji
replacement, image invalidation, relationships, account deletion, administration,
memories, ZIP export, and SSE delivery. The route test compares the route and
authentication inventory against `../go/server.go`.

Database timestamps retain microseconds for pagination and deadline comparisons.
PostgreSQL `numeric` coordinates become JSON numbers, and Unicode search uses
NFC normalization and full case folding as in Go. Framework-generated parsing
errors and ZIP container metadata are runtime-specific; API error codes and
exported content follow the Go implementation.
