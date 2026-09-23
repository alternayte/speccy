# Findings survive an edit

Issues: #60, #53.

## What it does
After an edit or an accepted fix, the AI findings of the last full review stay for every section that did not change. They count in the verdict, stay in the rail and the overlays, and keep their IDs. A finding in a changed section drops out, and the verdict line says how many sections changed since the AI review. The evidence and build question panels keep showing the last full review. An accepted fix no longer closes the other open suggestions or moves the rail.

## Decisions
- An AI finding whose anchored section hash is unchanged carries into the current version's verdict as a finding of that version — otherwise an edit to one section turns a doc blocked by an AI MUST finding into a lint-only Build Ready.
- A finding whose section changed drops out of the verdict and the rail — the AI never read that text.
- Whole-doc findings (divergence, coherence, doc-scope rubric) carry by the same rule — the error runs one way: a gap an edit answered elsewhere stays until the next full review, and the doc never reads Build Ready by mistake.
- Carried findings are merged on read, in `Summary` and in the findings list, from the newest lint run and the last full run. No rows are copied — a carried finding keeps its ID and its run, so an open suggestion survives, and a save adds no rows.
- The lint findings of the new version replace the lint findings of the full run — lint is cheap and already ran on the current text.
- The verdict line says "AI review from v<N> · <M> sections changed" when the full run read an older version — the author sees that part of the verdict is carried.
- The control to review the changed sections is the existing "Run review" — the review cache already reuses unchanged sections, so a rerun pays only for what changed.
- The evidence panel and the build questions panel read the last full run, labelled with its version — they show nothing after an edit today.
- The score counts carried items like current ones — the verdict and the score read the same set.
- The findings rail freezes its order on the last full run, not the newest run — a save makes a new lint run, and a new order re-sorts and scrolls the rail (#53).
- A finding card's suggestion state is keyed by finding ID — a carried finding keeps it across the refresh after an accept.

## Out
- No AI rerun on save.
- No copy of finding rows into lint runs.
- No carry-forward of a finding across a change to its own section.

## How I know it works
- Run a full review with an AI MUST finding in section A. Edit section B and save. The verdict stays Not Build Ready, the finding in A stays in the rail and the overlay, and the verdict line reads "AI review from v1 · 1 section changed".
- Edit section A instead. The finding in A is gone, and the verdict follows the remaining findings.
- After the edit, the evidence and build questions panels still show the full review, labelled v1.
- Open "Suggest fix" on two findings, accept one. The other suggestion stays open, and the rail does not scroll.
- Run the review again after the edit. The run report shows the unchanged sections reused from the cache.
