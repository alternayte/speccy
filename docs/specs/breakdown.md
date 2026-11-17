# Breakdown

Issues: #154, #155, #156, #157, #158, #159.

## What it does
From a Build Ready doc, Speccy proposes a breakdown: epics, and work items under them, each made of quotes from the doc. Speccy checks the breakdown as it checks a doc. A person edits it, assigns each item to a person or an agent, and pushes it to Linear, Jira or GitHub Issues. Each pushed issue carries a Speccy block that pins the doc version. A builder reads its slice through MCP or the CLI, and a verification run checks only the trace IDs of its work item.

## Decisions
- A bundle has one breakdown. It covers that doc's trace IDs only. An item may quote a linked spec doc as context, and coverage does not count the linked doc's IDs — the breakdown is the same unit as the handoff, and the traceability check already covers the link.
- One model call proposes the epics, the items, their grouping and their order, following the profile's `breakdown:` section. Code rejects each quote that does not match the doc exactly once — an item adds nothing the doc does not say, and grouping and order rules need judgement.
- The `breakdown:` profile section holds: what an epic is for this doc type, the largest work item as a count of trace IDs, and the order rules (provider before consumer, expand before contract). Built-in profiles ship defaults — size is countable, so the check is deterministic.
- A doc that is not Build Ready is refused with its MUST count, as a handoff is. `acknowledged` takes it anyway, and the breakdown stores the verdict.
- The breakdown is a table in the store with a `based_on` revision for each edit, not an event stream — event sourcing covers threads, waivers and bundle status only.
- A new doc version marks the breakdown stale and names the items whose trace IDs or quoted text changed. "Update to this version" quotes again from the new version. It keeps edits, assignees and pushed issue links, and adds unpushed items for new trace IDs — edits survive a doc edit, and a second push knows which items exist.
- A criterion stays a quote. To change a criterion, change the doc — an issue must not drift from the spec.
- MUST checks: coverage (each trace ID in an item or acknowledged), no orphan item, no dependency cycle, one target repo per item. SHOULD checks: a testable criterion (requirement grammar where the profile enables it), the size limit. They run on every edit. A MUST finding refuses the push.
- The reader check is a SHOULD, runs on demand only, and uses two readers on one item. Divergence gives a finding — it costs money, so a person starts it.
- The next action on a Build Ready doc of size app or initiative is a new kind, "Break it down". A feature-size doc keeps "Hand it to a builder". The handoff dialog offers the other path — the doc already declares its size.
- The breakdown screen shows epics with items, the findings, and a coverage view. A person splits, merges, moves a trace ID, reorders, sets dependencies, adds a note, and assigns. In reviewer mode a person only comments.
- An assignee is a person or an agent, listed apart, read from the tracker connection. An epic default applies to its items with no assignee.
- A push makes fixed tool calls per tracker, and the model never picks a tool. Speccy reads the server's `tools/list` before the push and refuses with the missing tool name — no vendor publishes a stable schema for every tool.
- Linear: through the admin MCP connection. An epic is a parent issue, an item a sub-issue, a dependency a blocking relation. The person picks the team at push. Assigning an agent app sets it as the delegate.
- Jira: through the admin MCP connection, by host. An `atlassian.net` host uses the Rovo tool names. Any other host uses the `mcp-atlassian` tool names, which serve Data Center — Rovo serves Cloud only.
- GitHub Issues: through the GitHub credential Speccy already holds, with the REST API: sub-issues and `blocked_by` dependencies. The person picks one repo at push — dependencies are GA in REST and behind an Insiders flag in the GitHub MCP server.
- Each issue body holds the text for people and one Speccy block in an HTML comment: the bundle, the version, the handoff ID, the verdict at the handoff, the trace IDs, the doc URL at that version, and the work item URL.
- A push records one handoff with the label `breakdown`, under the handoff refusal rule. Speccy stores each issue as an external link of the bundle, so `coherence.external` reads it.
- A push is idempotent. It creates only unpushed items and never edits or closes an issue. After a new version, Speccy lists the pushed issues whose trace IDs changed, and a person decides.
- With no connection, Speccy exports the breakdown as markdown and CSV, with the same Speccy blocks.
- A verification run takes `trace_ids` or `work_item` (an issue URL or key resolved through Speccy's stored links). It gives outcomes for those IDs only.
- A run on a pull request head advises only: a comment or a check, no blocking thread, no effect on the handoff. A run on a default-branch commit counts and opens blocking threads — the doc hears only about code that landed.
- The handoff merges the newest counting outcome of each trace ID. "Verify the build" clears when every ID is verified.
- A new `verify` action in the code repo (`alternayte/speccy/verify`), connected mode only, finds the work item from issue keys and URLs in the pull request title, body and branch. It runs on the pull request head and on the merge commit — standalone mode has no stored breakdown.
- MCP tools: `propose_breakdown`, `get_breakdown`, `update_breakdown(id, based_on, ops)`, `push_breakdown` (its description says push only when the person says so), `get_work_item(ref)` with one string `ref`. `get_bundle` gains `version`. `verify_build` gains `trace_ids` and `work_item`. The CLI gets the same commands.
- `get_work_item` returns the quoted criteria with heading paths, the agreed build answers, the linked docs, the external links, and the doc at the pinned version. It says when a newer version exists.

## Out
- No automatic fix of a breakdown finding.
- No edit or close of an existing tracker issue.
- No tracker credential in Speccy's store.
- Speccy never runs the code.

## How I know it works
- A Build Ready SDD with 12 trace IDs gives a proposal where each ID is in an item, and each criterion matches the doc once. A doc with no trace IDs gives items anchored by heading path.
- A Not Build Ready doc is refused with the MUST count. With `acknowledged`, the breakdown stores that verdict.
- An item with IDs from two repos, a cycle, or an uncovered ID gives a MUST finding, and the push is refused.
- Splitting an item keeps coverage complete. Moving the last ID out of an item gives an orphan finding.
- A doc edit marks the breakdown stale and names the changed items. Update keeps the assignees.
- In a Linear test workspace a push creates 2 epics, 5 sub-issues and their blocking relations. An item assigned to an agent starts an agent session. A second push creates nothing.
- The same push to Jira Cloud, to Jira Data Center through `mcp-atlassian`, and to GitHub Issues creates the same structure.
- A pull request that builds REQ-003 and REQ-004 gets a comment with outcomes for those two IDs only, and no blocking thread. After merge, a breach on REQ-004 opens a blocking thread.
- After merged runs cover all IDs, the handoff reads verified.
- An MCP client goes from `propose_breakdown` to a scoped `verify_build` with no web app. `get_work_item` on an issue whose doc has a newer version returns the pinned version.
- `just verify` passes.
