# The linked docs guide and the GitHub guide

## What it does
Two new documents join the guide. `docs/linked-docs.md` follows two linked bundles and shows traceability and coherence as they fire: a coverage gap, an acknowledgement that closes it, a contradiction, a restatement, and a stale verdict after an upstream edit. `docs/github.md` follows one repo: `speccy init --github`, a pull request with the summary comment and the check run, a reply that commits a waiver, then a source URL, an edit in Speccy, and a publish back as a pull request. Each document ends with a table for the reader who comes back with one finding. `just docs-shots` captures the local pictures, and `just docs-shots-github` captures the pull request pictures from a real pull request.

## Decisions
- Two documents, not new sections of the guide — the guide follows one doc and one person, and linked docs bring a second doc while the repo flow brings other people.
- Each document is a worked example first and a table last — a reader meets the checks in that order, and comes back later for one line.
- `docs/linked-docs.md` covers links, trace IDs, the matrix, coverage, contradiction, restatement, staleness, the standalone acknowledgement and the trace acknowledgement — they are one story: two docs that must agree.
- Its table names each check slug, its level, what it means, and the one thing to do — a person with a finding greps for the slug.
- `docs/github.md` covers the source URL, adoption mode and the ramp, the Action's comment and check run, the reply commands, the two verdicts of an unmerged waiver, forks, and publishing a draft — the repo flow runs in both directions, and a reader must see both to trust it.
- Its tables name the reply commands with what each one writes, and the workflow permissions with why each is needed.
- The README keeps the workflow snippet, advisory mode, and one line each on adoption mode and the reply commands, then links on — the README's job is to make someone try Speccy.
- The guide gains two pointers, one where a doc gains an upstream link and one in the CI section — a reader finds the next document where the question arises.
- `just docs-shots` gains the linked-docs pictures, from the payments PRD, the payments SDD, the contradicting SDD and the restated SDD in `testdata/bundles` — a picture the build regenerates stays true.
- `just docs-shots-github` makes the pull request pictures with no hand work — a picture nobody can regenerate drifts in silence.
- The pictures come from a public scratch repo, `alternayte/speccy-guide` — a headless browser screenshots a public pull request with no login, so no credential lives in the job.
- The job rebuilds the scratch repo each run: it commits the fixture specs and `.speccy.yaml` to the default branch, then branches from there — a fresh machine needs only the empty repo and a `gh` login.
- The job runs `speccy action` locally against the pull request, so the comment, the inline comments and the check runs are real — a mocked comment says nothing about the product.
- The job closes the pull request and deletes the branch when it is done, and leaves the repo — the next run starts from a known state.
- `BUILD.md` §6 says which pictures each job owns — the next person must not assume one job covers everything.

## Out
- No change to any check, the verdict rule, the Action, or the app.
- No new document for the CLI, hosted mode, or profiles. Those keep their documents.
- No screenshot of a private repo, and no signed-in browser session in a picture job.
- No creation of the scratch repo itself. The job says what to create when it is missing.
- No run of either picture job in CI.

## How I know it works
- Read `docs/linked-docs.md`. Every picture in it exists in `docs/images`, and the table lists every coherence and trace check slug the built-in profiles hold.
- Run `just docs-shots`. The linked-docs pictures are written again, and `git status` shows no other change.
- Read `docs/github.md`. It shows a real summary comment with a verdict, a reply command, and the commit that reply made.
- Run `just docs-shots-github` with a `gh` login. It commits the fixtures, opens a pull request on `alternayte/speccy-guide`, posts a real Speccy comment, writes the pictures into `docs/images`, then closes the pull request and deletes the branch.
- Run `just docs-shots-github` with no `gh` login. It stops and says to run `gh auth login`, and it changes no file.
- Open the README. Its GitHub section is shorter, and each of its lines points to `docs/github.md` for the detail.
- `just verify` passes, so the prose budget and the link checks hold.
