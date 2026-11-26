# Breakdown proposal job

Issue: #183.

## What it does
**Propose breakdown** runs as a background job, as a review does. The request answers at once, and the model call runs on the server whatever happens to the request. The breakdown page shows "Proposing" with the time it started, and shows the breakdown when the job stores it. A failed proposal shows its reason. Behind a proxy that cuts requests at 60 seconds, a proposal of a long doc completes. When a response is not a Problem, the app names the HTTP status, and keeps "could not reach the server" for a real network failure.

## Decisions
- `POST /docs/{docId}/breakdown` answers 202 with the proposal: `id`, `status` (queued, running, complete, failed), `error` and `created_at` — the review request pattern, so no proxy timeout cuts the model call.
- One table, `breakdown_proposal`, holds one row for each proposal: doc, status, error, acknowledged, created_by, created_at, finished_at. A River job of kind `breakdown_propose` in the interactive queue runs it with the creator as the actor. The conformance suite covers the table on SQLite and Postgres.
- `GET /docs/{docId}/breakdown/proposal` returns the newest proposal of the doc, or 404 when it has none — `GET breakdown` answers 404 for a doc with no breakdown yet, so it cannot carry the state of a first proposal.
- A doc with a queued or running proposal refuses a second one with 409 `proposal_running`. One model call for each doc at a time.
- The checks that refuse a proposal at once stay in the request: the verdict gate, the role assignment, and pushed issues. Only the model call and the store move to the job — the person sees a refusal at once.
- A proposal older than 90 days is deleted when a new one is queued — the `review_request` rule.
- The job reads the doc's current version when it runs, and the breakdown records that version, as today. A job starts within seconds of the request.
- The orphan handler marks a proposal failed when its job ends with no result, with the reason "The server stopped during the proposal. Propose again."
- The breakdown page polls the proposal every 3 seconds while it is queued or running, then reads the breakdown.
- `propose_breakdown` over MCP waits up to 45 seconds. A longer proposal answers `status: running`, and says to call `get_breakdown` — the rule of docs/specs/mcp-long-reviews.md. `get_breakdown` on a doc with a running proposal answers `status: running` and waits up to 45 seconds too.
- `speccy breakdown propose` waits for the job and prints the breakdown, as today.
- `problemMessage` in the web app: a response that is not a Problem gives "The server answered <status> <text>." A 502, 503 or 504 adds "A proxy in front of Speccy may have cut a long request." A `TypeError` from fetch keeps "Speccy could not reach the server."
- The hosted-mode docs say that a proxy must allow `proxy-read-timeout` of at least 300 seconds for the run event stream — the event stream holds a request open.

## Out
- Streaming the breakdown as the model writes it.
- Cancelling a running proposal.
- A job for the other single model calls: suggest fix, summarize diff, ask.

## How I know it works
- Propose a breakdown of a 37 KB Build Ready SDD behind a proxy with a 60-second read timeout. The page shows "Proposing", and later the breakdown, with no error.
- Reload the page during the proposal. It still shows "Proposing", and later the breakdown.
- Propose again while a proposal runs. The second request gets 409 `proposal_running`.
- A proposal whose model call fails shows its reason on the page, and **Propose breakdown** is available again.
- A doc that is not Build Ready is refused at once with its MUST count, and no job is queued.
- `propose_breakdown` over MCP on the same doc answers `status: running` after 45 seconds. `get_breakdown` later shows the breakdown.
- A proxy that answers 504 shows "The server answered 504 Gateway Timeout. A proxy in front of Speccy may have cut a long request."
