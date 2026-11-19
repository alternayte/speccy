# Hosted operations

## What it does
A hosted Speccy on Kubernetes gives each queue a budget of its own, so PR work cannot stop people and people cannot stop PR checks. The admin sees model spend per queue, per person and per repo. The server keeps its Postgres connections and its model cache bounded. It reports ready only when it can serve. River UI under Admin shows the queues.

## Decisions
- The `budget` row holds one monthly token limit for each queue: `interactive` and `pr`. Admin → Models has two limits. Reason: a PR flood cannot stop people, and an agent loop cannot block merges.
- A spent `pr` budget fails the PR check in blocking mode and names the budget. A spent `interactive` budget refuses new UI, CLI and MCP reviews with the existing `budget_spent` problem. Reason: each queue stops on its own budget only.
- Each model call records its tokens with the queue, the person (or the App) and the repo when there is one. Admin → Models shows this month's spend by queue, by person and by repo. Reason: the admin sees where the budget went.
- An existing workspace limit becomes the `interactive` limit. The `pr` limit starts at the same value. Reason: an upgrade does not stop reviews.
- At 80% of either limit, the workspace channel gets one message (channel-notifications.md). Reason: the admin acts before checks fail.
- The Postgres pool opens at most 20 connections. `SPECCY_DB_MAX_CONNS` overrides it. River's pgx pool counts inside the same number. Reason: a burst does not use up Postgres `max_connections`.
- `cache_entry` gets `last_used_at`. A cache hit updates it. A daily River periodic job deletes entries not used for 90 days. Reason: the cache grows without limit today.
- `/readyz` pings Postgres and checks that the River client runs. It answers 503 otherwise. `/healthz` stays a process check. The k8s sample uses `/readyz` for readiness and `/healthz` for liveness. Reason: a pod that cannot reach Postgres stops getting traffic.
- River UI is mounted at `/admin/queues`, for admins only, in hosted mode. Reason: the queue depth and failed jobs are the view an operator needs.
- No Prometheus endpoint. Reason: River UI and the JSON logs cover the queue, which was the missing view.

## Out
- Budget caps per person or per repo.
- Prometheus or OpenTelemetry export.
- River UI in local mode.

## How I know it works
- Set the `pr` limit low. Push to a PR until the limit is spent. The PR check fails and names the budget. A review in the UI still runs.
- Admin → Models shows spend this month by queue, by person and by repo, and the totals match the sum of the run costs.
- Upgrade a workspace with a limit of 5M tokens. Both queue limits read 5M.
- Run 40 reviews at once against a Postgres with `max_connections=50`. `pg_stat_activity` never shows more than 20 Speccy connections.
- Set an entry's `last_used_at` to 91 days ago and run the periodic job. The entry is gone. A recent entry stays.
- Stop Postgres. `/readyz` returns 503 within one probe period, and `/healthz` still returns 200.
- Open `/admin/queues` as an admin to see River UI. A member gets 403.
