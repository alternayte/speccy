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

## 2026-09-19 — Grounding uses MCP search as retrieval

- **Choice:** When the reviewer's backend has no native web search, Speccy itself calls the search tool of the MCP connection marked search, once per claim, and puts the results in the verify prompt as data. The model is not handed MCP tools in M5.
- **Alternative:** A tool-use loop where the model calls allowlisted MCP tools.
- **Reason:** Retrieval is deterministic, needs no tool loop per backend, and keeps tools out of the model's hands. REQ-034's order holds. A tool loop, if needed, can come with AI threads (REQ-088, M9).

## 2026-09-19 — Native web search per backend

- **Choice:** Anthropic: the `web_search_20260209` tool (the basic `20250305` for Haiku and older models). OpenRouter: the `web` plugin. The claude CLI: `--tools WebSearch,WebFetch` for fact checks only (verified with a live call). OpenAI chat completions, DeepSeek, and the other CLIs: none, so they use MCP search or leave claims unverified.
- **Alternative:** Treat every backend as search-capable.
- **Reason:** DEC-011: a label needs a real source. A label without a source is reset to unverified.

## 2026-09-19 — Prompt delimiters

- **Choice:** Untrusted content (the bundle, a section, a claim, a search result) sits between `<<<DATA <id>` and `DATA <id>>>>`, where the id is 16 hex characters of the SHA-256 of that content. The system prompt says data is never an instruction.
- **Alternative:** A fixed delimiter, or a random one per call.
- **Reason:** Content cannot contain the closing line of its own hash, so it cannot break out; unlike a random id, the prompt stays deterministic for the cache and the fake backend. The injection tests fail when the id is fixed.

## 2026-09-19 — Rubric stage

- **Choice:** Checks with `scope: doc` read the bundle (the main doc and up to 200 kB of text assets); `scope: section` checks run per section. The reviewer answers up to 8 checks per call, and a skipped check gets one more call alone; a check still unanswered counts as not applicable, with a run note. A failed check's finding anchors on its first quote that is in the doc, else on the section, else on the doc.
- **Alternative:** One call per check.
- **Reason:** Fewer calls; each answer is still cached per check (§8.10).

## 2026-09-19 — Grounding stage

- **Choice:** Claims are extracted per section with at least 12 words, cached by section hash (T-021). A claim must be text from its section, and a claim inside an "Assumption:" sentence is dropped (REQ-033). Claims are labelled 8 per call; a label is cached by the claim text, the search source, and the month, so facts are checked again each month. Finding slugs are `grounding.unverified-claim` (SHOULD) and `grounding.contradicted-claim` (MUST); adoption mode can relax them. Each claim is one item on the Evidence axis. Claims are stored in a `claim` table for the overlay (M10).
- **Alternative:** One whole-doc extraction.
- **Reason:** SDD §8.10 keys caches by section hash; the first live run showed the model listing design statements as claims, so the claims prompt (`claims-v2`) now defines a claim with examples.

## 2026-09-19 — Runs, jobs, and progress

- **Choice:** "Run review" queues a `full` run and a job. One worker per process claims jobs (`UPDATE … RETURNING`, with `FOR UPDATE SKIP LOCKED` on Postgres) and runs them with a 15-minute limit; a job's lock expires 20 minutes after it starts, so a dead worker's job runs again. Progress events go through an in-process broker to `GET /runs/{id}/events` (server-sent events). A run records roles, prompt versions, tokens, cost from the role prices, cache hits, and notes.
- **Alternative:** Run the review inside the HTTP request.
- **Reason:** SDD §7.2 and §7.3. SSE fan-out in one process matches §19 Q10.

## 2026-09-19 — Failed runs and the verdict

- **Choice:** A failed run stores no verdict and names the stage and the cause (REQ-024). While the newest finished run on the current version has failed, the bundle shows its last verdict as stale, with the error.
- **Alternative:** Show the last good verdict unchanged.
- **Reason:** REQ-024: the previous verdict becomes stale.

## 2026-09-19 — Cost estimate

- **Choice:** The estimate counts the rubric batches and section extractions that the cache cannot answer, plus about one label call per two sections, with a token size from the bundle text. It shows a dollar cost only when the reviewer role has prices.
- **Alternative:** A dry run.
- **Reason:** REQ-104 needs a figure before the run, not an exact one.

## 2026-09-19 — MCP connections

- **Choice:** Stdio (command, with the secret as a named environment variable) or streamable HTTP (bearer token). The allowlist refuses a tool that the server marks destructive; tools that are not marked read-only are allowed, with a warning in the UI. A search connection names one allowlisted tool; Speccy finds its query argument from the tool's schema.
- **Alternative:** Only tools marked read-only.
- **Reason:** Many servers do not annotate tools; the admin decides, and a server that says a tool changes data is refused.

## 2026-09-19 — Parallel calls

- **Choice:** 4 model calls at a time per run (REQ-105), as `review.Service.Parallel`. The setting reaches the admin screen with the other workspace settings (M8).
- **Alternative:** An environment variable now.
- **Reason:** One default serves local mode; a setting belongs with the admin settings.

## 2026-09-19 — Divergence roles

- **Choice:** A full run needs a model for each reader role the profile uses, and for the judge when there are two or more readers. An unassigned role stops the run before it starts, and the estimate dialog links to Admin. `divergence.readers` is at most 3, because REQ-101 names three reader roles.
- **Alternative:** Fall back to the reviewer's model for an unassigned role.
- **Reason:** REQ-101: an admin assigns each role. A silent fallback would hide low reader diversity (REQ-046) behind a default.

## 2026-09-19 — Build questions

- **Choice:** The reviewer writes the questions in one call, with the heading paths of the doc in the prompt. A cite is a trace ID that the doc defines or mentions, or a heading path (or a unique heading title). A question with no valid cite is dropped, with a run note. A question is MUST when a cite is a definition whose text has "MUST", or a section whose title the template marks as required (REQ-041). An upstream ID (REQ in an SDD) gives SHOULD until M7 resolves linked bundles. Questions, their level, and their anchor are pinned per version in `question` (REQ-047).
- **Alternative:** Let the model give each question its level.
- **Reason:** REQ-041 defines the level from the doc; deterministic code applies it.

## 2026-09-19 — Readers and the quote check

- **Choice:** Each reader answers up to 10 questions per call, with its own system prompt that says nothing of the rubric. A question the reader skips gets one more call; still missing, it is NOT SPECIFIED, with a run note. Answers are cached per reader role, model, bundle hash, and question text. A quote is found when `anchor.Find` (whitespace and emphasis normalized) finds it in any text file of the bundle. An answer with no found quote is NOT SPECIFIED (REQ-043).
- **Alternative:** One call per question per reader.
- **Reason:** Fewer calls. A reader still sees only the bundle and the questions (REQ-042).

## 2026-09-19 — The judge

- **Choice:** The judge runs only when every reader answered and there are two or more readers; the §8.5 table needs no groups in the other cases. Answers carry letters in an order that depends only on their content, so the prompt is stable for the cache. The answer schema puts `analysis` before `groups`, so the model compares before it groups. `divergence.Groups` repairs the grouping: an answer that the judge leaves out gets its own group.
- **Alternative:** A `reason` field after the groups.
- **Reason:** A live run on DeepSeek gave groups that contradicted its own reason ("10 s" and "10 seconds" split). With `analysis` first and a rule for units and extra detail (`judge-v3`), the same answers group together.

## 2026-09-19 — Divergence results

- **Choice:** Finding slugs `divergence.ambiguous` (diverge) and `divergence.gap` (gap), at the question's level, anchored on the first cite. Each question is one item on the Precision axis. Answers are stored in `answer`; the groups and result in `question_result`, a table beyond SDD §11.1. `GET /runs/{runId}/questions` returns them, with readers named by number (DEC-013). Evidence never names a model.
- **Alternative:** Keep results only in finding evidence.
- **Reason:** Questions the readers agree on have no finding, and the run report lists every question.

## 2026-09-19 — Review rail width

- **Choice:** The review rail is 320 px (`--review-rail`); the file explorer stays 248 px (`--rail`).
- **Alternative:** Shorter tab labels.
- **Reason:** Four tabs (Findings, Evidence, Questions, Versions) at caps tracking need about 300 px.

## 2026-09-19 — Link targets

- **Choice:** A frontmatter target is a bundle slug first. Otherwise, it is a path relative to the main doc's folder, and it matches a bundle folder, a main doc file, or a single-file bundle's file. A target with `://` is external (DEC-021) and nothing reads it. A link rule path is a single-file bundle's file or a folder bundle's folder, relative to the root; `{name}` matches within one path segment. A link to the same bundle with the same kind appears once; frontmatter wins over a rule.
- **Alternative:** Slugs only.
- **Reason:** SDD §10.2 allows "a relative path in local mode"; REQ-132 rules name files.

## 2026-09-19 — Stored links

- **Choice:** The `link` table holds the links of each bundle's current version. Every lint pass resolves every bundle's links and rewrites the rows that changed, because a new link rule or a new target bundle changes links without a new version. `trace_item` (SDD §11.1) is not built: trace IDs are read from the doc text when they are needed.
- **Alternative:** Store links only when a bundle gets a new version; store trace items per version.
- **Reason:** Stale link rows would give a wrong matrix. Reading IDs from text is fast and cannot drift.

## 2026-09-19 — Where coherence checks run

- **Choice:** Coverage (REQ-053), restatement (REQ-055), and dangling upstream references run with lint on every save, because they are deterministic. Contradiction (REQ-054) needs a model, so it runs only in a full run, in the coherence stage after divergence. A standalone doc (REQ-057) has no coherence checks.
- **Alternative:** All coherence checks in full runs only.
- **Reason:** DEC-027 limits model calls, not deterministic checks. An author sees a coverage gap at once.

## 2026-09-19 — Which links each check reads

- **Choice:** Coverage reads `implements` links (REQ-053). Restatement and dangling references read `implements` and `refines`. Contradiction reads `implements`, `refines`, and `references`. `supersedes` sets no check; the status change it causes arrives with bundle status (M9). Checks run from the doc that declares the link; findings anchor in that doc, and the evidence holds the other doc's anchor.
- **Alternative:** Findings in both bundles' runs.
- **Reason:** One run reviews one bundle. The evidence keeps the second anchor, so the finding still points at both docs (REQ-054).

## 2026-09-19 — Coverage and acknowledgements

- **Choice:** An upstream ID is covered when the downstream doc mentions it anywhere, or when `trace:` in the frontmatter acknowledges it: `out_of_scope` with a reason, or `covered_by` with a target and a reason. Each upstream ID is one Coherence item. A gap is one `trace.coverage` finding per ID, anchored on the frontmatter. Approval of acknowledgements follows the waiver policy at M9; until then the frontmatter is honoured as written (SDD §9.3).
- **Alternative:** Check that a `covered_by` target covers the ID.
- **Reason:** REQ-053 asks for a reference or an acknowledgement; M9 adds who may approve one.

## 2026-09-19 — Restatement measure

- **Choice:** Words are lower-cased with punctuation removed. The overlap is the share of a downstream paragraph's distinct 8-word shingles that are in one upstream paragraph; above 0.5 is a `coherence.restatement` SHOULD finding. A paragraph under 8 words is never checked.
- **Alternative:** Jaccard similarity of the two paragraphs.
- **Reason:** A short downstream paragraph that copies part of a long upstream one is still a restatement.

## 2026-09-19 — Upstream staleness

- **Choice:** `run_link` records the linked versions a run read. A verdict is stale with the reason `upstream_changed` when one of them is no longer current. A lint verdict is linted again at once, because lint is cheap. A full verdict stays stale until the next full run, so its model results stay visible.
- **Alternative:** Lint again and hide the full verdict.
- **Reason:** REQ-056 says the verdict becomes stale; it does not say to replace it.

## 2026-09-19 — Contradiction check

- **Choice:** One reviewer call per linked doc, with this bundle and the other main doc as data (`contradiction-v2`). Each conflict has `analysis` and `both_can_hold` before the quotes; a conflict with `both_can_hold` true, or with a quote that is not in its doc, is dropped. Slug `coherence.contradiction`, MUST.
- **Alternative:** Ask for the conflicts only.
- **Reason:** In a live DeepSeek run, `contradiction-v1` reported an added detail ("up to 3 times" against a PRD with no number) as a MUST conflict. With v2, the same docs give none, and a real conflict (10 against 5 working days) is still found.

## 2026-09-19 — Trace ID suggestions

- **Choice:** Speccy suggests an ID for each top-level list item without one, under a heading that contains "requirement" (REQ), "non-functional", "nonfunctional", or "quality" (NFR), or "decision" (DEC), for the profile's prefixes. Numbers continue after the highest number the doc uses. The trace page lists them; `POST /bundles/{id}/trace/ids` inserts the chosen ones as `**ID:** ` at the item start, from a base version, as a new version.
- **Alternative:** Ask a model which items are requirements.
- **Reason:** REQ-136 counts a suggested trace ID as a deterministic fix, so the suggestion must be deterministic too.

## 2026-09-19 — Traceability view

- **Choice:** A page per bundle (`/bundles/{id}/trace`) with the links both ways, one matrix for the bundle's own IDs when others implement it, and one for each bundle it implements. Rows are the upstream IDs with a prefix that a downstream profile covers; columns are the implementing bundles.
- **Alternative:** A tab in the review rail.
- **Reason:** A matrix needs width; the rail is 320 px.

## 2026-09-19 — auth-all in hosted mode

- **Choice:** auth-all v0.5.1 with email and password, the roles plugin (member, admin), the admin plugin, the API keys plugin (`spy_` tokens), optional OIDC (ID `oidc`) and GitHub, and Speccy's `internal/authinvite` plugin. auth-all tables have the prefix `auth_` and UUID keys; their migrations run from auth-all's export in their own goose table, `auth_goose_db_version`, out of order allowed, because enabling a feature adds a unit with an older version. The workspace role is auth-all's user role, so SDD §11.1's `membership` and `api_token` tables are not built.
- **Alternative:** Speccy's own `membership` and `api_token` tables.
- **Reason:** DEC-016. auth-all keeps the role on the user and caps an API key at its owner's role, so a second copy would drift. The live dev database hit the out-of-order case when the rate limiter was added.

## 2026-09-19 — Invites, reset links, and sign-up

- **Choice:** `internal/authinvite` stores invites and reset links in Speccy's `invite` and `reset_link` tables (SHA-256 of 32 random bytes). A link carries its token in the URL fragment. Accepting an invite spends it in one conditional update, then creates the user with the invite's role and password through the admin plugin, then issues a session; when the account cannot be made (an address in use), the invite stays usable. A reset link sets a new password through the admin plugin, which ends every session. An `OnBeforeUserCreate` hook refuses every user creation that Speccy did not start, so there is no open sign-up and no account from a first OAuth sign-in. Passwords need 12 characters.
- **Alternative:** auth-all's organization invitations.
- **Reason:** Those need an account whose email matches, and Speccy has no open sign-up. No mail server (DEC-016).

## 2026-09-19 — OAuth providers link, never auto-link

- **Choice:** OIDC and GitHub sign in a person who linked the provider under Account after a password sign-in. auth-all's email auto-link stays off.
- **Alternative:** Link by a verified provider email.
- **Reason:** An invite does not prove the email, so an auto-link could hand an account to whoever holds the provider address.

## 2026-09-19 — The role table

- **Choice:** One map in `internal/http/authz.go` from each operationId to an access level: public, reader, member, bundle read, bundle AI, bundle edit, admin. A strict-server middleware applies it before the handler; an operation with no row is refused. A hidden bundle answers 404, not 403. Bundle edits are for authors and admins; members read internal and link bundles and run reviews on them. The actor comes from auth-all (session or API token), else the guest cookie, else nobody; local mode sets the local admin on every request, and a request with no actor has no permission.
- **Alternative:** Checks inside each handler.
- **Reason:** SDD §14.2 asks for one table in code. T-041 reads api/openapi.yaml, checks the table is complete, and calls every endpoint as five actors.

## 2026-09-19 — Visibility, share links, and guests

- **Choice:** `bundle.visibility` (default internal), `share_token_hash`, and `share_expires_at`. A new share link replaces the old one and sets link visibility; leaving link visibility revokes it. A guest enters a display name; the guest cookie holds the guest ID, the share token hash, and an expiry of 30 days, with an HMAC under a key derived from the master key. A new or revoked link ends the guest at once. A guest reads the shared bundle only. Named members of a private bundle are its reviewers; assigning reviewers arrives with M9 (REQ-090).
- **Alternative:** A server-side guest session table.
- **Reason:** REQ-086 asks for a signed cookie.

## 2026-09-19 — Workspace settings

- **Choice:** `workspace.settings` holds the file and bundle limits (REQ-009, up to 50 MB and 500 MB), the invite expiry (REQ-081), and the parallel model calls (REQ-105). The limits apply to hosted mode; local mode keeps 10 MB and 50 MB.
- **Alternative:** Environment variables.
- **Reason:** REQ-009 and REQ-081 name an admin.

## 2026-09-19 — Rate limits

- **Choice:** auth-all's store limiter (counts in the database) in strict mode, with auth-all's sign-in rules, and per-address limits for TOTP (10 a minute), password change (10 in 15 minutes), invite and reset checks (30 a minute), invite acceptance and reset (10 an hour). Expired counters are removed every hour.
- **Alternative:** The in-memory limiter.
- **Reason:** SDD §14.2. Every instance shares one count.

## 2026-09-19 — Hosted development

- **Choice:** `compose.yaml` runs Postgres 17 on 127.0.0.1:55432. `just dev-hosted` runs the server in hosted mode behind Vite, with a fixed development master key and base URL http://127.0.0.1:5173; `just invite <role>` prints an invite for it. `internal/app` builds the services for both modes, so tests use the same wiring as the binary.
- **Alternative:** Docs that list the steps.
- **Reason:** BUILD §3 names `just dev-hosted`.

## 2026-09-19 — Event-sourced collaboration

- **Choice:** Threads, waivers, and bundle status are streams (DEC-008) with pure decide and evolve functions in `features/thread`, `features/waiver`, and `features/approval`. `es.Run` loads the snapshot, decides, evolves, and appends with the version check, and decides again on a conflict, up to 3 times. A projection now receives the new snapshot with the events; each view is written from the snapshot in the append transaction. The bundle status stream ID is the bundle ID; a bundle with no stream is a draft.
- **Alternative:** A handler per command that writes the views itself.
- **Reason:** DEC-008. The M1 entry deferred the helper until three aggregates showed its shape.

## 2026-09-19 — Waivers in the verdict

- **Choice:** A finding is waived when a frontmatter waiver names its check and its section path, and the waiver's `section_hash` equals the section's hash now; an empty section is the whole doc body. A waiver added by hand counts the same way (DEC-009, §9.3). The waiver's section is the finding's heading path. A final approval writes the frontmatter first, as a new version, then records WaiverApproved; the approval fails if the section changed since the request. After every change, an approved waiver whose hash no longer matches gets WaiverInvalidated. `finding.waived` stores the result; a check's item counts as waived when all its findings are.
- **Alternative:** Read waivers from waiver_view.
- **Reason:** DEC-009: waivers travel with the doc to git and CI.

## 2026-09-19 — Blocking threads and the verdict

- **Choice:** A run counts open blocking threads when it saves its verdict. The bundle's summary also counts them at read time: a thread marked blocking later makes the verdict Not Build Ready at once, and resolving it restores the run's result.
- **Alternative:** Run the lint stage again on each thread change.
- **Reason:** §8.6 rule 2 holds now, not only at run time; a thread change makes no new version.

## 2026-09-19 — Approvals

- **Choice:** Authors and admins request a review and name reviewers; reviewers become `bundle_reviewer` rows, which are also a private bundle's named members. Any member except an author approves the current version when its verdict is Build Ready now; the bundle is approved when the approvals of that version reach `approvals.required`. After every change, a bundle whose approvals are for another version gets ApprovalsRevoked. A `supersedes` link marks its target superseded.
- **Alternative:** Only named reviewers approve.
- **Reason:** REQ-076 names "human approvals" by non-authors; REQ-090 uses reviewers for the inbox.

## 2026-09-19 — Threads and the AI

- **Choice:** A text anchor comes from the client as a file and a byte range of the saved file; the server builds the anchor. Guests open and write threads for humans only, at most 30 messages an hour (REQ-086); decisions, blocking, and resolving are for members. An AI thread queues a `thread_answer` job on the review worker; the writer role answers from the thread, the bundle, and its linked docs as data, with web search or MCP search results, and lists its sources (DEC-011). Mentions are `@` and an email, or the part of the email before the `@`.
- **Alternative:** Answer in the request.
- **Reason:** A model call can take 2 minutes (REQ-103).

## 2026-09-19 — Inbox and insights

- **Choice:** The inbox is computed on read for the last 30 days: review requests the caller has not approved, messages on bundles they author, mentions, and finished full reviews of their bundles. `user_state.inbox_seen_at` marks what is read. Insights count full runs only: a lint run happens on every save.
- **Alternative:** A stored notification table.
- **Reason:** REQ-091 is in-app only; one query path cannot drift from the data.

## 2026-09-19 — Hosted profile editing

- **Choice:** In hosted mode the store holds the profiles; the built-ins seed a doc type with no profile. Maintainers of a profile and admins save YAML and a template as a new version after schema validation; admins create profiles and name maintainers. In local mode a save writes `.speccy/profiles/<key>.yaml` and its template. Rubric suggestions are threads on a profile, anchored to a check (REQ-015).
- **Alternative:** Profiles as files in hosted mode too.
- **Reason:** The owner chose to build the editor in M9. Hosted mode has no persistent disk (DEC-028).

## 2026-09-19 — Review input hashes leave out waivers

- **Choice:** `bundleHash` hashes the main doc without its frontmatter `waivers:` key, and a version with the same input reuses the pinned build questions.
- **Alternative:** Hash every byte.
- **Reason:** A waiver approval writes a new version that no model reviews differently; without this, the next full review would call every doc-scope step again.

## 2026-09-19 — Rail tabs

- **Choice:** The review rail has Findings, Threads, Evidence, and Versions. Evidence holds the claims, the assumptions, and the build questions.
- **Alternative:** Five tabs.
- **Reason:** Five tabs do not fit 320 px.

## 2026-09-19 — Re-anchoring

- **Choice:** Findings of a run on an older version, and text threads, move to the current version when they are read (SDD §8.8). The fuzzy step searches only the own content of the section with the same heading path, and accepts a match that differs from the quote by at most 30% of its length. A whole-doc anchor on the frontmatter follows the frontmatter. A detached anchor keeps its old text and shows in the Detached list.
- **Alternative:** Store a re-anchored copy per version.
- **Reason:** Reading is cheap, and a stored copy can drift from the text.

## 2026-09-19 — Overlay

- **Choice:** Each finding has at most one layer: ambiguous (divergence and gaps), contradicted (contradicted claims and conflicts between docs), unverified, risk (other MUST findings), and slop (other lint findings). The preview underlines the quote with the CSS Custom Highlight API and puts an icon beside the block. A quote over 300 characters gets the icon only. The switched-on layers are kept in the browser.
- **Alternative:** Wrap the quote in marks inside the server's HTML.
- **Reason:** The server HTML is shared by preview and print; highlights leave its DOM as it is.

## 2026-09-19 — Tour

- **Choice:** The tour is its own feature (`features/tour`) built from the verdict's run, the threads, and the waivers. MUST findings that need a decision are divergence, gaps, contradictions, contradicted claims, uncovered trace items, and a missing upstream link. An open decision is an open thread for humans that is not blocking and has no decision. A decision in the tour posts to the point's thread, or opens a thread on the finding, and marks the message as the decision.
- **Alternative:** Put all MUST findings in the tour.
- **Reason:** SDD §13.3: findings that the author can fix without a decision are not tour points.

## 2026-09-19 — Suggested fixes

- **Choice:** The writer role returns one patch: an exact text that occurs once in the file, and its replacement. A patch that does not apply gets one more try, then the request fails. The patch is stored on the finding; accept applies it to the current version only if the text still occurs once, then clears it. The request is synchronous.
- **Alternative:** A unified diff from the model.
- **Reason:** Models write unreliable diff hunks; an exact replacement can be checked.

## 2026-09-19 — Diff summary and run report

- **Choice:** The diff summary sends the section diff of the main doc and the diff of other text files to the writer role, cached by the content of both versions. The finding change compares the full reviews of both versions, or their lint runs when one has no full review; findings match by check, message, and quote. Runs record when each stage started and ended in `review_run.stages`.
- **Alternative:** Compare a full review with a lint run.
- **Reason:** A lint run has no model findings, so the comparison would report them as fixed.

## 2026-09-19 — Gauntlet capture

- **Choice:** `just gauntlet <run>` builds the binary and runs `buildtool gauntlet`. It runs local mode over a copy of `testdata/bundles` plus a PRD with one SHOULD finding, and hosted mode on a fresh `speccy_gauntlet` database in the compose Postgres. It seeds each state of BUILD.md §6.2 through the API and captures it with agent-browser. The full reviews and the diff summary call the local claude CLI; `GAUNTLET_MODEL` sets the model (default haiku). The empty and loading states come from a `fetch` override in the page, and the error state from an aborted request.
- **Alternative:** Seed a database by hand and commit it.
- **Reason:** The states must come from the current code; a stored database goes stale with each migration.

## 2026-09-19 — Contrast fixes from the gauntlet

- **Choice:** The light `--ink-3` is #686863, and the tour mutes other sections with `--ink-3`, not with opacity.
- **Alternative:** Keep #75756f and opacity 0.32.
- **Reason:** axe measured 4.05 to 4.47 for #75756f on the light surfaces, and 2.0 for the dimmed tour text; AA needs 4.5.

## 2026-09-19 — speccy review

- **Choice:** A local review syncs the folder with `.speccy.yaml` (or the current folder) into a store: `.speccy/state/` when it exists, else a temporary store that is removed after. It then runs stored reviews through the API in the same process, so links between local bundles resolve. With no reviewer model and no `--stages`, it runs lint only and says so on stderr; an explicit model stage with no model exits 2. `--server` sends each bundle's files to `POST /reviews`, which runs the stages in memory, resolves links against the server's bundles, and stores only the cache. Build questions of unsaved content are pinned in the cache by content hash. The token is in `SPECCY_TOKEN`.
- **Alternative:** Store the `--server` review as a version of the matching server bundle.
- **Reason:** The owner chose "review local text on the server". A run needs a version; the report link for connected mode (M12) can add storage.

## 2026-09-19 — One API for the CLI, TUI, and MCP

- **Choice:** `oapi-codegen` also generates a Go client (`internal/http/api/client.gen.go`). The CLI, the TUI, and the MCP server call the API handler in the same process through `speccyhttp.InProcess`, as the local user. Over `--server` and `/mcp`, the client sends the caller's token, so the role table decides (T-041). The markdown renderer moved to `internal/render`, because the HTML report also uses it.
- **Alternative:** Call the services directly.
- **Reason:** One path means one set of JSON shapes (SDD §12.3) and one role table.

## 2026-09-19 — HTML report

- **Choice:** `GET /bundles/{id}/export?format=html` and `speccy export --format html` write one HTML file: the current verdict, the score by category, the open and waived findings with file and line, and the rendered main doc with its images as data URIs. It has no script, so Mermaid shows as code.
- **Alternative:** Build it in M12 with the Action.
- **Reason:** The owner chose M11: the table in SDD §12.2 lists the command.

## 2026-09-19 — TUI and MCP

- **Choice:** `speccy tui` and `speccy mcp` use `.speccy/state/` and follow changes on disk, as local mode does. The TUI decides nothing: decisions and waivers stay in the app, and the tour screen says so. `e` opens `$VISUAL` or `$EDITOR` at the line (`+N`, or `--goto file:line` for VS Code and Cursor). The TUI logs to `.speccy/state/tui.log`. Over HTTP, MCP is stateless streamable HTTP with JSON answers.
- **Alternative:** Decide and waive in the TUI.
- **Reason:** REQ-122 lists the TUI's jobs; decisions need the thread context of the app.

## 2026-09-19 — GitHub source and publish

- **Choice:** A GitHub source is a repo, a branch, and a folder, read with the workspace's fine-grained token (DEC-019), which is encrypted at rest. The local scan reads an `fs.FS`, so a repo tree scans with the same rules as a folder on disk; blob contents load only when the scan reads them and are cached by SHA. Hosted mode polls every 5 minutes. A bundle's `source_ref` holds its published version and commit: an edit makes the current version differ from the published one, and a sync never overwrites it. When GitHub changes under unpublished edits, the bundle is marked ahead. Publish writes blobs, a tree, a commit, and a branch through the Git data API, and opens a pull request; the source branch never changes. A sync that finds the current text on GitHub marks it published. The admin who adds a source is the author of its bundles.
- **Alternative:** Clone the repo.
- **Reason:** DEC-018: Speccy never runs git, and hosted mode has no persistent disk.

## 2026-09-19 — The GitHub Action

- **Choice:** `action.yml` is a composite action: it downloads the release archive `speccy_<version>_<os>_<arch>.tar.gz`, restores `SPECCY_STATE_DIR` from the Actions cache, runs `speccy action`, and uploads the HTML reports. `speccy action` selects the bundles that hold the pull request's changed files. An inline comment carries a hidden key from the bundle, check, message, and quote, so a moved line does not duplicate it; a push skips keys of open threads and resolves Speccy's threads whose keys are gone (GraphQL, which REST cannot do). Inline comments are MUST findings, and findings with a certain fix at any level, MUST first, up to `pr.inline_limit`. The summary comment has a hidden marker and is updated; a pull request that changes no bundle gets no new one. In advisory mode a Not Build Ready check is `neutral`. CI models come from `SPECCY_MODELS` and `SPECCY_<BACKEND>_API_KEY`; Speccy picks no default model (§19 Q3).
- **Alternative:** A JavaScript action that calls the API.
- **Reason:** One binary does the review and the GitHub calls, and the same command runs on any CI.

## 2026-09-19 — Findings from the dogfood gate

- **Choice:** A required heading matches with or without its section number ("## 5. Decisions" has "Decisions"), in lint and in the divergence levels. A waiver of a doc-scope check (`scope: doc`) is for the whole doc: the model can point the finding at another section on each run, and a section-bound waiver would not follow it. Such a waiver ends on any edit of the body, as REQ-074 says for its section.
- **Alternative:** Ask authors to write template headings without numbers, and to waive per section.
- **Reason:** The SDD of Speccy itself, reviewed with the SDD profile, failed on both: numbered headings are common, and doc-scope findings moved between sections from run to run.

## 2026-09-19 — Release

- **Choice:** goreleaser builds static binaries (`CGO_ENABLED=0`; SQLite is pure Go) for Linux, macOS, and Windows on amd64 and arm64, archives named `speccy_<version>_<os>_<arch>`, and images `ghcr.io/alternayte/speccy` on distroless static as `nonroot`, running `speccy serve --hosted` (SDD §15.1). A pushed `v*` tag runs `.github/workflows/release.yml`. `just release-check` builds a snapshot without publishing.
- **Alternative:** A hand-written build script.
- **Reason:** One config builds the archives the Action downloads and the image, with checksums.

## 2026-09-19 — Overlay defaults

- **Choice:** The Risk and Ambiguous layers are on until a reader chooses; the Writing, Unverified, and Contradicted layers are one click away. The reader's choice is kept in the browser.
- **Alternative:** Every layer on.
- **Reason:** Gauntlet runs 1 and 2: with every layer on, a paragraph can carry four colours of underline, and every critic named it as the largest difference.

## 2026-09-19 — Report of a connected-mode review

- **Choice:** `POST /reviews` keeps the files and the result in `content_review` for 90 days, and returns `id` and `report_path`. The app shows the report at `/reviews/{id}` to workspace members, from `GET /reviews/{id}/report`, which uses the HTML report template. `speccy review --server` prints the link, and the Action links each bundle to it. No bundle or version on the server changes. After 90 days the report returns 404, and the next `POST /reviews` removes the row, with no background job.
- **Alternative:** Store the files as a new version of the server bundle with the same slug, and link to that run's report.
- **Reason:** The owner chose a stored review that changes no bundle. A pull request's text is not the server's text, and a version per push would fill the bundle's history. SDD §12.4 asks for the link.

## 2026-09-19 — Type scale, bundle header, and panel padding

- **Choice:** Titles step by 1.25 from 16px (20, 26, 32px); the bundle title is 20px. In the bundle header, Tour and Traceability are plain links, Share is a quiet button, and the HTML report, the PDF, and the .zip are in one Export menu, so Run review is the one strong action. Finding cards, rail lists, and trace matrix cells have 16px sides and 12 to 14px ends.
- **Alternative:** Keep the 1.2 ratio for every step, and the ten buttons of the same weight.
- **Reason:** Gauntlet run 2, differences 1 to 3.

## 2026-09-20 — The TUI frame

- **Choice:** The TUI draws one frame on every screen: a title bar with the screen and its context, a body that fills the terminal, a status line that is always there, a rule, and a key bar for that screen. `?` opens the key list. The list screen keeps a preview of the bundle under the cursor at the foot, and the bundle screen keeps the selected finding there, so no screen has an empty band and nothing moves when data arrives. Selection is an accent marker and a tinted row. The colours are the web tokens (DEC-029) as adaptive pairs, so a light terminal works. The floor is 80 by 24 cells: below 96 columns the list drops the profile and score columns.
- **Alternative:** Keep the plain list of lines, or draw bordered panes around every region.
- **Reason:** The old TUI wrote a few lines at the top and left the rest of the terminal empty, with no column headers, no focus, and no way to see every key. Borders around everything would cost four columns per pane at the 80-column floor.
- **Test:** `TestView_FrameFitsTheTerminal` checks that every line is exactly as wide as the terminal and the frame is exactly as tall, at four sizes and on every screen. It found the overflowing title bar.

## 2026-09-21 — Waivers and acknowledgements move to the sidecar

- **Choice:** DEC-009 is replaced. A doc's waivers and acknowledgements live in `.speccy/decisions/<doc path>.yaml`, one file per doc. A local bundle reads and writes that file at the root of the served folder. A db or GitHub bundle keeps it among the bundle's files, and a publish writes it to the root of the repo. An approval writes the sidecar and records `WaiverApproved`; the doc text does not change, so no version is made. `review_run.decisions_hash` records the sidecar a run read, so a decision made after the run lints the doc again. `bundleHash` leaves the sidecar out, so the pinned build questions stay.
- **Alternative:** Keep the waivers in the main doc's frontmatter, as DEC-009 said.
- **Reason:** A team that adopts Speccy on an existing repo maps its docs by path (REQ-130). Those docs have no frontmatter and no trace IDs, and Speccy must not write into a doc it did not write. Nobody uses Speccy yet, so one store replaces the other instead of joining it.

## 2026-09-21 — Decisions from a pull request

- **Choice:** A reply of `/speccy waive <reason>` or `/speccy ack <reason>` in one of Speccy's own review threads makes the next Action run write the entry to the doc's sidecar, commit it to the pull request's branch, and resolve that thread (REQ-126). The thread's key names the finding, so a waiver needs no more than the reason; an acknowledgement of `trace.coverage` names the trace ID first, because the finding is anchored on the doc, not on the ID. A refused command says why in the summary comment. The Action needs `contents: write`; on a fork it prints the sidecar to paste. While a waiver in the branch is not in the base branch, the comment and the check run give both verdicts (REQ-127). A sidecar entry names no approver: the pull request that merged it records who approved it.
- **Alternative:** Let the Action approve the waiver itself, against CODEOWNERS; and record the approver on the first default-branch run after the merge.
- **Reason:** A second approval config is a second thing to maintain, and branch protection already decides who may merge. A write to the default branch to record a name is refused by the very protection the design leans on, and git already holds the name.

## 2026-09-21 — Adopting a repo, and leaving adoption mode

- **Choice:** `speccy init --github` walks the repo, guesses a profile for each markdown file with the import guess, and writes `.speccy.yaml`: one glob for a folder whose markdown files all guess the same profile, else one entry per file. It then lints every mapped doc and puts each failing check slug in `adoption.relaxed`, and writes `.github/workflows/speccy.yml` with no secret, advisory, and the three permissions. It changes no doc (REQ-138). A relaxed check leaves the list through `/speccy enforce <slug>` in the pull request's conversation, which the Action commits to the branch. The pull request run itself decides which relaxed checks now pass everywhere: it lints the mapped docs the pull request did not change, and only when the repo has a relaxed check.
- **Alternative:** Compute "passes everywhere" on the default-branch run and carry it to the pull request in the Actions cache.
- **Reason:** The cache is keyed per branch and evicted after 7 days, so the offer would appear or not by luck. Lint holds a 10,000-word doc under a second (T-081), so the sweep costs seconds and needs no state between runs. The enforce command sits in the conversation because a relaxed check reports at INFO and gets no inline comment to reply to.
## 2026-09-21 — A GitHub URL as the way in

- **Choice:** A source URL makes a GitHub source in local mode and in hosted mode: `owner/name`, a repo URL, a `tree` URL, or a `blob` URL. `POST /github/resolve` says what the URL names before the source exists, and the dialog and `speccy add` both use it. A `blob` URL makes a one-doc source: `github_source` gains `is_file`, `profile`, and `api_url`, and the sync maps that doc so the single-file bundle rule (REQ-131) makes the bundle, with its `<name>.assets/` folder. Local mode gets its token from `gh auth token --hostname <host>` for each call and stores nothing, with a pasted fine-grained token as the fallback; the error says which of gh missing, gh logged out, or no access stopped it. Both modes poll every 5 minutes. A 404 on the repo reads as "no access", not "not found".
- **Alternative:** Clone the repo to disk in local mode, or read it with `gh api`.
- **Reason:** A copy on disk with no git behind it becomes a second truth, and the app already reads a repo tree through an `fs.FS`, so the scan rules are the same for a folder and a repo. `gh` gives the credential the machine already holds; using it for more than the token would be a second way to read a repo.
