# Docs site

## What it does
Speccy's user docs are one Astro Starlight site under `site/`, published at speccy-docs.pages.dev. The site has Tutorials, How-to guides, Concepts, Reference and Operations, with llms.txt. The Reference pages that have a source of truth are generated from it, and a check fails when a page is stale. Vale holds the prose to the house rules and the Domain words. Each YAML sample parses with Speccy's own parser. The app links to the site from the top nav and from each finding of a built-in check.

## Decisions
- The site is the only home of the user docs. `docs/*.md` moves into `site/src/content/docs/` and is rewritten into the sections. `docs/specs/`, `docs/gauntlet/` and `docs/decisions.md` stay in `docs/` — two copies drift, and the specs and decisions are the developers' record.
- The README keeps the pitch and the install, and links to the site — Deedbox's README has this form.
- The Guide becomes the tutorial "From a blank page to a build packet". The vocabulary pass proposes its new path for the Guide domain word — AGENTS.md names `docs/guide.md`.
- The page map:
  - Tutorials: from a blank page to a build packet; link an SDD to a PRD.
  - How-to guides: review a doc on disk, one doc in GitHub, or a folder or repo; adopt a repo and decide in a pull request; close a coverage gap; link to an issue, a page or the code; change a profile or a check; ask for and approve a waiver; hand a spec to a coding agent; verify a build; keep the verdict in CI; add a model backend; run hosted mode.
  - Concepts: the verdict; the review pipeline; profiles and size; waivers, the sidecar and acknowledgements; traceability; handoffs and build reports; guarantees.
  - Reference: CLI and TUI, configuration, `.speccy.yaml`, frontmatter, sidecar format, profile schema, check catalog, HTTP API, MCP tools, GitHub Action and reply commands.
  - Operations: back up and restore, upgrade, the master key, budget and model cost.
  This is the gap between today's ten pages and Deedbox's docs.
- `go run ./tools/buildtool docs-ref` generates the Check catalog, the Profile schema, the GitHub Action, the MCP tools and the CLI pages. The sources are the built-in profiles, `schemas/profile.schema.json`, `action.yml`, the MCP tool list and the CLI help. The HTTP API page comes from `api/openapi.yaml` through `starlight-openapi` — a handwritten copy of a list that changes each release is wrong within weeks.
- The Check catalog has one heading per check slug. The slug is the anchor. A removed check keeps an entry that says "Removed in vX.Y", from a list next to the profiles — the app's links make the anchors a contract.
- Configuration, `.speccy.yaml`, Frontmatter and Sidecar format are handwritten. A check fails when a `SPECCY_*` variable in the code is missing from the Configuration page — these pages have no single source file.
- Each YAML sample names its kind in the fence, such as ` ```yaml frontmatter ` or ` ```yaml speccy-config `. The check parses it with the loader the server uses and fails on an unknown key, a bad value or an untagged YAML block. CLI commands in samples must be in the CLI command list — a reader copies a sample as it is.
- Vale runs as a `go tool` with a `Speccy` style: second person, present tense, no marketing words, a page opens with what the reader gets, no passive voice, a sentence length limit, and DomainWords. `buildtool` generates DomainWords from the Avoid clauses in AGENTS.md — the docs are where readers learn the words.
- `just docs-ref-check` runs the generated pages check, the samples check, the configuration check and Vale, and `just verify` runs it — a stale page fails the PR gate as `gen-check` does.
- `just docs-shots` covers each page whose steps happen in the UI. It captures the dark theme only, from `bin/speccy` on the testdata bundles, into `site/src/assets/shots/` — only a capture script keeps the pictures true after a UI change.
- Concepts pages get SVG diagrams for the review pipeline, the verdict inputs and the waiver flow. The site loads no Mermaid.
- The site takes the app's accent colour and fonts — the docs and the app read as one product.
- The app has a "Docs" link in the top nav. Each finding of a built-in check has "What this check means", which opens `/reference/checks/#<slug>`. A finding of a custom profile's check has no link. One constant holds the base URL.
- One docs version, built from `main`. Each push to `main` deploys it — releases come days apart, and old versions do not stay in use.
- `just docs-deploy` deploys `site/dist` to the Pages project `speccy-docs` with the logged-in wrangler. `.github/workflows/docs.yml` copies Deedbox's workflow. It builds and checks on each PR and push. It deploys, and it comments a preview URL on a PR, only when `CLOUDFLARE_API_TOKEN` is set. `CLOUDFLARE_ACCOUNT_ID` is set with `gh secret set`, and the owner creates the token.

## Out
- No docs versions per release, and no custom domain.
- No light-theme pictures.
- No docs inside the app binary. The app links out.
- No change to the Speccy review engine for the docs.

## How I know it works
- speccy-docs.pages.dev shows the five sections of the page map, and `/llms.txt` lists the pages.
- `just verify` fails when a check slug is added to `sdd.yaml` and the Check catalog is not regenerated.
- `just verify` fails when a `yaml frontmatter` sample has `links: {implements: prd}`, and names the file and the line.
- `just verify` fails when a page says "toolbar", and the message names "Control bar".
- A finding of `links.has-upstream` in the app has "What this check means", and the link opens the `links.has-upstream` entry of the Check catalog.
- A PR that changes `site/` builds the site in CI. When the token is set, the PR gets a comment with a preview URL.
- `just docs-shots` rewrites the pictures, and each one shows the dark theme.
- The README is under 80 lines and links to the site.
