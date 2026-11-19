# Hosted PR review

## What it does
A hosted Speccy reviews the pull requests of each repo that has `.speccy.yaml` and `mode: app`. The workspace's GitHub App does the review, so the repo needs no workflow and no secret. The App posts the summary comment, the inline comments and one check per bundle, and it acts on reply commands as soon as they arrive. Every review runs as a River job in one of two queues, so 30 people and a burst of pushes share the instance and nobody waits behind bot work. The standalone Action stays for repos without a server. Connected mode is removed.

## Decisions
- The App reviews a PR on `pull_request` opened, synchronize and reopened, for a repo whose `.speccy.yaml` exists and whose `mode` is `app` or absent. This replaces the "PR review by the server" line under Out in github-app.md. Reason: one file turns it on, and a fork PR needs no Actions secret.
- `mode: app`: the Action exits at once with "The Speccy App reviews this repo". `mode: action`: the App skips the repo. `speccy init --github` asks which one and writes the key. `--reviewer action|app` answers it, and with no terminal it picks action and says so. Reason: one explicit key, with no race between the two reviewers.
- The App reads the repo at the head SHA as a `FromRepo`: the sidecar, `.speccy.yaml`, the sibling spec docs and the open pull requests for link rules. Reason: the review sees all the repo context that standalone sees.
- `enforcement`, `adoption.relaxed` and `mode` come from the base branch. `map`, `size` and the link rules come from the head. A PR that adds `.speccy.yaml` for the first time uses the head. Reason: a PR cannot change its own gate.
- The sidecar comes from the head. Reason: `/speccy waive` writes there, and approving the PR is the human check (DEC-009).
- The App review uses the workspace profiles. A repo `.speccy/profiles` folder is ignored, and the summary says so once, with a link to Admin → Profiles. Reason: one versioned profile per key keeps the maintainers, Insights and the false-ready rate true.
- The App subscribes to `issue_comment` and `pull_request_review_comment`. A reply command starts a job at once. The App commits the sidecar or `.speccy.yaml` change to the head branch with `requested_by` set, and it commits nothing to a fork. Reason: a reply no longer waits for a push, and a sidecar is not a doc.
- The manifest adds Checks write and the two comment events. When the App lacks them, Admin → GitHub says "The App lacks Checks: write" and links to the App's settings page. Reason: GitHub has no API to change the permissions of an existing App.
- Each check is named `speccy: <bundle slug>`. The docs tell the admin to set the App as the expected source in branch protection. Reason: existing branch protection rules carry over by name.
- A report of an App review is kept for 90 days, and the summary links to it. Reason: the same rule as other unsaved reviews.
- A new push cancels the queued or running review of the older SHA on the same PR. Reason: an old result can no longer count.
- When the `pr` budget is spent (hosted-operations.md), the check fails in blocking mode and names the budget. Reason: a review that cannot finish fails, and lint alone can show Build Ready while a material gap is open.
- River replaces the `job` table and the worker. Hosted uses `riverdatabasesql` in poll-only mode, and local mode uses River's SQLite driver. The conformance suite covers queue behaviour on both. Reason: one queue, with separate queues, cancel and River UI; the `database/sql` driver takes Speccy's own transactions, so a job exists only when the work that asked for it commits, and River shares the one pool.
- There are two queues, `interactive` (UI, CLI, MCP, thread answers) and `pr` (App). Admin → Workspace settings has "Reviews at a time" for each one. Reason: neither group starves the other.
- Each running review keeps its own limit, "Model calls at a time" (default 8). Reason: one shared call limit stretches runs past the 15-minute `RunTimeout`.
- On a 429 or a 5xx, the gateway waits for `retry-after` when it is present. Without it, the gateway backs off. It retries until the run's time ends. Reason: a burst against the provider's per-organization limit slows the review down instead of failing it.
- `POST /reviews` and the MCP `review_url` tool insert a job and return `202` with a review id. `GET /reviews/{id}` returns the state, then the result. `speccy review --server` polls. The MCP tools wait for the job and send progress notifications. Reason: every model call goes through River, and no proxy timeout cuts a review.
- The Action loses the `server` and `token` inputs. `mode: connected` fails with a message that names `mode: app`. Reason: nothing calls the server from CI any more.
- Hosted stays on one replica with the `Recreate` strategy. River retries a run that a deploy interrupted. Reason: model throughput is the limit, not the process.
- The broker drops a run's events 5 minutes after the run ends. Reason: today it keeps every run in memory.

## Out
- More than one hosted replica, and a `run_event` table.
- Workspace or CI API tokens. People keep personal `spy_` tokens.
- One model-call limit shared by the whole workspace.
- Repo profiles in hosted mode.
- Commits to fork branches.

## How I know it works
- Install the App on a repo with `.speccy.yaml` and no workflow. Open a PR that changes a spec doc. A `speccy: <slug>` check from the App, a summary comment and inline comments appear.
- Reply `/speccy waive <reason>` to an inline comment. A sidecar commit lands on the head branch with no push from a person. The next review counts the finding as waived, and a blocking check turns green.
- Push twice in quick succession. River UI shows the first job cancelled, and only the second posts.
- Set `mode: action`. The App posts nothing. Set `mode: app` in a repo with the Action workflow. The Action logs "The Speccy App reviews this repo" and posts nothing.
- A PR that sets `enforcement: advisory` on a repo whose base says `blocking` still fails a Not Build Ready check.
- Start 30 full reviews in the UI while 10 PR reviews run. The `pr` queue never runs more than its setting. A UI review starts within one slot of free capacity in `interactive`.
- `curl -X POST /api/v1/reviews` returns `202` behind an ingress with a 60s timeout, and `GET /reviews/{id}` later returns the verdict.
- Make the fake backend return 429 with `retry-after: 5` for 30s. The run finishes and does not fail.
- After 500 runs, the process heap returns to its baseline. `pprof` shows no `runEvents` for finished runs.
- `just verify` passes, with the queue conformance tests on SQLite and Postgres.
