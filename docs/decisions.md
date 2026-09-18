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

## 2026-09-18 — Bundle discovery in local mode

- **Choice:** `speccy` walks the served folder. Each folder with exactly one markdown file that has a frontmatter `type` is a bundle. A folder with two or more is listed as a problem that names the files. Hidden folders and `node_modules` are skipped. Symlinks are not followed. Files over 10 MB are skipped and listed as a problem. Confirmed by the owner.
- **Alternative:** Require `.speccy.yaml` `bundles:` globs at M2, or treat only the served folder as one bundle.
- **Reason:** Discovery works with no config. M3 adds `.speccy.yaml`, which can narrow the scan.

## 2026-09-18 — The db source at M2, and imports in local mode

- **Choice:** The db source (files as blobs, a version per change, REQ-009 limits) is built and tested on both engines. No server exposes it until hosted mode (M8). In local mode, an import writes a new folder under the served folder. Confirmed by the owner.
- **Alternative:** Hosted mode without auth on loopback, or local bundles that live only in SQLite.
- **Reason:** In local mode a bundle is always a folder the user can commit (DEC-003, DEC-018).

## 2026-09-18 — REQ-009 limits are constants until M8

- **Choice:** 10 MB per file and 50 MB per bundle, as constants in `internal/source/limits.go`. Imports in local mode use the same limits. Confirmed by the owner.
- **Alternative:** Environment variables now.
- **Reason:** Admin settings arrive at M8, which completes REQ-009.

## 2026-09-18 — One query interface for both engines

- **Choice:** sqlc emits `pgdb.Querier` for Postgres. `tools/buildtool sqladapter` generates `db/sqlite/adapter.gen.go`, which implements the same interface on the SQLite queries by struct conversion. SQLite columns use the type names `UUIDTEXT` and `JSONTEXT` (TEXT affinity), so the generated types match Postgres field for field. `store.DB.Queries()` and `store.Tx.Queries()` return the interface for the engine.
- **Alternative:** A hand-written adapter per feature.
- **Reason:** SDD §11.2 allows the interface. Generation removes the duplicate code, and a schema drift between engines fails the build.

## 2026-09-18 — Bundles table additions

- **Choice:** `bundle` has `main_doc` and `archived_at` columns beyond SDD §11.1. `archived_at` is set when a local bundle's folder is gone. Share and visibility columns arrive at M8.
- **Alternative:** Derive the main doc from the files on each read; delete rows of gone folders.
- **Reason:** The bundle list needs the main doc without reading blobs. Archiving keeps the versions of a folder that comes back.

## 2026-09-18 — Optimistic version check on every change

- **Choice:** Every file change sends `base_version`. The server moves the bundle head only from that version (`UpdateBundleHead ... WHERE current_version_id IS base`). In local mode, the server scans the disk before the check, so an edit on disk that the watcher has not seen yet also counts. A mismatch is a 409 with code `version_conflict`.
- **Alternative:** Last write wins.
- **Reason:** DEC-004: one editor at a time. A save never overwrites a change it did not see.

## 2026-09-18 — Reads come from versions

- **Choice:** File lists and file content come from the version blobs in the store, in local mode too. The watcher keeps versions in step with the disk (full rescan, 300 ms quiet period).
- **Alternative:** Read local files from disk on each request.
- **Reason:** One read path for both sources, and old versions stay readable for diffs.

## 2026-09-18 — Section hash details

- **Choice:** A section's own content starts after the heading line (after the underline for a setext heading). Only top-level headings start sections; headings in lists, quotes, and code do not. Normalization also trims leading and trailing blank lines. The text before the first heading is a level-0 section with an empty path.
- **Alternative:** Include the heading line in the hash.
- **Reason:** A waiver keys on the heading path and the hash. Renaming a heading changes the path, not the content.

## 2026-09-18 — Preview rendering

- **Choice:** `POST /render` returns HTML where each block has `data-src-start`, `data-src-end` (byte offsets into the file), and `data-line`. Raw HTML is dropped, and dangerous URLs are emptied. Code is highlighted on the server with chroma CSS classes. Mermaid blocks are `<pre class="mermaid">`, drawn by the client with `securityLevel: strict`. Relative images load through the file content endpoint, which sends a sandbox CSP.
- **Alternative:** Client-side highlighting.
- **Reason:** DEC-017 and SDD §14.3.

## 2026-09-18 — Markdown in the editor

- **Choice:** The editor uses `@lezer/markdown` with GFM directly, plus a small list-continuation command.
- **Alternative:** `@codemirror/lang-markdown`.
- **Reason:** `lang-markdown` bundles the HTML, CSS, and JavaScript languages and autocomplete, about 60 kB gzipped, for features the editor does not need. The editor loses highlighting inside fenced code; the preview still highlights it.

## 2026-09-18 — JavaScript budget measures the bundle route

- **Choice:** `buildtool budget` counts the entry, the bundle route's split chunks (`src/routes/bundles/$bundleId/index.tsx`), and their static imports. Dynamic imports (Mermaid, panels loaded on demand) are excluded. At M2 the total is 282 kB of 400 kB (SDD §13.4, raised from 300 kB on 2026-09-18).
- **Alternative:** Count the entry only.
- **Reason:** SDD §13.4 sets the budget for the bundle route.

## 2026-09-18 — New versions reach the UI by polling

- **Choice:** The bundle page polls the bundle every 2 seconds. A new version reloads a clean editor, or shows a banner over unsaved edits.
- **Alternative:** Server-sent events now.
- **Reason:** SSE arrives with run progress (REQ-026). Polling a local server is cheap.

## 2026-09-18 — Export and print at M2

- **Choice:** M2 has the `.zip` export and PDF through print CSS (the preview only). The HTML report arrives with the verdict (M12).
- **Alternative:** An HTML export without a verdict.
- **Reason:** SDD §18 puts HTML export at M12.

## 2026-09-18 — Development data

- **Choice:** `just dev` serves `build/dev-bundles`, a copy of `testdata/bundles` made on first run.
- **Alternative:** Serve `testdata/bundles` directly.
- **Reason:** Edits in dev must not change the fixtures that tests use.

## 2026-09-18 — UI building blocks

- **Choice:** Components built from the design tokens on `radix-ui` primitives, with `lucide-react` icons and self-hosted Inter and JetBrains Mono (`@fontsource-variable`). No shadcn component is copied yet.
- **Alternative:** shadcn/ui components as generated.
- **Reason:** The design-direction rule forbids the default component look. The M2 screens need a button, a dialog, a menu, and an input; shadcn arrives when a component needs more than a primitive.

## 2026-09-19 — Required headings in a template

- **Choice:** A template heading line that ends with `<!-- required -->` is required (`lint.required-headings`). A title matches at any level, ignoring case. New docs from the template drop the marker.
- **Alternative:** A list of required headings in the profile YAML.
- **Reason:** The template stays the one place that shows the doc's shape, and the marker is invisible in rendered markdown.

## 2026-09-19 — Profile versions in local mode

- **Choice:** Profiles are the built-ins with `.speccy/profiles/*.yaml` over them. On each load, a profile whose YAML or template text differs from its latest stored version gets a new version. `profile_version` stores the template text and the origin as well as the YAML. A file that does not load is listed on the Profiles endpoint, and the last good set stays in use.
- **Alternative:** Version by file modification time.
- **Reason:** REQ-012: the content decides the version, so the same file gives the same version.

## 2026-09-19 — Lint details

- **Choice:** Prose is the text of paragraphs, list items, headings, and table cells; code, raw HTML, links' destinations, and placeholders are not prose. Requirement items (for `lint.rfc2119-case`) define a REQ or NFR ID, or an ID with a covered prefix. A reference with a covered prefix (REQ in an SDD) is never dangling here; linked bundles resolve it at M7. `WORD-123` is never an acronym. The slop and weasel lists are Speccy's own; no third-party list is imported.
- **Alternative:** Lint the whole text with regular expressions.
- **Reason:** Rules that read the parse tree do not fire on code and links, and the positions match the preview (DEC-017).

## 2026-09-19 — Placeholders in the parser

- **Choice:** The shared goldmark configuration has an inline parser for `<…>` text that is not an HTML tag, a URL, or an e-mail address. Lint finds placeholders by node type, and the preview shows them highlighted.
- **Alternative:** Find placeholders in lint only, with a regular expression.
- **Reason:** Without it, `<Product name>` parses as raw HTML, which the preview drops, so the author sees an empty line.

## 2026-09-19 — The lint-only verdict

- **Choice:** Each new version gets a run of kind `lint`. Items for the score are the lint rules, plus `links.has-upstream` when the profile requires an upstream link. M3 checks that link in the frontmatter (a link of a required kind, or a `standalone` reason); M7 resolves the target. Radar: structure (placeholder, required headings, broken links, IDs, limits, asset nudge) and clarity (slop, sentence length, weasel words, acronyms, RFC 2119 case, passive voice). A doc whose type has no profile gets a failed run that says how to add one.
- **Alternative:** No verdict until the AI stages exist.
- **Reason:** SDD §18 M3 asks for a lint-only verdict.

## 2026-09-19 — Adoption mode

- **Choice:** `.speccy.yaml` `adoption.relaxed` makes each listed check report at INFO. Findings keep a `relaxed` flag. A relaxed `links.has-upstream` also stops the upstream rule of §8.6 from blocking. The count shown is the relaxed slugs that are real checks of the profile.
- **Alternative:** Hide relaxed findings.
- **Reason:** REQ-133: relaxed checks still appear as findings.

## 2026-09-19 — One JSON column type

- **Choice:** `db/dbtype.JSON` scans text or bytes and writes text. sqlc maps `jsonb` (Postgres) and `JSONTEXT` (SQLite) to it.
- **Alternative:** `json.RawMessage`.
- **Reason:** SQLite returns a JSON default as a string, which `json.RawMessage` cannot scan, and `[]byte` values made SQLite store BLOBs. The conformance suite checks the default case.

## 2026-09-19 — Golden files for the fixtures

- **Choice:** `testdata/bundles/<slug>.golden.json` holds each fixture bundle's verdict, score, and findings (check, level, quote). `go test ./internal/features/review -run Golden -update` rewrites them.
- **Alternative:** Assertions in code for each fixture.
- **Reason:** BUILD §5 names the golden layer. A diff shows any change in lint output.

## 2026-09-19 — `speccy profile validate` waits for M11

- **Choice:** The profile schema and its path-by-path errors are built (REQ-014's logic). The command itself arrives with the CLI at M11.
- **Alternative:** Add the command now.
- **Reason:** SDD §18 puts all §12.2 commands at M11.

## 2026-09-19 — Agent CLI presets (§19 Q4, resolved)

- **Choice:** Four presets, each in a temporary folder that holds only the bundle and `prompt.md`:
  - `claude` (2.1.277, live call): `claude -p --output-format json --json-schema {schema} --tools "" --no-session-persistence --strict-mcp-config --model {model}`; prompt on stdin; answer in `structured_output`; tokens in `usage`.
  - `cursor-agent` (docs only, no subscription on the build machine): `cursor-agent -p --output-format json --mode ask --model {model} "<follow prompt.md>"`; answer in `result`; no token counts, so they are estimated.
  - `opencode` (1.18.26, live call): `opencode run --format json --pure --agent plan -m {model} -f prompt.md -- "<follow prompt.md>"`; JSON lines; answer in `text` events; tokens in `step_finish`. Its plan agent adds about 12,000 input tokens per call.
  - `pi` (0.85.1, live call): `pi -p --mode json --no-tools --no-session --no-context-files --no-extensions --no-skills --model {model}`; prompt on stdin; answer and tokens in the `agent_end` event.
- **Alternative:** Presets for claude and cursor-agent only.
- **Reason:** The owner asked for pi and opencode. SDD REQ-102 and §19 Q4 were changed with the owner's approval. `testdata` holds the real outputs, and the parser tests read them.

## 2026-09-19 — Structured output per backend

- **Choice:** Anthropic: `output_config.format` with the JSON schema, through the official Go SDK. OpenAI and OpenRouter: `response_format` `json_schema` (not strict). DeepSeek, cursor-agent, opencode, pi, and custom CLIs: the schema goes in the prompt. The gateway validates every answer against the schema in all cases.
- **Alternative:** Trust the providers that enforce a schema.
- **Reason:** DEC-014 needs one rule for all backends; the providers differ in what they enforce.

## 2026-09-19 — One HTTP client for OpenAI-compatible APIs

- **Choice:** OpenAI, OpenRouter, and DeepSeek use one small `net/http` client for `/chat/completions`, with a base URL per kind. Anthropic uses the official SDK.
- **Alternative:** The official OpenAI Go SDK.
- **Reason:** Three providers share the same endpoint; one small client covers them without a large dependency.

## 2026-09-19 — Gateway rules

- **Choice:** The gateway owns retries (the SDK's are off): 2 retries with 1 s and 2 s backoff (plus up to 25% jitter) for HTTP 429 and 5xx; 1 retry when the answer is not JSON or breaks the schema; a call that passes 120 s fails with no retry. Tokens count against the budget after every call, including a call whose answer fails the schema. The budget check runs before each attempt.
- **Alternative:** Retry timeouts as well.
- **Reason:** REQ-103 lists retries for 429 and 5xx only; a 120 s call that timed out is likely to time out again.

## 2026-09-19 — Budget and prices

- **Choice:** `budget` has one row per workspace and UTC month. A new month copies the last month's limit. No limit means no budget. Each role assignment has optional prices per million input and output tokens, for the cost estimate that arrives with full runs (M5).
- **Alternative:** A built-in price table.
- **Reason:** Prices change often and depend on the plan; a built-in table would go stale.

## 2026-09-19 — Secrets

- **Choice:** `kernel.Sealer` (AES-256-GCM, a random nonce in front of each ciphertext). Local mode keeps the key in `.speccy/state/key`, created with mode 0600; a looser mode is refused. Hosted mode will read `SPECCY_MASTER_KEY` (M8). The API returns only `has_secret` and the last 4 characters, and an update without a secret keeps the stored one.
- **Alternative:** OS keychain.
- **Reason:** SDD §14.1.

## 2026-09-19 — Admin screen in local mode

- **Choice:** An Admin screen for backends (with a Test button that makes one short call), role assignments, and the monthly budget. Local mode's one user is an admin (SDD §3), so it always shows. The fake backend is for tests only and never appears in the API.
- **Alternative:** Configuration files only.
- **Reason:** REQ-101 and REQ-104 name an admin; PMs in local mode need a screen, not a YAML file.
