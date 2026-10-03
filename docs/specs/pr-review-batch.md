# Pull request review at scale

## What it does
A reviewer reviews many spec pull requests in one batch, from the CLI or from an agent through MCP. Speccy posts the result of each pull request as a pending review that only the reviewer sees. Each comment leads with a plain question or fix for the author. The reviewer adds their own concerns as precise questions with `speccy ask`, and lists, deletes or discards pending comments. All of it runs in local mode with the reviewer's own GitHub login.

## Decisions
- `speccy review-prs <PR URL>... | --repo <owner/name> [--requested] [--parallel N] [--again] [--yes] [--stages …]` starts a batch. MCP `review_prs` takes the same inputs — one batch for both surfaces.
- With `--repo`, the batch takes each open pull request that is not a draft and changes a spec doc. `--requested` keeps only those where someone asked for the reviewer's review — a draft has not asked for review.
- `--parallel` defaults to 3. Within one pull request, the spec docs also review in parallel under the same limit — the batch is useful only if it is faster than one by one.
- Before it starts, the CLI prints the count of pull requests and the cost estimate, and asks for confirmation. `--yes` skips it — the reviewer sees the spend before it happens.
- The store records each pull request review: repo, number, head SHA, time, batch ID. The batch skips a pull request already reviewed at its head SHA, unless `--again` — a second run by mistake costs nothing.
- The store holds a batch record with the state of each pull request: waiting, reviewing, posted, skipped (with the reason), failed (with the error), and the verdict and comment count when done — the agent reads progress and does not block.
- MCP `review_prs` returns the batch ID and the estimate at once. `get_batch` reads the state. `cancel_batch` stops new pull requests from starting — one MCP call must not run past the client timeout.
- The batch runs in the owner. It goes on when the agent session ends. The CLI prints each pull request when it finishes and returns when the batch ends — a client process and an agent see the same batch.
- The batch obeys the monthly token budget. With no budget set, there is no limit. At the limit, it starts no new pull request and marks the rest skipped for budget — no second cap to configure.
- When the reviewer has a pending review on the pull request, the batch adds its comments to it through GraphQL. It does not change the reviewer's own comments. It skips a finding whose key marker is already in that review — GitHub allows one pending review per person, and no draft is lost.
- After a review of every stage, the batch deletes Speccy's finding comments in the pending review whose finding is gone, in the folders of the bundles that reviewed without a failure. It keeps the reviewer's comments and questions — a fixed finding must not stay in front of the author.
- One comment format for the pending review and for the Action in CI. An answer finding leads with its question. A reword finding leads with its fix, as a suggestion block when Speccy can make one. Any other finding leads with its message. The level and the check slug go on a last small line that links to the check catalog — authors read CI comments most, and one format is learned once.
- `speccy ask <PR URL> "<concern>" [--force]` and MCP `ask_author`: the reviewer model finds the section in the spec docs of the pull request that the concern is about, and writes one precise question for the author. Speccy adds it on that line in the reviewer's pending review, and creates the pending review when none exists — the reviewer adds questions while reading.
- When two sections fit the concern equally, `ask` posts nothing and returns both heading paths. The reviewer names one with `--section` — a question on the wrong section is noise.
- When the doc already answers the concern, `ask` posts nothing. It returns the quote that answers it and the question it would have asked. `--force` posts it anyway — no comments based on nothing.
- A question on a line outside the diff goes into the body of the pending review — GitHub takes a line comment only in the diff.
- `speccy pending list <PR URL>` and MCP `list_pending` give each comment of the reviewer's pending review: ID, path, line, first words, and whether Speccy wrote it. `speccy pending delete <PR URL> <ID>...` and `delete_pending` delete the named comments. `speccy pending discard <PR URL>... | --repo <owner/name> | --batch <ID>` and `discard_pending` discard whole pending reviews — the agent lists first, then acts on what the reviewer names.
- These tools act on any comment in the reviewer's own pending review — it is the reviewer's private draft.
- In hosted mode, every command and tool above answers "Pending reviews post as you, so they run in local mode." The new tables exist in SQLite and Postgres — a pending review belongs to the person whose credential posts it, and hosted mode holds one workspace token. The conformance suite covers both stores.
- `speccy action --pr` stays as it is — one pull request, with `--dry-run` to print.

## Out
- Submitting a pending review. The reviewer submits on GitHub.
- Touching a submitted review or any comment the author can see.
- Draft pull requests.
- Batches in hosted mode and in the GitHub Action.

## How I know it works
- `speccy review-prs --repo <owner/name> --yes` on a repo with 5 open spec pull requests: 3 run at once, and each pull request gets one pending review that only the reviewer sees on GitHub.
- The same command again with no new commits: all 5 show as skipped, already reviewed at their SHA, and no model call runs.
- A push to one pull request, then the command again: only that pull request is reviewed.
- From Claude Code with `speccy mcp`: "review the open spec PRs in <repo>" returns a batch ID, and "how is the batch doing" lists each pull request with its state.
- A comment on GitHub for an answer finding starts with a question, and its last line holds the level and a check slug that links to the check catalog. A CI run of the Action shows the same format.
- `speccy ask <PR URL> "retries unclear when the provider times out"` adds one question on the retry section of the reviewer's existing pending review. The reviewer's own draft comments are unchanged.
- `speccy ask` with a concern that the doc answers prints the answering quote and posts nothing.
- `speccy pending list`, then `delete` with one ID, removes that comment only. `discard --batch <ID>` removes every pending review the batch made.
- `speccy serve` in hosted mode, then `review_prs` over MCP, returns the local-mode message.
