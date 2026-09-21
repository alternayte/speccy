# Decisions in a pull request

## What it does
A team reviews PRDs and SDDs in GitHub pull requests, in a repo that Speccy did not write. `speccy init --github` writes `.speccy.yaml` and a workflow, with the checks that fail today already relaxed in adoption mode. The Action comments on a doc PR as before. A reply of `/speccy waive <reason>` or `/speccy ack <reason>` in a Speccy thread makes the next Action run commit a waiver to `.speccy/decisions/<doc path>.yaml` on the PR branch and resolve that thread. A reply of `/speccy enforce <slug>` commits the removal of a relaxed check. The waiver store is this sidecar, in every mode.

## Decisions
- The sidecar `.speccy/decisions/<doc path>.yaml` is the one on-disk store for waivers and acknowledgements — an adopted doc has no frontmatter and no trace IDs, and Speccy does not write into a doc it did not write.
- Frontmatter holds only `type` and links. DEC-009 is replaced, not amended — nobody uses Speccy yet, so two stores buy nothing.
- One file per doc — one PR can carry a PRD and an SDD, and two docs must not conflict in git.
- Two PRs that decide on the same doc conflict in git — they decide about the same text.
- An entry names the doc path, the heading path with an index for duplicate siblings, and a hash of the section text — a heading path is the only stable handle an adopted doc offers.
- A changed section hash, or a heading path that no longer resolves, ends the waiver with status invalidated — the existing ended waiver rule, with no new state.
- A reply command in a Speccy review thread records the decision — the conversation is already there.
- The Action commits the entry to the PR branch. It needs `contents: write`. On a fork PR it prints the exact YAML to paste — a fork gives no write token.
- Speccy runs no approver check. The requester is the commenter. The approver is whoever merges, filled in on the first default-branch run after the merge — a repo's branch protection is the authority, and a second approval config is a second thing to maintain.
- The comment and the check run give one verdict, computed with the waivers in the branch, and name the dependency: "Build Ready. 2 waivers in this PR are not merged yet. Without them: Not Build Ready." — an author must not read a self-granted waiver as an agreement.
- `speccy init --github` guesses the path mappings with the import guess, runs a lint-only pass, and preloads the failing check slugs into adoption mode — an adopted repo's honest first verdict is a wall of findings, and that reads as noise.
- The generated workflow needs no secret and is advisory. The summary comment names what the model stages add and the one secret to set — a team pays nothing to see the first signal.
- A default-branch run tests each relaxed check against every mapped doc. A check that passes everywhere gets named in the next summary comment with `/speccy enforce <slug>` — a relaxed check with no way back is a permanent lie in the verdict.
- Nothing leaves adoption mode by itself — a maintainer decides what the verdict means.
- In hosted mode a waiver approved in the app writes the entry into the same draft and publish pull request as doc edits (REQ-123). The inbox card says the approval lands in a pull request and stays pending until that PR merges — Speccy never changes the source branch.
- In local mode the app writes the sidecar to disk and the person commits it — local mode already treats doc text this way.

## Out
- Build question answers stay in the store. A divergence sends a person to the app or the report artifact.
- No change to the check rules, the review stages, the verdict rule, or the next action order.
- No write of any kind into a doc.
- No build packet or handoff from a pull request.
- No Speccy-side reading of CODEOWNERS or branch protection.

## How I know it works
- Run `speccy init --github` in a clone of a repo with loose PRDs and SDDs. It writes `.speccy.yaml` with path mappings and a relaxed check list, and `.github/workflows/speccy.yml` with no secrets.
- Open a PR that edits one mapped doc. The Action comments, sets one check run per bundle, and the comment says "Adoption mode: N checks relaxed".
- Reply `/speccy waive it ships in the next doc` on a Speccy inline comment. The next run commits `.speccy/decisions/docs/prd-payments.md.yaml`, resolves that thread, and the comment names the waiver and both verdicts.
- Edit the waived section. The next run reports the waiver as invalidated, and the verdict drops.
- Open the same PR from a fork. The comment prints the YAML to paste and posts no commit.
- Merge the PR. The next default-branch run fills the approver with the person who merged.
- Reply `/speccy enforce <slug>` for a check the comment offered. The next run commits the removal from `.speccy.yaml`, and the finding counts again.
- Approve a waiver in the app on a GitHub-sourced bundle. The entry appears in the publish pull request, and the inbox card stays pending until that PR merges.
