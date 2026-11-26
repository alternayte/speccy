# Backend call timeout

Issue: #181.

## What it does
Each backend in Admin → Models has a call timeout: the limit for one model call to it. A new or existing backend has 300 seconds. When a call inside a review hits the limit, the gateway tries it once more if the run's time leaves room for a full try. When the call still fails, `run_error` names the backend, the limit, and what to change. A rubric over a 38 KB doc completes on a slow but healthy backend whose limit is high enough.

## Decisions
- The timeout is the field `call_timeout_seconds` in the backend's `config` jsonb, from 30 to 600 — the slow thing is the model, and the admin who sees the error can change it with no redeploy. No migration: a row with no value has 300.
- `Backend` and `BackendInput` in the OpenAPI carry `call_timeout_seconds`. A value outside 30–600 is refused with 400 `bad_timeout`.
- The backend form in Admin → Models shows the field in seconds, with the hint "The limit for one model call. Raise it for a slow model."
- The default goes from 120 to 300 seconds — a 38 KB rubric on a reasoning model passes 120, and 300 matches the proxy read timeout the hosted docs already ask for.
- The upper limit is 600 seconds — the vendor SDK default (Anthropic and OpenAI, read 2026-11-26), and it leaves a 15-minute run room for its other stages.
- A call that hits the limit inside a run with a deadline gets one more try when the deadline is later than now plus the limit — the issue's case. A call with no deadline gets no retry — such a call runs inside an HTTP request, and a second wait only makes the proxy cut it.
- The room `retryable` keeps before the deadline for other transient errors is a quarter of the backend's limit, not of the fixed 120 seconds.
- The error reads "the <backend> backend did not answer within <limit>. Raise its call timeout in Admin → Models, or assign the <role> role a faster model." — the issue asks for advice on what to change.
- The **Test** button uses the backend's limit.
- The timeout applies to every backend kind, `agent_cli` too.
- Reviews, breakdown proposals, thread answers and verification runs are jobs, so no proxy cuts them. Suggest fix, fix all, check conflicts, diff summary, the ask draft and **Test** run inside the HTTP request. A proxy cuts them at its own read timeout, and the app names the status (docs/specs/breakdown-proposal-job.md). The request context ends when the proxy closes the connection, so the model call stops.
- A pod that stops cancels its running jobs, and River tries them again later, as today. A longer limit changes nothing there.
- The hosted docs say: set the proxy's read timeout to at least the largest call timeout of a backend, 300 seconds by default. nginx: `proxy_read_timeout` and `proxy_send_timeout`, 60 s by default. ingress-nginx: the `proxy-read-timeout` and `proxy-send-timeout` annotations. Traefik: `respondingTimeouts.writeTimeout` is 0 (no limit) by default; when it is set, set it to at least the same value. All read 2026-11-26.

## Out
- Streaming the model answer, and an idle timeout.
- An environment variable for the timeout.
- Splitting the rubric into several calls for a large doc.
- A timeout for each role.

## How I know it works
- Admin → Models shows 300 for an existing backend. Saving 900 shows the `bad_timeout` message. Saving 400 stores it, and the form shows 400 after a reload.
- A fake backend that never answers, with a limit of 30, fails a review stage after two tries with the new message, which names Admin → Models.
- **Test** on that backend fails after 30 seconds with one try.
- A full review of the 38 KB SDD from #181 on the OpenRouter GPT backend with a limit of 400 completes the rubric stage.
- The hosted-mode docs page shows the nginx, ingress-nginx and Traefik settings.
