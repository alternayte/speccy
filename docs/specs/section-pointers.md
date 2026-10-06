# Section pointers

## What it does
A rubric check with a `section:` key reads its section and each pointed-to section (#139). The check can then pass when its section says "Governance and compliance records the deviation" and that section holds the facts. The author does not copy the facts into the check's section.

## Decisions
- Speccy finds a pointer in the text of the section. A profile key cannot do it, because headings differ from doc to doc.
- A pointer is a markdown link to the anchor of a heading in the same doc, or the full title of a heading in the text, with case ignored.
- A heading title of one word counts only as a link. A single word such as "Data" gives false matches.
- Speccy follows pointers one level deep. A pointed-to section's own pointers add nothing, so the input stays small and predictable.
- The pointed-to sections go into the check's data as context, labelled with their heading path. The model may quote from them.
- The tree hash of each pointed-to section goes into the check's input hash. An edit to a pointed-to section makes the check run again, and an edit elsewhere does not.
- A cached answer that quotes a pointed-to section is valid. The rule that drops a quote outside the section (#116) accepts these sections.
- The cost estimate counts the pointed-to sections.

## Out
- No pointers into another doc or an asset.
- No pointers for whole-doc checks or for `scope: section` checks.
- No profile key that lists more sections.

## How I know it works
- An SDD's Security section says "Governance and compliance records the deviation", and that section holds the facts. A full review passes `sdd.security`. The answer quotes Governance and compliance.
- Edit Governance and compliance. The next review asks `sdd.security` again. Edit another section. The next review takes `sdd.security` from the cache.
- A section that says "the data" with no link to a heading named Data gets no extra section.
- `just verify` passes.
