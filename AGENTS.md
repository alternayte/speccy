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
- Bundle: a folder with one or more spec docs and zero or more assets. Avoid: group, initiative folder.
- Asset: any file in the bundle that is not a spec doc.
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
- Carried finding: an AI finding from the last full review whose section has not changed since. It counts in the current version's verdict. Avoid: stale finding, old finding, inherited finding.
- Ended waiver: an approved waiver that stopped applying because its section changed. The status value stays invalidated. Avoid: expired, stale.
- Build packet: the spec doc, the bundle's assets, the linked spec docs, the trace IDs, and the build questions with their agreed answers, handed to a coding agent. Avoid: payload, bundle export.
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
- Source URL: the GitHub URL a person pastes to make a source: a repo, a branch and folder, or one doc. Avoid: repo link, import URL, clone URL.
- Adopted repo: a repo with docs that Speccy did not write, mapped to profiles by path, with no Speccy frontmatter. Avoid: legacy repo, existing repo, brownfield.
- Reply command: a /speccy reply in a Speccy review thread that the next Action run turns into a commit. Avoid: slash command, bot command, chatops.
- External link: a link from a bundle to an artifact outside Speccy: an issue, a page, a repo path, or a commit. Avoid: reference, integration, external reference.
- Drift: a code target changed after the doc version that a review run read. Avoid: stale, out of date, divergence.
- Unchecked: an external link state meaning no credential and no MCP connection can read the target. Avoid: unknown, unreachable, skipped.
- Skipped doc: a markdown file in a source that the scan passed over, because it names no type and no mapping covers it. Avoid: unmapped file, orphan doc, candidate.
- Adopted type: the doc type a person accepted in Speccy for one path in a source. The repo replaces it when it names its own. Avoid: override, profile override, guess.
- Adopted link: a link a person confirmed in Speccy for a doc in a repo source. Speccy stores it and writes nothing into the repo. A link the repo names replaces it. Avoid: inferred link, implicit link, stored link.
- Spec doc: a markdown file that names a type, that a map glob covers, or that has an adopted type. Each spec doc has its own profile, versions, review runs and verdict. Avoid: typed doc, reviewed doc, main doc.
- Dismissed doc: a markdown file a person marked as not a spec, so Speccy stops offering to adopt it. Avoid: ignored file, hidden file, excluded.
- Verification run: one execution of the post-build gate on one bundle version against one code repo at one SHA. Avoid: conformance run, build check, second gate.
- Code target: a repo path plus a verbatim anchor quote that locates where one trace ID is implemented. A test target is the same, anchored on the test declaration line. Avoid: code link, symbol, reference, coverage entry.
- Anchor quote: the verbatim string that locates a target in a file. It must match exactly once. Avoid: snippet, marker, locator.
- Builder claim: the optional statement a builder submits naming the SHA, the code targets and the test targets for one trace ID. Avoid: mapping, attestation, evidence.
- Breached: the verification outcome where the cited code contradicts the requirement text. Avoid: diverged, broken, non-conformant.
- Requirement grammar: the optional profile check that parses a definition into a trigger and a response in the EARS shapes. Avoid: EARS check, requirement format, structured requirement.
- Source policy: the profile section that says which domains the grounding stage accepts, their tier and their freshness period. Avoid: allowlist, trusted sources, grounding config.
- Unproven: the verification outcome where a code target holds and the judge neither affirmed the requirement nor found a contradiction. Avoid: inconclusive, unknown, partial.
- Claim class: the class a claim takes from the heading path of the section it is anchored in, which selects the grounding source policy. Avoid: claim type, category, tag.
- Start from: the profile whose YAML and template prefill the form of a new profile. Avoid: clone, duplicate, template, copy.
- Carried file: a file a bundle holds because its main doc references it. Speccy never writes it back. Avoid: copied file, linked asset, imported asset, vendored file.
