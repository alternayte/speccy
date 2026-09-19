# Gauntlet run 1 (M10)

`just gauntlet run-1` captured 23 states. Each state has 4 screenshots: the light and dark themes, at 1440 px and 390 px wide. The file names are `<state>-<theme>-<width>.png`.

The reference set is 7 screenshots of public pages from Stripe docs, Notion help, Linear, and Neon. They are in `docs/gauntlet/reference/`, which git ignores: they are third-party content.

## Checklist (BUILD.md §6.3)

- [x] **The verdict is the first thing a user sees on the bundle screen.** The verdict bar is under the header and spans the full width. See `verdict-*` and `bundle-*`.
- [x] **Doc text measure is about 72 characters at 1440 px.** The `.doc` class sets `max-width: 72ch`. See `bundle-preview-light-1440.png`.
- [x] **Each overlay layer is readable without colour.** Each layer has its own icon and underline style: solid, wavy, double, dotted, and dashed. The legend shows each style. See `bundle-preview-*`.
- [x] **Contrast meets WCAG 2.2 AA in both themes.** axe-core 4.10 (`color-contrast`) reports no violations on these screens: bundles, bundle (preview and split), tour, run report, trace, diff, inbox, admin, and profiles, in both themes. This run fixed three failures:
  - The light muted ink `--ink-3` went from #75756f to #686863.
  - The tour muted the other sections with opacity. It now uses the muted ink colour.
  - Placeholders in the tour keep their own colour.
- [x] **The tour works with the keyboard only.** `j` and `k` move between points. `d` records a decision. `w` asks for a waiver on a finding. `c` opens a comment. The first `Esc` closes the composer, and the second `Esc` leaves the tour. I checked each key in the browser.
- [x] **Every list and panel has an empty, a loading, and an error state.** See `bundles-empty`, `bundles-loading`, `bundles-error`, `inbox-empty`, and `inbox-full`. This run added two fixes:
  - A page that fails to render now shows an error with a Reload button, not the router's blank default page.
  - A network failure now says "Speccy could not reach the server. Check that it is running, then try again." It said "Failed to fetch" before.
- [x] **No screen scrolls sideways at 390 px.** I measured the page width and every element on 10 screens at 390 px. No page scrolls, and no element goes past the edge outside a code block or a table.
- [x] **A non-technical user can tell, from the bundle screen alone, what to do next.** The verdict bar has one sentence for the next step, for example "21 MUST findings to fix". The Tour button leads to the decisions.
- [x] **`just budget` passes.** The initial JavaScript is 314 kB gzipped, and the limit is 400 kB.
- [x] **Side-by-side comparison with the reference set.** See below.

## Critic rounds

Each round had one critic per part, with no context. A critic saw one of our screenshots and one reference screenshot in a random order, and scored both. The scores are out of 50: typography, spacing, alignment, colour, and hierarchy, 10 each.

| Part | Reference | Round 1 (ours / ref) | Round 2 (ours / ref) |
|---|---|---|---|
| Reading surface | Stripe docs guide | 29 / 39 | 30 / 39 |
| Trace matrix | Linear docs | 29 / 39 | 38 / 36 |
| Dark theme | Neon docs | 33 / 40 | 30 / 40 |
| Tour | Notion help | 41 / 30 | 35 / 30 |
| Run report | Linear home | 33 / 43 | 29 / 43 |

Between the rounds, I made one fix on the reading surface:
- A finding on a whole heading shows its gutter icon only, and the heading has no underline.
- Highlights use their underline styles only, with no fill.

Our total went from 165 to 162, so the loop stopped after round 2. The trace matrix changed by 9 points with no change to it, so single scores are not reliable.

## Three differences to fix

1. **Too many colours at once on the reading surface.** Every critic named this for the bundle screen. With all layers on, one paragraph can have 4 colours of underline and gutter icons. A fix could focus one layer at a time, or show only Risk and Ambiguous by default. The other layers would stay one click away.
2. **The type scale has too little contrast.** On the run report and the bundle header, the page title, the verdict heading, and the metadata are close in size and weight. The references use a large page title and smaller, lighter metadata.
3. **Vertical rhythm in dense panels.** The finding cards in the rail and the sections of the run report have tight, uneven gaps between the badge, the message, the quote, and the actions. The references use one spacing step inside a group and a larger step between groups.

## Three things that already match

1. **The reading layout.** The layout has three columns: an explorer, the doc at about 72 characters, and a rail. Stripe docs and Notion help use the same layout.
2. **The dark theme.** It has near-black neutral surfaces and one green accent, as Neon does.
3. **Dense tables with small uppercase section labels.** The trace matrix and the run report tables use the same density and labels as Linear's docs.
