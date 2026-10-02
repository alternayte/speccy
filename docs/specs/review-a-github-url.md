# Review a GitHub URL

## What it does
An agent or a reviewer reviews the docs of a GitHub file, folder, commit or pull request with no copy by hand. Speccy reads the files at the head commit with the GitHub credential it holds, reviews them, and saves no bundle. A reviewer on a laptop posts the findings of a pull request as one pending review, which only that reviewer sees. Issues #92 and #91.

## Decisions
- A review of a GitHub URL saves no bundle. It is the unsaved review that `review_content` runs, with the 90-day report. — A pull request lives for days, and a source for each one leaves bundles that nobody owns.
- One API operation and one MCP tool, `review_url`, take the URL of a file, a folder, a branch, a commit or a pull request. — One read serves the agent and the CLI.
- For a pull request, Speccy reviews the spec docs that the pull request changes. For a folder, it reviews the spec docs under it. — A reviewer wants the docs of the change, not the repo.
- Speccy stops when the URL names more than 20 spec docs, and says to name a folder or a doc. — Each doc is a full review with model calls.
- The review uses `.speccy.yaml` and the sidecars of the repo at that commit, and the profiles of the Speccy workspace. — A GitHub source reads them the same way.
- A link from one reviewed doc resolves to another spec doc of the same commit, and then to the saved bundles. — An SDD and its PRD often change in one pull request.
- The answer names the repo, the commit, and each doc with its findings in the `get_findings` shape, with the path in the repo and the lines. — An agent or a script puts each finding on its line.
- `speccy action --pr <url> --pending` reviews the pull request with the local state and models, and posts one pending review. It creates no check run and no summary comment. — A pending review shows to its author only, so a reviewer checks the findings before the author sees them.
- `--pr` needs `--pending` or `--dry-run`. `--dry-run` prints the review as JSON and posts nothing. — A laptop run must not post what the Action posts.
- The pending review uses the comment rules of the Action: levels, relaxed checks, waivers and `pr.inline_limit`. A finding on a line outside the diff goes into the review body. — GitHub takes a comment only on a line of the diff.
- The token is `GITHUB_TOKEN`, or the login of `gh`. — Local mode already reads GitHub with the `gh` login.

## Out
- No fix loop and no carried findings on such a review. Add the branch as a source for those.
- No reply commands, no check runs and no summary comment from `--pr`.
- No review of a doc on a host other than GitHub.

## How I know it works
- `review_url` with a pull request URL gives the repo, the head commit, and one entry for each spec doc the pull request changes, each with findings that have a line.
- `review_url` with the URL of one doc at a branch gives one entry. The bundle list of the app has no new bundle.
- `speccy action --pr <url> --dry-run` prints JSON with the commit, the body and the comments, and GitHub has no new review.
- `speccy action --pr <url> --pending` makes one review that shows "Pending" to its author, with comments on changed lines.
- `speccy action --pr <url>` with neither flag stops with a usage message.
