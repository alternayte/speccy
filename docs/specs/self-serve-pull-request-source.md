# Self-serve pull request source

## What it does
A member who is the author of a pull request, or a reviewer of it, adds a pull request source from its URL, with no admin. They use From GitHub in the app or `add_source` in the MCP server. Speccy checks the person against GitHub when they add it. When the pull request closes, or merges onto docs that main already holds, its bundles leave the bundle list. A "Closed pull requests" toggle shows them again. Their links keep working, so the Speccy links in a GitHub review never break.

## Decisions
- A member may add a pull request source when their linked GitHub account is the pull request's author, a requested reviewer, or a person who has submitted a review on it. GitHub's record grants the right, so no admin is needed for the people in the pull request. GitHub drops a requested reviewer from the list once they review, so a submitted review counts.
- Speccy reads the pull request's author, requested reviewers and reviews with the GitHub App, or the token, at add time. It checks no later change — the check gates the add only.
- A member with no linked GitHub account gets the error `github_not_linked`, which says to sign in with GitHub. A member who is none of the three gets `not_pull_participant`, which names the pull request — without an identity, Speccy cannot match the person.
- A member may add only a pull request URL. A repo, branch, folder or doc URL stays admin-only, with the error `admin_only` — those make workspace sources that no pull request bounds.
- `resolveGithubUrl` and `addGithubSource` become `member` in the role table. Each handler applies the rules above. `deleteGithubSource` and `syncGithubSource` stay admin-only.
- The member picks the profile of each doc, as an admin does. The adopted types they write apply to that source only.
- A pull request that is already a source returns the existing source to anyone allowed to add it — the current behaviour of `addPull`.
- On merge, the source follows the base branch, as today, whoever added it. A doc on main is a workspace doc, and an admin can delete the source.
- The bundle list leaves out a bundle when every spec doc in it is read-only, either because its pull request closed or because the doc moved to a bundle on main. `listBundles` takes `include_closed`, which is false by default. A bundle with one live doc stays in the list.
- The bundle list has a "Closed pull requests (N)" toggle. It shows the hidden bundles, each with a state chip: Closed or Merged. It is hidden when N is 0.
- A hidden bundle keeps its URL, its reviews, its threads and its Insights counts. Nothing deletes it. An author or an admin deletes it the usual way — a link in a GitHub review must keep working.
- A reopened pull request lifts its read-only state, as today. Its bundles come back to the list.
- In the app, From GitHub shows for every member. For a person who is not an admin, it accepts only a pull request URL, and says so under the field.
- The `add_source` MCP description drops "Admins only" and states the rules above.
- Local mode does not change: the local person is the admin.

## Out
- Self-serve for repo, branch, folder or doc sources.
- Archiving or deleting closed pull request bundles automatically, or on a schedule.
- Assignees, or people who only commented, as people who may add a source.
- Rechecking the right after the add, for example when a reviewer is removed.
- Self-serve sync or delete of a source.

## How I know it works
- A member whose GitHub account opened PR #25 pastes its URL in From GitHub. The bundle appears, with them as its author, and no admin acted.
- A member who is a requested reviewer of the PR adds it. After they submit a review on GitHub, the add still works.
- A member with no part in the PR gets `not_pull_participant`. A member with no linked GitHub account gets `github_not_linked`.
- A member who pastes a branch URL gets `admin_only`. An admin who pastes it gets the source.
- An agent connected with a member's `spy_` token calls `add_source` with the PR URL and gets the same results as the app.
- Close the PR on GitHub. Its bundles leave the bundle list, and "Closed pull requests (1)" shows them with the chip Closed. A Speccy link in the PR's GitHub review still opens the bundle.
- Reopen the PR. The bundle returns to the list, and the doc is editable.
- Merge a PR whose doc a main source already holds. That bundle leaves the list with the chip Merged. A PR doc that main did not hold stays in the list and follows main.
