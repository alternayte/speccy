# Gauntlet run 2 (M12)

`just gauntlet run-2` captured 24 states. Each state has 4 screenshots: the light and dark themes, at 1440 px and 390 px wide. This run adds `bundle-default`: the preview with the overlay layers that a reader first sees. The `bundle-*` screens still have every layer on, as BUILD.md §6.2 asks.

## Checklist (BUILD.md §6.3)

- [x] **The verdict is the first thing a user sees on the bundle screen.** See `bundle-default` and the `verdict-*` states.
- [x] **Doc text measure is about 72 characters at 1440 px.** The measure did not change since run 1.
- [x] **Each overlay layer is readable without colour.** Each layer has its own icon and underline style. See `bundle-preview-*`.
- [x] **Contrast meets WCAG 2.2 AA in both themes.** axe-core 4.10 reports no failures on the bundle screen or the run report in either theme, the two screens this run changed. The run 1 checks of the other screens still hold: no colour token changed since then.
- [x] **The tour works with the keyboard only.** The tour did not change since run 1.
- [x] **Every list and panel has an empty, a loading, and an error state.** See `bundles-empty`, `bundles-loading`, `bundles-error`, `inbox-empty`, and `inbox-full`.
- [x] **No screen scrolls sideways at 390 px.** I measured the bundle screen and the run report in both themes.
- [x] **A non-technical user can tell, from the bundle screen alone, what to do next.** See `bundle-default`: the verdict bar says what to fix, and only the blocking and ambiguous text is marked.
- [x] **`just budget` passes.** The initial JavaScript is 315 kB gzipped, and the limit is 400 kB.
- [x] **Side-by-side comparison with the reference set.** See below.

## Critic rounds

Each round had one critic per part, with no context. A critic saw one of our screenshots and one reference screenshot in a random order, and scored both. The scores are out of 50.

| Part | Reference | Round 1 (ours / ref) | Round 2 | Round 3 |
|---|---|---|---|---|
| Reading surface | Stripe docs guide | 29 / 44 | 29 / 39 | 29 / 39 |
| Trace matrix | Linear docs | 30 / 39 | 35 / 39 | 30 / 39 |
| Dark theme | Neon docs | 31 / 39 | 38 / 40 | 28 / 39 |
| Tour | Notion help | 36 / 29 | 29 / 39 | 35 / 31 |
| Run report | Linear home | 36 / 39 | 32 / 44 | 31 / 42 |
| **Total (ours)** | | 162 | 163 | 153 |

The fixes between the rounds:
- **After round 1:** the reading surface showed too many colours, which is difference 1 from run 1. Only the Risk and Ambiguous layers are now on by default. The other layers are one click away, and a saved choice still wins.
- **After round 2:** the run report had the largest gap. It now has a larger title, more space between sections, and more padding in the table rows.

The gauntlet stops at three rounds. The same screen changes by up to 10 points between rounds with no change to it, so no single score is reliable.

## Three differences to fix

1. **Type scale.** Critics name it in every round: the page title, section labels, and body text are close in size and weight on the bundle screen, the trace matrix, and the run report.
2. **The bundle header has many buttons of the same weight:** Tour, Traceability, GitHub, Draft, Request review, Share, Run review, Export, Report, and PDF. Only Run review is primary. A menu for the export actions would leave one clear action.
3. **Dense panels.** The finding cards in the rail and the rows of the trace matrix have tight padding.

## Three things that already match

1. The reading layout: an explorer, the doc at about 72 characters, and a rail, as in Stripe docs and Notion help.
2. The dark theme's neutral surfaces with one green accent, as in Neon.
3. Colour that carries meaning. The run 2 critics named the tour's functional colour and its focus panel as stronger than the reference.
