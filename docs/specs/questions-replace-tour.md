# The questions screen replaces the tour

## What it does
The questions screen becomes the one list of everything that blocks Build Ready and needs a person. The server builds the list, and the web app, the TUI and the MCP server read it. The tour route, page, API and TUI screen are removed. A reviewer sees the same list without the answer controls, and can comment on each item.

## Decisions
- `GET /docs/{docId}/questions` replaces `GET /tour`. It returns the ordered items of the verdict's run, plus a flag that a MUST check has no answer. — The server owns the list, so three clients cannot drift.
- The list holds four item kinds: a material gap with its options, a blocking thread with a reply box, a decision finding (trace.coverage, downstream-request, has-upstream) with its acknowledge or send-back action, and any other open MUST finding with its existing fix or acknowledge action. — The screen lists exactly what blocks Build Ready and needs the author.
- Advice, SHOULD findings, waived findings and open decisions (non-blocking human threads) are not items. Open decisions stay in the threads panel. — They do not block.
- An unanswered MUST check is not an item. The screen shows one "Check again" control at the top when the flag is set. — Another review answers it, not the author.
- Items follow doc order by anchor. An item with no anchor comes first. — The author reads the doc top to bottom.
- A gap whose acknowledgement waits for approval is not an item, as in the tour today. — The author already answered it.
- The next action count is the length of the list. Next action kinds are waiver, questions, review, adopt, request-review and handoff. The decide and fix kinds are removed. — The count and the screen always agree.
- A person who cannot edit sees the same items with no option pickers, Other, Not needed or Submit. Each item has Comment, which opens a thread anchored on its finding. They can reply to a blocking thread. — A reviewer sees the same cost of Build Ready and can steer the answer.
- For a person who can approve, the waivers that wait for them show at the top of the screen as the existing waiver card. The waiver next action still targets the waiver card. — The approver needs no second screen; the approval flow of PR #20 does not change.
- Reviewer mode keeps two screens: the doc and the questions screen. Its "Answer the questions" link goes to the questions screen. — Reviewer mode loses no capability.
- The TUI tour screen becomes a questions screen on the same key layout, reading the new endpoint. The MCP tool get_tour becomes get_questions. — Every client says the same thing.
- The control row menu entry "Tour" becomes "Questions". — One name for one screen.
- Rail cards stay for advice and for one-at-a-time answers. — The material gate decided this.
- The tour package, the tour route, the tour page and the TourPoint schema are deleted. — No dead surface.
- Docs site pages and the reference pages that name the tour say "questions screen". "Tour" leaves the domain words. — The docs describe the product as it is.

## Out
- No change to the material judge, the verdict rule or the profiles.
- No change to the waiver approval flow (PR #20).
- No new item kind beyond the four.
- No change to the threads panel.

## How I know it works
- `/bundles/<id>/docs/<doc>/tour` no longer exists, and the control row offers "Questions".
- On a doc with a material gap, an open blocking thread and a trace.coverage finding, the questions screen lists all three, and the next action reads "Answer 3 questions".
- Resolving the blocking thread on the screen drops the count to 2 without a new review.
- A doc whose only MUST check came back with no answer shows "Check again" and no items, and the next action is review.
- A reviewer opens the questions screen from reviewer mode, sees the items with no option pickers, and comments on one. The author sees the thread on that finding.
- An approver with a waiting waiver sees the waiver card at the top of the questions screen and can approve it there.
- The TUI shows the same items as the web screen for the same doc, and the MCP tool get_questions returns them.
- `grep -ri tour` over internal, web/src, api and site/src finds no tour surface.
- `just verify` passes.
