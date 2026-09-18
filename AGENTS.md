## What this is
Speccy reviews markdown spec bundles and returns one verdict: Build Ready or Not Build Ready. The repo is at M0 (SDD.md §18): the design is in SDD.md and BUILD.md, and no code exists yet.

## Run
Nothing runs. There is no justfile and no binary. BUILD.md §3 lists the recipes that M0 must create.

## Test
No tests exist. BUILD.md §4 defines the gate for every PR. BUILD.md §3 names the gate recipe; the justfile does not exist yet. SDD.md §16 names the tests.

## Stack rules
- One Go binary embeds a React SPA. The frontend uses Vite and pnpm; the global Bun rule does not apply.
- SQLite in local mode, Postgres in hosted mode. One conformance suite runs against both.
- The API is spec-first: OpenAPI 3.1 is the source, and the Go server interfaces and TypeScript client are generated.
- Event sourcing lite covers threads, waivers, and bundle status only: pure decide and evolve functions, no async projections.
- Doc text lives in git or on disk. The store holds reviews, threads, waivers, and drafts.
- SDD.md and BUILD.md are read-only for the agent. Propose the exact change and the reason; the user edits.
- SDD.md and BUILD.md are gitignored. A clone holds no design docs.

## Domain words
- Bundle: a folder with one main doc and zero or more assets.
- Main doc: the one markdown file in a bundle with a type field in its frontmatter.
- Asset: any other file in the bundle.
- Profile: the versioned configuration for one doc type.
- Check: one binary rule in a profile, with a slug, a level, and a stage.
- Finding: one failed check, with an anchor to the text.
- Review run: one execution of the review pipeline on one bundle version.
- Verdict: the result of a run: Build Ready or Not Build Ready. An old version's verdict is stale.
- Score: passed checks divided by applicable checks. For metrics, not a gate.
- Build question: a question an implementer must answer to build the thing.
- Reader: one independent AI session that answers build questions from the doc only.
- Divergence: readers gave different meanings for one build question.
- Gap: all readers answered that the question is not specified.
- Waiver: an approved exception for one check in one section, with a reason.
- Acknowledgement: an author statement that a link or trace item is intentionally absent. It uses the waiver mechanism.
- Link: a typed relation between two bundles.
- Trace ID: a stable ID in a doc, such as REQ-012 or DEC-004.
- Tour: the ordered list of points that need a human decision.
- Section: the text under one heading, down to the next heading of the same or higher level.
