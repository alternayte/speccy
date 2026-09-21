# Build report

## What it does
A coding agent that took a build packet reports what it learned about the doc. It says it is blocked, meaning it cannot build a section without an answer, or it leaves a note, meaning it built something but the doc was unclear. Each report opens a thread on the section it names, and the thread carries the handoff it came from. A blocked report is a blocking thread, so the bundle returns to Not Build Ready until a person answers it. Insights gains the false-ready rate for each profile: the share of Build Ready handoffs that came back blocked.

## Decisions
- The loop carries what the build learned about the doc, not how the build is going — Speccy does not run the build, so any progress it stored would be a number nobody can verify.
- A report opens a thread, and the thread holds the handoff ID — a thread already has an anchor, an audience, a blocking flag, and a decision, so a second aggregate would duplicate all of it.
- The builder chooses blocked or note — the agent is the only party that knows whether it could proceed, and an agent must not turn a dashboard red by writing a remark.
- A blocked report is a blocking thread — if a builder cannot build a Build Ready doc, the verdict was wrong, and the tool must say so.
- A report against a stale handoff opens as a note and never blocks — the doc moved on after the builder took it.
- A report names a section heading path or a trace ID, and Speccy anchors the thread to it. A report that names neither anchors to the doc — the answer belongs beside the text that caused the question.
- The MCP tool `report_build` takes the handoff ID, the kind, the target, and the text — the handoff ID is already in the packet, so the agent needs no lookup.
- `HANDOFF.md` gains an "If you get stuck" section that names the tool and quotes the handoff ID — an agent that is not told will never report, and the packet is the only thing it is sure to read.
- `speccy report` sends the same report — a builder with no MCP connection still closes the loop.
- The false-ready rate counts only the handoffs taken at Build Ready — a handoff taken with acknowledged is no evidence against the profile.
- Under the rate, Insights lists the checks and sections that the blocked reports point at, most frequent first — a profile with a missing check needs to know where to look.

## Out
- No build progress, and no started, finished or failed state.
- No new aggregate for a report.
- No automatic edit of the doc from a report.
- No verdict change from a note.
- No report from a builder that took no handoff.

## How I know it works
- `report_build` with the handoff ID, kind `blocked`, and a section heading opens a blocking thread anchored to that section. The bundle's verdict becomes Not Build Ready, and the rail shows the thread with the version the builder took.
- The same call with kind `note` opens a thread that changes no verdict.
- A report against a handoff that is stale opens as a note, whatever kind the builder sent, and the thread says the doc changed after the handoff.
- The bundle's authors get an inbox item for the new thread.
- A person answers the thread and resolves it. The blocking flag clears, and the verdict returns to what the run said.
- `HANDOFF.md` in a written packet holds the handoff ID and the instruction to report.
- `speccy report <bundle> --handoff <id> --blocked --section "Data model" --text "..."` opens the same thread.
- Insights shows the false-ready rate for each profile, and the checks and sections the blocked reports name.
- `just verify` passes.
