# MCP long reviews

Issue: #177.

## What it does
`review_bundle`, `review_content` and `review_url` wait up to 45 seconds for the result. A review that ends in that time returns its result in the same call, as today. A longer review returns `status: running` and a `run_id`, and the agent then calls the new tool `get_run` with that ID. `get_verdict` names a queued or running review of the doc as `pending_run`, so an agent can tell a review that still runs from one that was lost. No client timeout drops a review result.

## Decisions
- A start tool waits at most 45 seconds — Cursor's agent path stops a `tools/call` at 60 seconds and ignores progress (Cursor forum, 2026-07-02), and its remote HTTP path fails at about 300 seconds.
- After 45 seconds, the tool returns `{"status": "running", "run_id": "<id>", "stage": "<stage>", "next": "Call get_run with run_id until status is complete or failed."}`. It is not an error. The review keeps running on the server.
- `get_run` takes `run_id`. It answers `queued` or `running` with the current stage. When the run ends, it answers with the same result that the start tool gives for a review that ends in time. A failed run is a tool error with the reason.
- `get_run` takes the ID of a bundle run and of a content or URL review request. It reads the bundle run first, then the review request — the agent keeps one ID and one tool for each kind of review.
- `get_run` waits up to 45 seconds too, so an agent that calls it again and again makes few calls.
- `get_verdict` adds `pending_run` with `id`, `status` and `started_at` when the doc's newest full or partial run is queued or running. It reads `listRuns` of the doc.
- Progress notifications stay — Claude Code resets its idle timer on progress.
- The MCP tasks extension is out — the Go SDK v1.8.0 has no tasks support, and Cursor does not list it.
- The tool descriptions of the three start tools say what to do with `status: running`.

## Out
- The MCP tasks extension.
- A wait time that the agent sets.
- Cancelling a review over MCP.
- Changes to the HTTP API.

## How I know it works
- `review_bundle` with `stages: []` on a small bundle returns the verdict in one call.
- `review_bundle` on a 20 KB SDD with every stage returns `status: running` and a `run_id` after 45 seconds. `get_run` with that ID returns `running` and later the verdict, the same shape as a review that ends in time.
- `get_verdict` during that review shows `pending_run` with the run's ID. After the run ends, `pending_run` is gone, and the verdict is the new run's.
- `review_content` on the same SDD returns `status: running` and an ID. `get_run` with that ID returns the content review result.
- From Cursor, a full review of the SDD in issue #177 gives no -32001 error.
