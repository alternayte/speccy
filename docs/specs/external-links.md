# External links

## What it does
A main doc links to an issue, a page, a repo path, or a commit, in the same frontmatter `links:` list. Speccy resolves each target, reads the ones a credential covers, and reports two things. A code target that changed after the doc version raises a drift finding. An issue or a page that contradicts the doc raises a conflict finding. Both are SHOULD findings, so neither blocks Build Ready. One table on the links and matrices screen shows every external link and its state.

## Decisions
- External links use the existing `links:` list, with a scheme on the target: `github:owner/repo#path`, `jira:PAY-412`, or a bare URL — one list and one parser, so an author looks in one place.
- A short key expands to a URL through a per-scheme pattern in `.speccy.yaml` — the key stays readable in the doc.
- One new kind, `implemented-by`, names a code target — the other four kinds cannot say that the target implements this doc.
- Targets are doc-level, not per-section — a per-section code link needs an anchor that survives an edit, which is a larger thing.
- `links.code-drift` is a SHOULD check — the code moving is news, not a defect in the doc.
- Drift is measured against the doc version, not the previous run — the version is the only record of a person reading the doc, so a re-run that nobody read does not clear the warning.
- Speccy reads a code target with the credential it already holds: the machine `gh` token in local mode, the source's token in hosted mode — no new credential store and no new setup step.
- Speccy reads a non-GitHub target through an admin MCP connection, chosen by host — the model never picks the tool, so an untrusted doc cannot steer Speccy into a destructive call.
- `coherence.external` is a SHOULD check in the same stage as `coherence.contradiction`, cached on the target's content hash — an unchanged issue costs nothing on the next run.
- There is no manual coherence button — a review run is the button, and the Action runs a review on each push.
- A target Speccy cannot reach reads as unchecked, with a reason — an unreadable target must not read as a pass.
- A malformed target, an unknown scheme, or a scheme with no pattern is a MUST lint finding — the author fixes it in one edit, with no judgement.
- The build packet carries external links as kind and URL, plus the commit the review read for a code target — the packet exists so a coding agent does not have to ask where things are.

## Out
- Speccy stores no tracker credentials and calls no vendor API directly.
- Speccy fetches no issue or page content into the build packet.
- Speccy writes nothing back to an issue, a page, or a repo.
- The TUI gets no MCP screen. An admin configures connections in the web app.

## How I know it works
- A doc with `kind: implemented-by, target: github:alternayte/speccy#internal/features/waiver` shows one external link row that reads aligned.
- A commit that touches that path, then a review run, turns the row to drifted. The finding names the commit, the date, and a compare URL. The verdict stays Build Ready.
- A new version of the doc turns the row back to aligned.
- A `jira:` link with no pattern in `.speccy.yaml` fails lint with a MUST finding on the frontmatter.
- A `jira:` link with a pattern, and no MCP connection for that host, reads unchecked with the reason.
- With an Atlassian MCP connection for that host, an issue that states the opposite of a doc statement raises a `coherence.external` finding, anchored in the doc and quoting the issue.
- The same review, run again with the issue unchanged, makes no MCP call.
- `HANDOFF.md` from the build packet lists each external link with its kind and URL.
