# Missing controls

## What it does
Each flow that the docs describe works from the web app. A person hands a spec doc to a builder from the app, marks a doc standalone, withdraws an acknowledgement, answers a gap on the Traceability page, and asks for a verification waiver. Insights shows the breach rate. The control row says when a linked doc made the verdict stale. ⌘S saves in every view. A member no longer sees a control that only an admin may use.

## Decisions
- The next action "Hand it to a builder", and an item in More, open a **Hand it to a builder** dialog — the next action led to a History tab with no control.
- The dialog has **Download the build packet**: Speccy records the handoff with an optional label and returns a .zip with the spec doc, its assets, the linked spec docs and `HANDOFF.md`, the files `speccy handoff --out` writes. The server builds the zip — the web app's JavaScript budget carries no zip library.
- The dialog has **Copy for a coding agent**: the `speccy handoff <path> --out <folder>` command and an MCP prompt that names `handoff_bundle` for this spec doc, each with a copy button — most builders are coding agents, and those paths record their own handoff.
- When the verdict is not Build Ready, or is stale, the dialog says so, and the download needs **Take it anyway**, which sends `acknowledged: true` — the API already records the verdict at the handoff.
- The `links.has-upstream` finding and its tour point get **Mark it standalone** beside Suggest fix. It takes a reason of at least 20 characters and follows the waiver policy for the finding's level. The approval writes `standalone:` to the sidecar — an acknowledgement uses the waiver mechanism.
- An acknowledged matrix cell and the "Standalone (acknowledged)" badge get **Withdraw**. It takes effect at once, with no approval, removes the sidecar entry through the write path an approval uses, and records who withdrew it in the event log. Only a person who can edit the spec doc sees it — a withdrawal only makes the verdict stricter.
- A Referenced cell gets a link to the reference in the editor, and no Withdraw — the reference is prose the author wrote.
- A gap cell on the Traceability page gets **Answer**, which opens the existing three answers for that column's spec doc. Only a person who can edit that spec doc sees it.
- A missing or breached outcome of a verification run in the History tab gets **Ask for a waiver**, with a reason and the waiver policy. The inbox link to the request opens that run — the request has no finding to select.
- Insights shows the breach rate beside False ready for each profile — the API returns it already.
- A verdict that is stale because an upstream spec doc changed says so in the control row, and names the spec doc — no screen said it.
- ⌘S and Ctrl+S save in every view of the editor — the Save button's title already promises it.
- A member does not see **Accept** on skipped docs. The list says "An admin accepts these." — `adoptSkippedDocs` is admin only.

## Out
- The bugs and the stale text that the docs work found. They go in their own PR, each fixed with a failing test first. The Action cache that holds the model key is the first of them, with its own patch release.
- Visual polish. The gauntlet runs after this spec.
- No Withdraw for a Referenced cell, and no approval step for a withdrawal.

## How I know it works
- On a Build Ready spec doc, the next action opens the dialog. Download gives a .zip with `HANDOFF.md`, and History lists the new handoff with its label.
- On a Not Build Ready spec doc, Download stays off until **Take it anyway**, and the handoff records Not Build Ready.
- The copied `speccy handoff` command runs as it is, and writes the same files as the zip.
- **Mark it standalone** with a reason makes a request. After approval, the sidecar holds `standalone:`, the badge shows, and `links.has-upstream` passes.
- **Withdraw** on an Out of scope cell makes it a gap at once, and the sidecar no longer holds the entry.
- **Answer** on a gap cell of the Traceability page closes the gap as the tour does.
- **Ask for a waiver** on a missing MUST outcome makes a request, and its inbox link opens the run.
- Insights shows a breach rate. Editing a PRD makes its SDD's control row name the PRD. ⌘S in Preview saves. A hosted member sees no **Accept** on skipped docs.
