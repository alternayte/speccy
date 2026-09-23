# One bundle per spec doc

Issues: #59, #57, #58, #51, #55.

## What it does
A folder that holds a PRD and an SDD gives each doc its own bundle, its own profile and its own review. Import lists every markdown file with a profile picker and offers the link between the docs. The bundle page has a control that changes a doc's profile. A link target with `%20` resolves, a link that does not resolve names the bundles that could match, and an empty traceability page says why it is empty.

## Decisions
- A folder with two or more spec docs gives one single-file bundle per doc — a verdict, a link and a handoff already belong to one bundle, and a bundle with several reviewed docs changes every screen that assumes one main doc.
- A spec doc is a markdown file that names a `type`, that a `map:` glob covers, or that has an adopted type — a README next to a PRD must not become a reviewed spec.
- A folder with exactly one spec doc stays a folder bundle with its assets — nothing changes for a bundle that works today.
- The error "more than one main doc" goes away in the local scan and in import — two typed docs now mean two bundles.
- Untyped markdown stays a skipped doc, offered for adoption, or a carried file of the doc that references it — the adopt flow already asks per path.
- The import dialog lists each markdown file with a profile picker, prefilled with `profile.Guess`, and a "Not a spec" choice — import and adoption then ask the same question the same way.
- A failed guess leaves the picker empty. Import no longer fails with "Pick a type and import it again" — the person picks a profile in the same dialog.
- When the files include an SDD and exactly one doc its profile can implement, the dialog shows a prechecked "SDD implements PRD" line — Speccy offers the link and never infers it silently.
- For an import, a confirmed link goes into the SDD's frontmatter — an imported bundle is Speccy's copy.
- For a repo source, a confirmed link is stored as an adopted link, beside the adopted type. A link the repo names itself replaces it — Speccy writes no file in a repo it does not own.
- The bundle page gets a profile control. It writes the `type` field in a doc Speccy owns, and stores an adopted type for a repo doc — the frontmatter is the only way to change a profile today.
- No type-to-profile mapping and no workspace default profile — one profile per doc type stands, a team edits the `sdd` profile to change it, and a default would review a doc with a profile nobody chose.
- `findTarget` decodes a target with `url.PathUnescape` before it compares, and uses the raw target when decoding fails (#51) — lint, render and references already decode link paths this way.
- `links.has-upstream` names the bundles whose profile the doc can implement, when a target does not resolve — "No bundle matches" leaves the person guessing the slug.
- The traceability page shows an empty state: what the page shows, why it is empty (no link, a link that does not resolve, or no trace IDs), and the one thing to do next (#55) — a blank page teaches nothing.

## Out
- No bundle with more than one reviewed doc.
- No workspace mapping from a doc type to another profile.
- No workspace default profile.
- No link inferred without a person's confirmation.
- No write into a repo Speccy does not own.

## How I know it works
- A local folder with `PRD - X.md` (`type: prd`) and `SDD - X.md` (`type: sdd`) shows two bundles in the list, each with its own verdict after a review.
- The same folder with a `README.md` shows two bundles, and the README is not a bundle.
- Import of a folder with `PRD - X.md` and `SDD - X.md` and no frontmatter lists both files with guessed profiles and a prechecked "SDD implements PRD" line. After import, the SDD's frontmatter holds the link, and `links.has-upstream` passes.
- A repo source with the same two files offers the same choices in Adopt. After confirming, the repo gets no commit, and the SDD's link resolves.
- The profile control on a bundle changes its profile. The next review uses the new profile's checks.
- The link target `PRD%20-%20X.md` resolves to the PRD bundle.
- A link to a slug that does not exist gives a `links.has-upstream` message that names the PRD bundles in the workspace.
- A bundle with no links opens a traceability page that explains why it is empty and offers the next step.
