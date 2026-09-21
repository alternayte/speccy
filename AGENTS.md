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
- Main doc: the one markdown file in a bundle that a type field or a path mapping names.
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
- Sidecar: the file .speccy/decisions/<doc path>.yaml that holds the waivers and acknowledgements for one doc. Avoid: waiver file, decisions file, metadata file.
- Heading path: the list of headings down to one section. It anchors a decision in a doc with no trace IDs. Avoid: breadcrumb, section path, anchor.
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
- Build packet: the main doc, its assets, the linked bundles' main docs, the trace IDs, and the build questions with their agreed answers, handed to a coding agent. Avoid: payload, bundle export.
- Handoff: one record that a builder took a build packet for one bundle version, with the verdict at that moment. Avoid: job, build run.
- Size: the scale one main doc covers: feature, app, or initiative, declared in its frontmatter. A check's scope field is a different thing. Avoid: scale.
- Re-entry prompt: HANDOFF.md, the file a coding agent reads to resume building after it loses its context. Avoid: handover doc, resume file.
- Build report: what a coding agent tells Speccy about the doc after it took a build packet. Avoid: feedback, build result.
- Blocked report: a build report saying the agent cannot build a section without an answer. It opens a blocking thread. Avoid: blocker.
- Note: a build report saying the agent built something, but the doc was unclear. It changes no verdict. Avoid: remark.
- False-ready rate: for one profile, the share of Build Ready handoffs that came back blocked. Avoid: accuracy, precision.
- Reviewer mode: the reduced two-screen surface for a person who cannot edit the bundle. Avoid: guest view, read-only mode, simple mode.
- Status line: the one line on the reviewer screen saying in words whether the author is still working or the spec is ready. Avoid: verdict chip, status badge.
- Guide: docs/guide.md, the one worked example from a new bundle to a build packet. Avoid: getting started, tutorial, walkthrough.
- Next action: the one thing the server says this person must do next on this bundle, with a kind, a sentence and a target. Avoid: suggestion, nudge, call to action, todo.
- Control row: the one row above the panes with the title, the verdict in words, and the next action. Not the control bar. Avoid: header, toolbar, verdict bar.
- History: the rail tab with the versions and the handoffs of one bundle. Avoid: timeline, activity.
- Adopted repo: a repo with docs that Speccy did not write, mapped to profiles by path, with no Speccy frontmatter. Avoid: legacy repo, existing repo, brownfield.
- Reply command: a /speccy reply in a Speccy review thread that the next Action run turns into a commit. Avoid: slash command, bot command, chatops.
