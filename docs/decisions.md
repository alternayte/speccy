# Decisions

Small implementation choices that `SDD.md` does not cover (`BUILD.md` §2). Newest last.

## 2026-09-18 — Module path

- **Choice:** `github.com/alternayte/speccy`. Confirmed by the owner (SDD §19 Q2).

## 2026-09-18 — Layout follows SDD §17, with repo tooling from sluice

- **Choice:** Use the SDD §17 tree. Take the justfile shape, `go tool` directives, air config, golangci-lint config, the gen-check diff, and the `tools/buildtool` check command from `github.com/alternayte/sluice`.
- **Alternative:** Copy sluice's layout (`internal/<feature>/` flat, `ui/` on Bun, OpenAPI generated from Go).
- **Reason:** SDD §17 fixes the tree. DEC-026 makes the OpenAPI file the source. AGENTS.md sets pnpm for the frontend.

## 2026-09-18 — pnpm runs through npx at a pinned version

- **Choice:** The justfile runs `npx --yes pnpm@12.4.2`. `web/package.json` records the same version in `packageManager`.
- **Alternative:** Require a global pnpm.
- **Reason:** A clean clone needs only Node. The version is the same on every machine and in CI.

## 2026-09-18 — TypeScript 6.0

- **Choice:** `typescript@~6.0`.
- **Alternative:** TypeScript 7.
- **Reason:** typescript-eslint 8.70 supports TypeScript below 6.1 only.

## 2026-09-18 — HTTP stack

- **Choice:** `net/http` `ServeMux`, with oapi-codegen `std-http-server` and `strict-server` output in `internal/http/api`. Package `internal/http` is named `http` and imports the standard library as `nethttp`.
- **Alternative:** chi or huma.
- **Reason:** The standard mux has method and path patterns. A router library adds nothing that M0 uses.

## 2026-09-18 — `GET /api/v1/meta`

- **Choice:** One endpoint that returns the version and the mode. The home page reads it.
- **Alternative:** No endpoint at M0.
- **Reason:** It proves the full path: OpenAPI file, generated Go interface, generated TypeScript client, and the SPA.

## 2026-09-18 — Local mode address

- **Choice:** Default `127.0.0.1:7878`, set with `--addr`. Any address that is not a loopback IP is refused (DEC-015, T-040). `localhost` maps to `127.0.0.1`.
- **Alternative:** A random free port.
- **Reason:** The Vite dev server proxies `/api` to a fixed address.

## 2026-09-18 — CLI parsing

- **Choice:** The standard `flag` package, with one `switch` on the command name.
- **Alternative:** cobra.
- **Reason:** M0 has three commands. Revisit when M11 adds the full command set.

## 2026-09-18 — Embedded SPA placeholder

- **Choice:** `web/dist/.keep` is committed, so `go build` works before the web app is built. The server answers 503 with a hint when `index.html` is missing.
- **Alternative:** Build the web app in `go generate`.
- **Reason:** Go tooling must work without Node.

## 2026-09-18 — JavaScript budget at M0

- **Choice:** `buildtool budget` measures the entry chunk and its static imports. Lazy route chunks are excluded.
- **Alternative:** Wait for the bundle route.
- **Reason:** The bundle route arrives at M2. The check must then add that route's chunk.

## 2026-09-18 — Recipes created at M0

- **Choice:** `setup`, `dev`, `gen`, `gen-check`, `test`, `test-pg`, `lint`, `build-web`, `budget`, `build`, `verify`.
- **Alternative:** All BUILD §3 recipes now.
- **Reason:** `dev-hosted`, `e2e`, `gauntlet` and `release` have nothing to run until M8, M10 and M12. `gen` adds sqlc at M1. `verify` adds `speccy review` at M3 (BUILD §7).

## 2026-09-18 — Database drivers

- **Choice:** `modernc.org/sqlite` for SQLite and `pgx/v5/stdlib` for Postgres. Both run through `database/sql`.
- **Alternative:** `mattn/go-sqlite3`, and the native pgx pool.
- **Reason:** modernc needs no cgo, so the binary stays static. With `database/sql` on both engines, a projection gets one `*sql.Tx` type, whatever the engine.

## 2026-09-18 — SQLite connection settings

- **Choice:** One open connection, WAL, `foreign_keys(1)`, `busy_timeout(5000)`, `synchronous(NORMAL)`, `_time_format=sqlite`.
- **Alternative:** A connection pool with `BEGIN IMMEDIATE`.
- **Reason:** SQLite allows one writer. One connection serialises writes in the process, so a write never gets `SQLITE_BUSY`. Local mode has one user.

## 2026-09-18 — Generated query packages

- **Choice:** SQL in `db/<engine>/queries/<feature>.sql`. sqlc writes Go to `db/postgres` (package `pgdb`) and `db/sqlite` (package `sqlitedb`). Migrations are in `db/<engine>/migrations`, embedded by package `db`.
- **Alternative:** Generated code in each feature package.
- **Reason:** SDD §11.2 puts the queries for both engines in `db/postgres/` and `db/sqlite/`.

## 2026-09-18 — Column types per engine

- **Choice:** Postgres uses `uuid`, `jsonb` and `timestamptz`. SQLite uses `TEXT` for UUIDs and JSON (with a `json_valid` check) and `DATETIME` for times. Go reads every time as UTC.
- **Alternative:** `TEXT` on both engines.
- **Reason:** Each engine keeps its native types. The conformance suite proves that both return the same values.

## 2026-09-18 — Event store API

- **Choice:** `es.Store` has `Append`, `Load` and `Events`. `Append` takes the expected version (0 for a new stream), the new snapshot and the events. Projections are registered per stream type in `es.New`. Each projection gets a `store.Tx`, which carries the engine.
- **Alternative:** A generic load-decide-evolve-append helper now.
- **Reason:** The aggregates arrive at M9. The helper waits until those three aggregates show its shape.

## 2026-09-18 — No workspace table at M1

- **Choice:** M1 migrates only `es_streams` and `es_events`. The `workspace` table arrives with the first tenant-scoped table at M2.
- **Alternative:** Create every table in SDD §11.1 now.
- **Reason:** A table arrives with the feature that uses it. SDD §11.3 gives the `es_*` tables no `workspace_id`.

## 2026-09-18 — Postgres in tests

- **Choice:** testcontainers-go with `postgres:17-alpine`. One container per test binary, and one fresh database per test. The `postgres` build tag turns it on.
- **Alternative:** A `docker compose` Postgres that `just test-pg` starts.
- **Reason:** The tests own their database. CI and a clean clone need only Docker.
