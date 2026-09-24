# Traceability from end to end

## What it does
A PRD and an SDD in one folder trace from the link to the handoff. Speccy reads the trace IDs that people already write. A PRD with no IDs gets a hint, not a failure. Each coverage gap in the SDD has three answers in the tour: this doc covers it, another doc covers it, or it is out of scope. Each answer closes the gap in the verdict and in the matrix. `docs/linked-docs.md` walks through this step by step.

## Decisions
- A trace ID definition is an ID with a prefix of the profile and one or more digits. It starts a heading or the first cell of a table row, or it starts a list item or a paragraph with a colon, a dash or an em dash after it — Speccy reviews the doc as it is written, and a sentence that starts with an ID stays a reference.
- An ID-like token at a definition position whose prefix the profile does not list gets an INFO finding `trace.unknown-prefix` that names the profile's prefixes — the author learns why an ID does not count.
- A doc with a requirements, decisions or non-functional heading and no trace ID gets one INFO finding `trace.no-ids` that offers Add IDs — no doc fails for its shape.
- When an upstream doc defines no ID that the profile covers, `trace.coverage` is not applicable, and the matrix says why — the check never passes with nothing to check.
- The matrix shows "No trace IDs" in place of "No gaps" when the upstream doc defines none — "No gaps" said something false.
- "This doc covers it" asks for the SDD section that covers the ID. Speccy shows the change `Covers REQ-001.` at the end of that section. On accept, the change is a new version, or a draft for a repo source — the ID sits in the doc, where the builder reads it.
- "Another doc covers it" (with the doc) and "Out of scope" (with a reason) are Acknowledgements. They follow the profile's waiver policy, and an approval writes a `trace:` entry to the sidecar — AGENTS.md defines an Acknowledgement as a use of the waiver mechanism.
- A waiver request on `trace.coverage` is an Acknowledgement. The finding offers the three answers, not "Ask for a waiver" — one record closes a gap in both the verdict and the matrix.
- An approved Acknowledgement stays until a person changes it. A section edit does not end it — it is about the ID, not a section.
- Add IDs says how many IDs it wrote, and names the version — a click with no result reads as a failure.

## Out
- No coverage without trace IDs, such as a model that matches requirements to sections.
- No change to the GitHub reply command `/speccy ack`.
- No mapping between IDs of different forms, such as `REQ-1` and `REQ-001`.

## How I know it works
- A PRD whose requirements are a table with `REQ-001` in the first column, and an SDD linked to it, show a matrix with one gap.
- `### REQ-002 Refund email` in a PRD is a definition, and `- FR-003: …` gets `trace.unknown-prefix`.
- A PRD with an unnumbered requirements list is Build Ready, with one `trace.no-ids` INFO finding. Its SDD has no coverage finding, and the SDD's Traceability page says "No trace IDs" and that coverage does not apply.
- In the tour, "This doc covers it" on REQ-001, with the Design section picked, writes `Covers REQ-001.` in that section. The gap turns to "Referenced".
- "Out of scope" on REQ-002 with a reason makes a request. After approval, the sidecar holds the `trace:` entry, the matrix cell reads "Out of scope", and the finding is gone.
- "Another doc covers it" on REQ-003 names the doc, and after approval the cell reads "Covered by".
- Add IDs shows "Added 3 IDs as version 2".
- `docs/linked-docs.md` follows a PRD and an SDD in one folder, from the link to the handoff, and each step matches the app.
