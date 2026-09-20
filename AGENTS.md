## What this is
Speccy reviews markdown spec bundles and returns one verdict: Build Ready or Not Build Ready. The design is in SDD.md and BUILD.md; SDD.md §18 lists the milestones.

## Run
`just dev` runs Go (air) and Vite; open http://127.0.0.1:5173. `just build` writes bin/speccy.

## Test
`just verify` is the PR gate: gen-check, lint, test, test-pg, budget.
Tools: Go 1.26.2, Node 24+, just 1.58, golangci-lint v2.12.2. pnpm 12.4.2 runs through npx.

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
- Control bar: the one row of markdown and profile controls above the preview. Avoid: toolbar, ribbon.
- Editable preview: the rendered preview that a click makes editable in place, writing back the markdown range. Avoid: WYSIWYG, rich text editor.
- Profile control: a control in the control bar that applies a fix the profile knows about. Avoid: smart action, AI action.
- Divider: the draggable separator between two panes. Avoid: splitter, gutter, handle.
- Next waiver: the control under a decided waiver card that scrolls to the next request waiting for this person on this bundle. Avoid: skip, queue.
- Decision reason: the text an approver gives when they reject a waiver. Avoid: rejection note, feedback.
- Ended waiver: an approved waiver that stopped applying because its section changed. The status value stays invalidated. Avoid: expired, stale.
