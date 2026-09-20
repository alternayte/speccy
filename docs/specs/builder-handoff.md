# Builder handoff

## What it does
Speccy hands a Build Ready bundle to the coding agent that builds it. One MCP tool and one CLI command return the build packet: the main doc, its assets, the main docs of the bundles it links to, the trace IDs, and the build questions with the answer the readers agreed on. The packet carries `HANDOFF.md`, a re-entry prompt with one checklist line per trace ID, so a session that lost its context resumes from the file. Speccy records which version each handoff took, and the bundle page says when a newer version exists.

## Decisions
- Speccy never runs the build — a runner needs model choice, sandboxing, repo access and secrets, none of which the review pipeline needs.
- The packet holds the build questions with their agreed answers — that is the one thing Speccy holds that the doc does not, and it is what an implementer needs.
- The packet holds no findings and no waivers — they describe the doc's own quality, and they invite a builder to edit the spec instead of building from it.
- `HANDOFF.md` carries a checklist of the doc's trace IDs — they exist, they are stable, and they are the doc's own units of work, so Speccy needs no planner.
- The agent owns `HANDOFF.md` after the handoff, and Speccy never reads it back — Speccy cannot see the build, so any progress it stored would be a number only the agent can update.
- A handoff row holds the bundle, the version, the verdict at that moment, who took it, an optional label the caller passes, and the time — which version a builder took is the one fact Speccy can keep honestly.
- The handoff row is a table in the store, not an event stream — event sourcing covers threads, waivers and bundle status only.
- A handoff is refused when the verdict is not Build Ready, or when it is stale. The problem names the MUST count or the stale version — the verdict is the only lever Speccy has, so it must bite.
- `acknowledged: true` takes the packet anyway, and the row stores the verdict it was taken at — a flat ban sends people around the tool for a deliberate spike, and an unrecorded override hides it.
- The MCP tool returns the packet inline. The CLI writes the same packet as a folder: the main doc and its assets at the top, the linked docs under `links/`, and `HANDOFF.md` — an agent in a terminal opens files, and an MCP caller passes one object to a model.
- The bundle page lists the handoffs of a bundle, newest first, and marks one stale when a newer version exists — an author needs to know their edit landed after someone started building.

## Out
- No build runner, no job queue, no sandbox.
- No started, finished or failed state on a handoff.
- No report from the builder back into Speccy.
- No findings, waivers or review report in the packet.
- No change to the export .zip or the HTML report.

## How I know it works
- `speccy handoff <bundle> --out ./packet` on a Build Ready bundle writes the folder: the main doc, its assets, `links/`, and `HANDOFF.md`.
- `HANDOFF.md` names the thing to build, points at the main doc and the linked docs, and holds one unchecked line per trace ID in the doc.
- The same command on a Not Build Ready bundle writes nothing and prints a problem that names the MUST count. With `--acknowledged` it writes the packet.
- The MCP tool `handoff_bundle` returns the same packet inline, and refuses the same way.
- The bundle page lists the handoff with its version and the verdict it was taken at. A new version marks that handoff stale.
- `just verify` passes.
