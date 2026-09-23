# Verify from a link

## What it does
A person starts a verification run from the bundle page. They paste a GitHub URL, or they accept the repo that the field already holds. Speccy shows the resolved repo and commit, then runs the gate as a job with progress on the page. The CLI and the MCP tool take the same URL, and with no URL they use the bundle's `implemented-by` link. A drifted code link offers a run at the commit that drifted.

## Decisions
- One field takes any URL that `ParseURL` in `internal/source/github/url.go` accepts, plus `.../commit/<sha>` and `.../pull/<n>` — a person pastes the URL from the address bar, and one parser serves the web, the CLI and MCP.
- A repo URL reads the head of the default branch. A tree or blob URL reads the head of its branch. A commit URL reads that commit. A pull request URL reads its head commit — each URL names one build, and the run records the SHA it read.
- The folder or file in a URL does not narrow the scan — the gate reads the whole tree, because code often implements a requirement outside the linked folder.
- Speccy shows the resolved repo, branch and short SHA before the run starts — a person must see what gets checked before Speccy makes model calls.
- The field prefills from the bundle's `implemented-by` link. With no link, it prefills from the repo of the last verification run. With two or more links, it offers each linked repo — the doc already names its code, and the run row already stores the last repo.
- A pasted URL writes nothing into the doc — Speccy writes no file in an adopted repo, and one run against a fork does not move the doc's code.
- In local mode the field also takes an absolute folder path. Hosted mode refuses a path with "Paste a GitHub URL. The server cannot read your disk." — the API already reads a folder in local mode, and unpushed code must be checkable.
- `links.code-drift` gets a control, "Verify at <short sha>", which opens the form with that commit — drift says the code moved, and the next question is whether it still conforms.
- Drift does not change the next action, and Speccy never starts a run by itself — drift is a SHOULD, and each run makes model calls for every trace ID on every push.
- A run is a job, like a review run. `POST /bundles/{id}/verifications` returns 202 with the run ID, and the page shows progress by trace ID over the existing broker — a request that makes model calls for 30 trace IDs outlives a hosted proxy timeout.
- The CLI, MCP and `speccy action --verify` wait for the job to end — their output and exit codes stay as they are.
- `speccy verify <bundle> [<url or folder>]` takes the URL as an argument. `--repo` and `--sha` still work — scripts and the README already use them.
- The permission to start a run is the permission to start a review run — both spend the workspace's model budget on one bundle.
- A GitHub credential failure uses the messages of the source URL entry: no `gh`, no login, or no access — a person cannot fix a failure that does not say what failed.
- The Verification panel shows when a bundle has no runs, with the field and the Verify control — today the panel hides itself when it is empty, so there is no way in.

## Out
- No automatic run on drift, on push, or on a new version.
- No builder claims from the web form. Claims come from the CLI and MCP.
- No handoff attached to a web run.
- No narrowing of the scan to a path.
- No test execution. A cited test stays a citation.

## How I know it works
- On `payments-sdd` with `implemented-by: github:acme/payments#internal/pay`, the Verification panel shows the field prefilled with `acme/payments`. Verify shows the default branch and a short SHA, and the run then appears with progress by trace ID.
- Paste `https://github.com/acme/payments/pull/12`. The confirmation names the PR's head SHA, and the finished run records that SHA.
- Paste `https://github.com/acme/payments/blob/main/internal/pay/retry.go`. The run reads the whole tree at the head of `main`.
- In local mode, paste `/Users/you/code/pay`. The run lists "a folder" as its target. In hosted mode, the same input shows the refusal sentence.
- A drifted code link shows "Verify at <short sha>". The control opens the form with that commit.
- `speccy verify docs/specs/pay` with no URL verifies the linked repo at its default branch head. `speccy verify docs/specs/pay --repo acme/pay --sha <sha>` gives the same output as before.
- A private repo that the token cannot read shows "the token has no access to that repo", not "not found".
