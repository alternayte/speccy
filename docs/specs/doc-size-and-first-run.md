# Doc size and the first run

## What it does
A main doc declares its size in its frontmatter: feature, app, or initiative. Each check in a profile declares the sizes it applies to, so a one-feature SDD does not fail the checks that only a whole application needs. Speccy also reviews a markdown file that has no frontmatter and no `.speccy.yaml`: it picks the profile and the size from the content, names both in the result, and runs. After the first verdict, Speccy offers to write the type and the size into the file.

## Decisions
- Size is one word in the main doc's frontmatter: `size: feature | app | initiative` — the author writes it once, and it travels with the doc into any tool.
- A check declares `sizes: [feature, app, initiative]`, and applies at every size when the field is absent — the doc type does not change with size, but the set of checks does.
- At size feature the required headings narrow to Context, Decisions, and Non-goals. The other required headings report at INFO — the headings are a house style, and a small doc pays for them in waiver spam.
- Trace coverage, the upstream link check, and the decision-ID checks stay MUST at every size — they are the difference between a doc an implementer can build from and prose.
- At size initiative the link checks harden: the doc must link its children — an initiative that names no child doc hides the work.
- A missing size is inferred from the doc's word count and its links, and the run names the size it used — the author gets a verdict before they learn the vocabulary.
- An inferred size never changes the file — Speccy does not edit a doc to make its own review possible.
- A markdown file with no frontmatter type and no mapping is reviewable: the profile comes from the content, and the run names it — the first run is where the tool earns the right to ask for anything.
- A folder with two candidate main docs asks which one to use, as it does today — a bundle has exactly one main doc.
- After a verdict on a doc with no type, the UI and the CLI offer to write the type and the size into the frontmatter — the author chooses to make it stick, after they have seen the value.
- The built-in prd.yaml and sdd.yaml carry the sizes on their checks — the built-in profiles are the example every workspace copies.

## Out
- No second template per doc type.
- No second profile per size.
- No size on an asset or on a link.
- No change to the waiver mechanism.
- No automatic edit of any doc.

## How I know it works
- `speccy review path/to/spec.md` on a markdown file with no frontmatter and no `.speccy.yaml` returns a verdict. The output names the profile it picked and the size it inferred.
- The same file with `size: feature` in its frontmatter reports the Components, Data model, Interfaces, Failure modes, Limits, Security, Testing and Open questions checks at INFO, not MUST.
- The same file with `size: app` reports those checks at MUST.
- `build/dev-bundles/rate-limits` at size feature reaches Build Ready with no waiver.
- An initiative doc that links no child doc fails a MUST link check.
- After a review of a doc with no type, the control bar offers to write the type and the size. A click writes both into the frontmatter as a new version, and nothing else in the file changes.
- `just verify` passes.
