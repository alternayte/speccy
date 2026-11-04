# Verify after the handoff

## What it does
The next action carries the author past the handoff to a verified build. HANDOFF.md tells the coding agent to finish with a verification run against its handoff, so the run is the signal that the build is done. When the current version has a handoff and no usable verification run, the next action reads "Verify the build" and opens the verify field. A MUST outcome that is missing or breached already opens a blocking thread, so it arrives as "Answer N questions". A verified run ends the path with no next action.

## Decisions
- No new build report kind. The agent calls `verify_build` or `speccy verify` with the handoff ID when it finishes. — The tool exists and already links the run to the handoff; a `done` report would say the same thing twice.
- HANDOFF.md gains a last step: when the build is done, verify it against handoff `<id>` with `verify_build` or `speccy verify`, naming the repo, branch, commit or pull request built. The `verify_build` description says to call it when the build from a packet is done. — The agent reads HANDOFF.md; the instruction must be there.
- A new next action kind `verify`, sentence "Verify the build". It shows when the verdict is Build Ready, the current version has at least one handoff, and the newest verification run of the current version is absent, failed, or stale. — The author gets one button when the agent skips the step or a person builds.
- A queued or running run gives no next action. — Waiting is not an action for this person.
- A done run gives no new action. A blocking MUST outcome comes back through its blocking thread and the questions screen. A verified run, or a not-verified run with no blocking outcome, gives nil. — The path already exists; the rule adds nothing to it.
- A doc with no trace ID whose prefix is in `verify.prefixes` never gets `verify`. — A run cannot start on it.
- The `verify` action opens History and focuses the verify field, prefilled as today. — The field stays where it is; the action finds it.
- The rule sits in `nextaction.Of`, after `handoff`, for one bundle only, as `handoff` does. The bundles list does not compute it. — One rule for the web app, the TUI and the MCP server; the list stays cheap.
- Only a person who can edit gets `verify`. — It is an author action, as `handoff` is.

## Out
- No change to the verification run, its outcomes, its waivers or its blocking threads.
- No automatic run started by Speccy without a call from the agent or the author.
- No change to `blocked` and `note` reports.
- No move of the verify field out of History.

## How I know it works
- After "Hand it to a builder" on a Build Ready doc, the next action reads "Verify the build". A click opens History with the verify field focused and prefilled.
- A HANDOFF.md from a new build packet ends with the verify step and names the handoff ID.
- An agent that calls `verify_build` with the handoff ID starts a run; while it runs the next action is empty.
- A run with a MUST missing gives "Answer 1 question". A verified run leaves no next action.
- An edit of the doc after a verified run, then a new review and a new handoff, brings back "Verify the build".
- The TUI and the MCP `get_bundle` show the same next action as the web app.
- `just verify` passes.
