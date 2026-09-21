# Reviewer mode and the end-to-end guide

## What it does
A person who cannot edit a bundle sees a reviewer surface with two screens. The first screen shows the main doc, the assets list, one status line in plain words, and the comment action. The second screen is the tour, which shows only the points this person can act on. A person who can edit keeps the full bundle page and enters reviewer mode with a search param on the bundle route. One guide, `docs/guide.md`, follows one worked example from a new bundle to a build packet, with screenshots and three GIFs that a just recipe captures from the real app.

## Decisions
- Reviewer mode is a view, not a person — the same reduced surface serves a share-link guest and a signed-in stakeholder.
- Speccy derives reviewer mode from `can_edit` and stores nothing — the mode is a view, so it needs no table and no API field.
- The tour is the whole working surface for a reviewer — a second guided surface would repeat the anchor, focus and progress logic.
- The tour in reviewer mode lists blocking threads and open questions only — findings and waiver requests are an author's job.
- The reviewer screen keeps the assets list at all times — a reviewer needs the pictures and the data the doc points at.
- The status line gives words only, with no score, no finding count and no verdict chip — a number invites a wrong reading.
- Reviewer mode hides the explorer, the rail tabs, versions, diff, trace, handoffs, print, export and the top nav — each one is an author's tool that only invites a wrong click.
- `just docs-shots` drives the real app with agent-browser against `build/dev-bundles` and writes every file in `docs/images/` — a hand-captured image goes stale without a signal.
- A GIF covers the review run to a verdict, the click-to-edit in the editable preview, and the tour moving point to point — motion carries what the prose cannot.
- Every other step uses a PNG, capped at 1200px wide, and `just verify` budgets the total size of `docs/images/` — the budget stops the repo growing with each capture.
- `docs/guide.md` replaces `getting-started.md`, `authoring.md` and `reviews.md` — three short overlapping docs are why the current set is not sufficient.

## Out
- The author surface does not change. The full bundle page, the rail tabs, the diff, the trace and the handoffs panel stay as they are.
- No new permission, role or invite type.
- `cli-and-tui.md`, `configuration.md` and `decisions.md` stay as they are.

## How I know it works
- Open a share link as a guest. The screen shows the doc, the assets list, one status line and the comment action. The header shows no nav links.
- Start the tour as that guest. The tour lists blocking threads and open questions. It lists no finding and no waiver request.
- Open the same bundle as the author. The full bundle page appears. Add the reviewer search param. The reduced surface appears.
- Run `just docs-shots`. `git status` shows changes only under `docs/images/`.
- Run `just verify`. The budget check passes with the captured images in place.
- Read `docs/guide.md` from top to bottom. It carries one bundle from creation to a build packet, and each screenshot matches the current app.

## Next grill
Keep the author side simple for every entry point: a doc written outside the app and imported, a doc half written, and a doc written in the app from start to finish.
