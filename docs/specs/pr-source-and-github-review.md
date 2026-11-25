# A pull request as a source, and a GitHub review from the app

## What it does
A person pastes a pull request URL into "From GitHub". Speccy makes a source on that pull request, with one bundle for each spec doc the pull request changes, and the pull request author as the author. The reviewer runs the review and opens "Prepare GitHub review". They pick the Questions screen items, the MUST findings or all findings, untick what they do not want, and confirm. Speccy writes a pending review on the pull request as the reviewer, with no comment that the pull request already holds. The reviewer submits it on GitHub, and the author's replies show on the matching items in Speccy. The repo needs no `.speccy.yaml`. An agent runs the same flow through MCP, with no UI.

## Decisions
- The source URL accepts `.../pull/<n>`. The source records the pull request number, title, author login and author GitHub ID, and it reads the docs at the head SHA — the pull request is the thing a reviewer holds.
- The dialog lists the markdown files that the pull request changes in the import picker, with a profile guess and "Not a spec" for each — the same question as import and adoption, asked the same way.
- A spec doc that the pull request adds later is a skipped doc, offered for adoption — the reviewer chose the docs, and a push must not add a review to pay for.
- The `pull_request` webhook syncs the source on synchronize, from the base repo, so a fork works too. Polling stays — GitHub does not retry a webhook.
- A sync makes a new version and starts no review. The next action becomes "Review again" — a trial must not spend on each push.
- On merge, the source moves to the base branch, and each bundle keeps its versions, threads and author. When a source on the base branch already covers a path, that bundle becomes read-only and links to the bundle there — the trial carries into main, and one doc never has two live bundles.
- On close without merge, the source stops syncing and says "Pull request #N closed". The admin keeps or deletes it — the review record stays for Insights.
- Draft pull requests are accepted — a person starts the review, not a webhook.
- The pull request author is the bundle author. Speccy matches the author's GitHub ID to a linked GitHub account, or at that person's first GitHub sign-in. The person who pasted the URL becomes a named reviewer — the person who raised the pull request owns the doc.
- The source row shows "PR #N", the title and the author. Under it, each spec doc shows its path, profile name and verdict — the PRD and the SDD results stay apart.
- "Prepare GitHub review" sits on the control row of each bundle of a pull request source. It covers every bundle of the pull request — GitHub allows one pending review per person per pull request.
- The dialog has three presets: Questions screen items (the default), MUST findings, and all findings. Each preset fills a checklist grouped by spec doc, with the comment text of each item. The reviewer unticks items, then confirms — the reviewer reads each comment before GitHub holds it.
- A finding with an open pull request thread that holds its key marker is not in the checklist. A finding that the reviewer's pending review already holds is not in it either — one comment per finding, whoever posted it first.
- A line with human comments shows "N comments on this line already" on its item — a comment without a marker cannot be matched to a finding, and the reviewer decides.
- Comments use the format of pr-review-batch.md. A material gap leads with its build question and its two readings. A blocking thread leads with its text. Each comment links to the bundle and to the check catalog — the author sees the question first.
- An item on a line outside the diff goes into the review body — GitHub takes a line comment only in the diff.
- Speccy writes the pending review with the `prreview` post path and `action.Merge`. It adds to the reviewer's existing pending review, and it deletes Speccy's comments whose finding is gone. It never changes a submitted comment — one code path for the CLI and the app.
- In hosted mode Speccy posts with the reviewer's GitHub user token. A person with no GitHub link starts the link OAuth first. A token workspace refuses with "A GitHub review posts as you. Connect the GitHub App in Admin → GitHub." — a comment must never carry another person's name. Local mode uses the `gh` token.
- This replaces "Pending reviews in hosted mode. They stay refused." under Out in github-app.md. The reason given there, one workspace token, no longer holds.
- Speccy reads the replies on threads that hold its key marker, from the `pull_request_review_comment` webhook and at each sync. Each reply shows read-only on its finding and Questions screen item as "@login on GitHub: …" — the author stays on GitHub, and the reviewer sees the answer.
- A reply has two controls for a person who can edit: "Not needed" and "Ship it open", the questions screen's words for a waiver. Each one prefills the reason with the reply text — the reviewer does not copy it by hand.
- This replaces "No import of a pull request's own review comments into Speccy's threads" under Out in github-url-entry.md, for marked threads only.
- MCP `add_source` and `speccy add --server <url>` take a source URL, a pull request URL too, with the doc picks and profiles. They call the function of the "From GitHub" dialog — an agent gets the same source as a person.
- MCP `prepare_github_review` takes a pull request URL, a preset, and the finding IDs to leave out. With `dry_run` it returns the checklist and each comment text, and posts nothing. Without it, it writes the pending review — the agent shows the dry run to the reviewer, the same human check as the dialog.
- In hosted mode, `list_pending`, `delete_pending`, `discard_pending` and `ask_author` work with the GitHub user token of the person who owns the `spy_` token. They answer "Link GitHub under Account" for a person with no link — the reason for local only is gone.
- `get_questions` and `get_findings` return the GitHub replies of each item — an agent sees the author's answer where a person does.
- `review_prs` and its batches stay local only — a trial must not start a run over every pull request of a repo.

## Out
- Reply commands, and any commit to the pull request branch, for a repo with no `.speccy.yaml`.
- Submitting the review. The reviewer submits on GitHub.
- Changing or resolving a submitted comment.
- Matching human comments to findings by their text.
- Turning an existing branch source into a pull request source.
- Batches of pull requests in hosted mode.

## How I know it works
- In hosted mode with the App, paste the URL of a pull request that adds a PRD and an SDD in a repo with no `.speccy.yaml`. The picker lists both files with their guessed profiles. Confirm. The source row shows the pull request number, the title, the author, and two docs with their profiles.
- Sign in as the pull request author with GitHub. Both bundles list that person as the author.
- Run both reviews, then open "Prepare GitHub review" with the default preset. The checklist lists the Questions screen items under the PRD and under the SDD. Untick one and confirm. On GitHub, the reviewer sees one pending review with the ticked items, and the author sees nothing.
- Submit it on GitHub, push a fix for one finding, review again, and prepare again with "All findings". The submitted findings are not in the checklist. The pending review holds only new findings.
- Reply to a submitted comment as the author. The reply shows on its Questions screen item within seconds. Press "Not needed". The reason holds the reply text, and the item closes.
- Merge the pull request. The source moves to the base branch, and each bundle keeps its versions and threads.
- From Claude Code with the hosted MCP server: "review the PRD and SDD in <pull request URL> and draft a GitHub review of the musts". The agent calls `add_source`, `review_bundle` for each doc, and `prepare_github_review` with `dry_run`, and shows the checklist. On "post it", one pending review appears on GitHub as the person who owns the `spy_` token.
- In hosted mode, `list_pending` on that pull request lists the comments, and `delete_pending` with one ID removes that comment only.
- In a token workspace, open "Prepare GitHub review". The dialog shows the sentence about the App and posts nothing.
