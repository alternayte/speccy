# A bundle holds one or more spec docs

Issues: #69, #66, #67. Replaces the decision "one single-file bundle per doc" in `one-bundle-per-spec-doc.md`.

## What it does
A bundle is a folder. It holds one or more spec docs and their assets. Each spec doc has its own profile, review run, findings rail, overlays, verdict and handoff. Speccy never combines them. A click on a spec doc in the file tree changes the editor, the rail and the control row to that doc. A bundle with one spec doc looks and works as it does today.

## Decisions
- The store is bundle → spec doc (ID, path, profile) → versions, review runs, findings, waivers, threads and handoffs, all keyed on the spec doc ID — each spec doc is reviewed on its own, so its records must belong to it.
- The migrations are rewritten, and Speccy carries over no data. An upgrade resets the local `.speccy/state` — no one uses Speccy yet, so the clean model costs nothing.
- The route is `/bundles/$bundleId/docs/$docId` — a link, an inbox item, a thread or a handoff opens one spec doc directly.
- `/bundles/$bundleId` opens the spec doc whose next action waits for this person. If no next action waits, it opens the first spec doc in path order — the person lands where their work is.
- The file tree marks each spec doc with its profile and its verdict. A click on an asset opens the asset as it does today — the tree is the doc switcher, so the page needs no new control.
- The bundle list shows one row for each bundle. The title is the folder name. The chip shows the worst state of the bundle's spec docs: Not Build Ready, then Not reviewed, then Build Ready. A muted count follows, for example "2 spec docs: PRD, SDD" — a list row keeps one chip and the same shape as the other rows.
- A link resolves to one spec doc. A relative path names the doc, in the same bundle or in another bundle — a link between a PRD and an SDD in one folder is a relative path.
- A bundle slug resolves only when exactly one spec doc in that bundle has a profile that the link type accepts. With zero matches or two or more, `links.has-upstream` fails and names the spec docs that could match — Speccy never picks a doc silently.
- A spec doc version covers the doc and every file in the bundle that is not a spec doc — Speccy does not track which doc uses which asset.
- An edit to the PRD makes a new PRD version only. An edit to a shared asset makes a new version of each spec doc in the bundle. A change in a linked doc makes no new version — the verdict goes stale only when the reviewed input changes.
- A source is always a repo, a branch and a folder. A source URL that names one doc makes a source for the doc's parent folder, and the bundle opens on that doc — a folder is a bundle, so the folder is the unit to add.
- A source URL whose folder an existing source already covers shows "Already added" and opens that bundle. Speccy does not make a second source. The error "another source already holds" goes away — two sources can no longer hold the same doc.
- A folder that directly holds one or more spec docs is a bundle. A subfolder with no spec doc in its subtree holds assets of the nearest parent bundle. A subfolder with its own spec doc is a separate bundle — a `diagrams/` folder belongs to its doc, and a nested initiative stands alone.
- A folder with markdown but no spec doc makes no bundle. Its files stay skipped docs that a person can adopt — the adopt flow already covers them.
- A share link grants the whole bundle. The reviewer screen has the same doc switcher, and the status line is per spec doc — permission belongs to the folder, and a per-doc share has no second use.
- A folder import makes one bundle. The import dialog opens it and shows any `problems` from the response (#66) — the person sees what the import made.
- A source added again takes over the archived bundle for its folder and un-archives it. The add dialog shows the error of the first sync (#67) — a deleted source must not block the same folder forever.

## Out
- No combined verdict, score or handoff for a bundle.
- No stored group of bundles, and no UI to move a doc between bundles.
- No share link for one spec doc.
- No tracking of which spec doc uses which asset.
- No data migration from the current store.
- No local mode UI for GitHub sources, and no sync at start (#68).

## How I know it works
- A local folder with `PRD - X.md` (`type: prd`), `SDD - X.md` (`type: sdd`) and `diagrams/flow.png` shows one bundle in the list, with the chip "Not reviewed" and "2 spec docs: PRD, SDD".
- On that bundle, a review of the PRD with the prd profile and a review of the SDD with the sdd profile give two verdicts. A click on each doc in the tree shows only that doc's findings in the rail.
- The SDD's `implements: ./PRD - X.md` resolves, and `links.has-upstream` passes.
- An edit to the PRD makes the PRD verdict stale and leaves the SDD verdict current. An edit to `diagrams/flow.png` makes both verdicts stale.
- The PRD is Build Ready, and the SDD is Not Build Ready. The list chip shows Not Build Ready.
- `/bundles/<id>/docs/<sdd id>` opens the SDD directly.
- A folder with one spec doc opens as today, with no change in the tree or the rail.
- A GitHub URL for `SDD - X.md` makes one source for its folder and opens the SDD. A second URL for the same folder shows "Already added".
- After a source is deleted and added again, the bundle comes back with its history.
- A folder import opens the new bundle and shows its problems, if there are any.
- A subfolder `payments/` with its own `type: sdd` doc shows as a separate bundle.
