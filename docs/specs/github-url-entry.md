# A GitHub URL as the way in

## What it does
A person pastes a GitHub URL into the bundles screen, or runs `speccy add <url>`. Speccy resolves it, shows the repo, the branch, the folder or the doc, and the profile it will use, and the person confirms. The result is a GitHub source with the bundles of that repo, in local mode and in hosted mode. Edits are drafts, and Publish opens a pull request. Local mode gets its token from the `gh` CLI.

## Decisions
- A URL makes a GitHub source, not a one-off review — "continue working from there" means edits, a verdict, and a way back, which is what a source already is.
- Local mode gets the same source, the same drafts, and the same publish as hosted mode — one code path for reading a repo.
- Speccy accepts `github.com/owner/repo`, `.../tree/<branch>/<path>`, `.../blob/<branch>/<path>/doc.md`, and `owner/repo` — the URL a person holds is the one from the address bar, which is usually a blob URL.
- A source says whether its path is a folder or one doc — a blob URL names one doc, and the scan rule for a folder does not fit it.
- A doc with no type and no mapping takes the profile from the dialog's guess, recorded in the source — a repo is other people's tree, so Speccy writes no type into it.
- Local mode runs `gh auth token --hostname <host>` for each call and stores nothing it returns — the machine already holds the credential, and revoking it in `gh` must take effect at once.
- With no `gh`, no login for that host, or no access, the dialog names which of the three it is and offers a fine-grained token to paste — a person cannot fix a failure that does not say what failed.
- A pasted local token goes in the `github_connection` row, encrypted with the key in `.speccy/state/` — local mode already holds secrets that way.
- Hosted mode never shells out. It uses the workspace connection (DEC-019) — a server has no person's CLI session.
- A member with no connection set gets one sentence and a link to the GitHub screen; an admin gets the screen — the fix belongs to an admin, and the member must know that.
- A URL the token cannot read says the token has no access to that repo — GitHub answers 404 for a private repo, and "not found" sends a person to look for a typo that is not there.
- A GitHub source writes no file into the served folder. Its versions live in the local store — a copy on disk with no git behind it becomes a second truth.
- Both entry points call one function — two ways in must not make two results.
- Both modes poll every 5 minutes while the app runs, and a source keeps its error when a sync fails — the reason to point Speccy at a repo is that other people change it.
- `speccy add` syncs once and exits — a terminal command does not own a background loop.
- `.speccy.yaml` in the repo still maps the docs and relaxes the checks — an adopted repo reads the same way through a URL as through a checkout.

## Out
- No GitHub App. §19 Q5 stays open, and the workspace token stands (DEC-019).
- No `git`, no `gh repo clone`, and no `gh api`. Speccy calls `gh` for a token only (DEC-018).
- No write to the source branch, and no write of any kind into the repo other than a publish pull request.
- No change to the review stages, the verdict rule, the sidecar, or the Action.
- No import of a pull request's own review comments into Speccy's threads.

## How I know it works
- Paste `https://github.com/<owner>/<repo>/blob/main/docs/prd-payments.md` into the bundles screen of local mode. The dialog names the repo, the branch, the doc, and the profile. Confirm, and the bundle opens with a verdict.
- Run `speccy add https://github.com/<owner>/<repo>/tree/main/docs` in a folder. It prints the bundles it made, and they are on the bundles screen.
- Log out of `gh` and paste a URL. The dialog says `gh` is not logged in for that host, and offers the token field. Paste a token, and the source works.
- Paste a URL for a repo the credentials cannot read. The dialog says the token has no access to that repo.
- Edit a doc of a GitHub source in local mode. The served folder gains no file. Press Publish. A pull request opens on the repo, and the source branch does not move.
- Change the doc on GitHub. Within 5 minutes the bundle shows the new version, or the source shows the error of a failed sync.
- Paste a URL with no connection set in hosted mode, as a member. One sentence names the admin screen. As an admin, the screen opens.
