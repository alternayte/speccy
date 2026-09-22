# Adopt the docs of a GitHub source

## What it does
A source that reads a folder or a whole repo lists the markdown files the scan passed over, each with a guessed doc type. A person accepts the ones they want, and each accepted file becomes a bundle at once, with a review and a verdict. Speccy holds the accepted type itself, so the repo takes no commit and the source branch does not move. One control later opens a pull request that writes the same answer into the repo's `.speccy.yaml`.

## Decisions
- The accepted type lives in Speccy, one row per source and per path — a team gets a verdict on a repo before it asks anyone to merge anything.
- The repo always wins: frontmatter `type`, or a `.speccy.yaml` mapping that covers the file, replaces the stored row on the next scan — the two routes converge instead of fighting, and nothing moves under a team that merges the mapping later.
- The bundle keeps its ID, its threads and its waivers when the repo takes over — the doc is the same doc, whoever names its type.
- The list holds every passed-over markdown file in path order, with the guess when `profile.Guess` is sure and "No guess" otherwise, and a type picker on each row — a hidden file is a dead end, and the guess ranks the list without deciding it.
- Above 200 files the list says how many more there are, and asks the person to narrow the source to a folder — a monorepo must not turn the screen into a scroll.
- The pull request writes `map:` entries only: one per folder when every passed-over doc there was accepted with the same type, else one per file — a folder glob would otherwise take in a doc the person left alone, and a reviewer weighs one decision.
- The web carries this. The TUI stays a review surface — the TUI targets 80 by 24, and a second copy of the flow drifts from the first.

## Out
- No new CLI command. `speccy add <url>` and `speccy init --github` already cover the terminal.
- The pull request does not write `.github/workflows/speccy.yml`, and it does not change any doc.
- Speccy writes no frontmatter into a source repo.
- A one-doc source keeps its own profile field. This does not replace it.

## How I know it works
- A source for a repo folder whose docs name no type lists those files, each with a guess or "No guess".
- Accepting one makes a bundle. It appears on the bundles screen, a review runs, and it shows a verdict.
- The repo takes no commit: the branch's head is the same before and after.
- Adding a `.speccy.yaml` mapping in the repo for that path, then a scan, leaves the bundle in place with the same ID, its threads and its waivers, and the stored row is gone.
- Adding frontmatter `type` to the doc in the repo does the same.
- A source with 500 passed-over files lists 200 and says how many more.
- "Write the mapping to the repo" opens one pull request that adds `map:` entries and changes nothing else.
- A file rejected, or never accepted, makes no bundle and no review.
