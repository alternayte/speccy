# The next action, and an author screen that earns its parts

## What it does
The API returns one next action on every bundle, for the person who asks: a kind, a sentence, and a target. The web author screen puts the title, the verdict in words, and the next action as the only primary button in one row, and moves tour, traceability, share, GitHub, export, print and files into one More menu. Each panel of the screen appears only when the doc's state earns it. The TUI shows the same sentence in its status line, and the key `n` does the named thing on every screen. Nothing follows from how the doc arrived, so an imported doc, a half-written doc and a doc written in Speccy land on the same screen.

## Decisions
- The server computes the next action — the verdict, the findings, the threads, the waivers and the tour are already there, and three clients deriving it would drift.
- The next action is computed for the caller — a waiver waits for some people and not for others.
- The precedence runs: a waiver that waits for you, the first tour point, the first MUST finding, a check of the current version, the type and size for the frontmatter, the reviews the profile needs, the handoff to a builder, then none — the order runs from what blocks other people to what only you can start.
- The bundles list returns the kind and the sentence only, and the single bundle adds the target — the list is where an author picks the work, and a list of 100 bundles stays one query.
- The screen earns its parts from the doc's state — a fixed journey breaks when a doc arrives half written.
- One control row replaces the top row, the adopt bar and the verdict bar — three bars compete, and the author reads none of them. The control bar of the preview does not change.
- The next action is the only primary button on the screen — two primary buttons are no primary button.
- The adopt bar becomes a next action — it is one more thing to do, not a standing bar.
- A rail tab appears only when the state earns it: Findings and Evidence after a run, History from the second version, Threads always — an empty tab teaches nothing.
- Versions and handoffs merge into one History tab — both answer what happened to this doc.
- The preview is the first view, and the app remembers the last view per browser — the preview is the only view a non-technical author needs.
- The explorer appears when the bundle holds an asset, and the More menu opens it either way — a tree of one file is furniture.
- The TUI gets one global key `n` for the next action, first in the key bar and in `?` — a hidden panel is acceptable, a hidden key is not.
- `n`, `r`, `t`, `e`, `?`, `q` and `esc` mean the same thing on every TUI screen — a screen that redefines a global key teaches the wrong reflex.

## Out
- Reviewer mode does not change. It keeps the two screens of `reviewer-mode-and-guide.md`.
- No change to the review stages, the verdict rule, the waiver policy or the handoff.
- The MCP server gains no tool. It may read the field.
- No wizard, no checklist, and no stored progress for a bundle.

## How I know it works
- `GET /api/v1/bundles/{id}` returns `next_action` with a kind, a sentence and a target. `GET /api/v1/bundles` returns the kind and the sentence on each item.
- Open a bundle with no run. The screen shows one row, one primary button that says to check the doc, one rail tab, and no explorer.
- Run the review. The same screen now shows the Findings and Evidence tabs, and the primary button names the first MUST finding.
- Ask for a waiver as one person, and open the bundle as the approver. The approver's next action is the waiver. The requester's is not.
- Add an asset. The explorer appears.
- Open a bundle for the first time in a new browser. The preview shows, not the split. Pick Code, reload, and the code view shows.
- Run `speccy tui`. The status line holds the same sentence as the web screen for that bundle, `n` does it, and `n` is the first key in the key bar and in `?`.
- A bundle with nothing open shows no primary button, and `n` does nothing.

## SDD change to make
SDD §13.4: replace "the verdict bar is the most prominent element of the bundle screen" with "the verdict and the next action share the one control row, and the next action is the only primary button on the screen".
